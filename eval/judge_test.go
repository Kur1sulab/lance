package eval

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newTestJudge(t *testing.T, roots ...string) *judgeEnv {
	t.Helper()
	if len(roots) == 0 {
		roots = []string{t.TempDir()}
	}
	return &judgeEnv{vars: newVars(), http: newEvalHTTPClient(), readRoots: roots}
}

func TestHTTPResponseProbe(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/secret":
			_, _ = w.Write([]byte("OBJECTIVE-abc123"))
		case "/forbidden":
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte("blocked by waf"))
		case "/redirect":
			http.Redirect(w, r, "/secret", http.StatusFound)
		}
	}))
	defer srv.Close()
	j := newTestJudge(t)
	ctx := context.Background()

	t.Run("命中", func(t *testing.T) {
		out := j.evaluate(ctx, Probe{ID: "p1", Kind: ProbeHTTPResponse, URL: srv.URL + "/secret",
			ExpectStatus: []int{200}, ExpectContains: []string{"OBJECTIVE-abc123"}})
		if !out.Hit {
			t.Fatalf("应命中: %+v", out)
		}
		if out.Observed.Status != 200 || out.Observed.RespSHA256 == "" || !strings.Contains(out.Observed.Preview, "OBJECTIVE") {
			t.Fatalf("观测事实不完整: %+v", out.Observed)
		}
	})

	t.Run("状态码不符即未命中", func(t *testing.T) {
		out := j.evaluate(ctx, Probe{ID: "p2", Kind: ProbeHTTPResponse, URL: srv.URL + "/forbidden",
			ExpectStatus: []int{200}, ExpectContains: []string{"OBJECTIVE-abc123"}})
		if out.Hit {
			t.Fatal("403 不该算命中")
		}
		if !strings.Contains(out.Reason, "403") {
			t.Fatalf("理由应点出状态码: %s", out.Reason)
		}
	})

	t.Run("禁止片段命中即未命中", func(t *testing.T) {
		out := j.evaluate(ctx, Probe{ID: "p3", Kind: ProbeHTTPResponse, URL: srv.URL + "/secret",
			ExpectContains: []string{"OBJECTIVE"}, ExpectNotContains: []string{"abc123"}})
		if out.Hit {
			t.Fatalf("出现禁止片段不该算命中: %+v", out)
		}
		if len(out.Observed.Forbidden) != 1 {
			t.Fatalf("应记录禁止片段: %+v", out.Observed)
		}
	})

	t.Run("不跟随重定向", func(t *testing.T) {
		out := j.evaluate(ctx, Probe{ID: "p4", Kind: ProbeHTTPResponse, URL: srv.URL + "/redirect",
			ExpectStatus: []int{302}})
		if !out.Hit {
			t.Fatalf("302 本身应作为判定依据（统一响应陷阱常靠跳转掩盖差异）: %+v", out)
		}
	})

	t.Run("靶不可达记 error 而不是未命中", func(t *testing.T) {
		out := j.evaluate(ctx, Probe{ID: "p5", Kind: ProbeHTTPResponse, URL: "http://127.0.0.1:1/",
			ExpectStatus: []int{200}, TimeoutMS: 500})
		if out.Error == "" {
			t.Fatal("连不上应记 error（无法判定），不能静默变成「未命中」")
		}
		if out.Hit {
			t.Fatal("error 不能算命中")
		}
	})
}

func TestFileReadProbe(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "access.log")
	content := "SERVED /data/www/../secret/objective.txt\nPROXY target=127.0.0.1:1 bytes=0\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	j := newTestJudge(t, dir)
	ctx := context.Background()

	out := j.evaluate(ctx, Probe{ID: "f1", Kind: ProbeFileRead, Path: logPath,
		ExpectContains: []string{"SERVED /data/www/../secret/objective.txt"}})
	if !out.Hit {
		t.Fatalf("应命中: %+v", out)
	}
	if out.Observed.FileBytes == 0 || out.Observed.FileSHA256 == "" {
		t.Fatalf("应记录文件事实: %+v", out.Observed)
	}

	out = j.evaluate(ctx, Probe{ID: "f2", Kind: ProbeFileRead, Path: logPath,
		ExpectContains: []string{"SERVED /etc/passwd"}})
	if out.Hit {
		t.Fatal("不含关键行不该命中")
	}
	if len(out.Observed.Missing) != 1 {
		t.Fatalf("应记录缺失片段: %+v", out.Observed)
	}

	// 路径越界必须被拦住：一道恶意题不能把评测机上的文件读进 scorecard。
	out = j.evaluate(ctx, Probe{ID: "f3", Kind: ProbeFileRead, Path: "/etc/passwd",
		ExpectContains: []string{"root"}})
	if out.Error == "" || out.Hit {
		t.Fatalf("越界路径应报错: %+v", out)
	}
}

func TestTCPConnectProbe(t *testing.T) {
	hub := newCallbackHub()
	defer hub.closeAll()
	l, err := hub.open("p-out", "127.0.0.1:0", "META-SERVICE 220 eval\n", "credential: CRED-xyz\n", "GET /creds")
	if err != nil {
		t.Fatalf("开监听器失败: %v", err)
	}
	j := &judgeEnv{vars: newVars(), http: newEvalHTTPClient(), callbacks: hub, readRoots: []string{t.TempDir()}}
	ctx := context.Background()

	// 没人连 → 未命中（这就是「目标没有出站」的判据）。
	out := j.evaluate(ctx, Probe{ID: "p-out", Kind: ProbeTCPConnect, TimeoutMS: 300})
	if out.Hit {
		t.Fatal("没有连接不该命中")
	}

	// 真的连一次 → 命中，并记录 peer/banner。
	done := make(chan struct{})
	go func() {
		defer close(done)
		c, err := net.Dial("tcp", l.addr)
		if err != nil {
			return
		}
		defer c.Close()
		// 监听器先读后写，所以客户端要先发请求行（和真 SSRF 的行为一致）。
		_, _ = c.Write([]byte("GET / HTTP/1.0\r\nHost: meta\r\n\r\n"))
		buf := make([]byte, 256)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, _ = c.Read(buf)
	}()
	out = j.evaluate(ctx, Probe{ID: "p-out", Kind: ProbeTCPConnect, TimeoutMS: 3000,
		ExpectClientPayloadContains: []string{"GET / HTTP/1.0"}})
	<-done
	if !out.Hit {
		t.Fatalf("真实连接应命中: %+v", out)
	}
	if out.Observed.TCPConns != 1 || !out.Observed.BannerSent || out.Observed.TCPPeer == "" {
		t.Fatalf("观测事实不完整: %+v", out.Observed)
	}
}

// TestCallbackCredentialGating 验证「凭据只在走到特定路径时才给」：
// 没有这一层，m2（元数据）和 m3（凭据）就会命中同一份响应，阶梯是假的。
func TestCallbackCredentialGating(t *testing.T) {
	hub := newCallbackHub()
	defer hub.closeAll()
	l, err := hub.open("p-cred", "127.0.0.1:0", "META-SERVICE 220 eval\n", "credential: CRED-gated\n", "GET /creds")
	if err != nil {
		t.Fatal(err)
	}
	dial := func(req string) string {
		c, err := net.Dial("tcp", l.addr)
		if err != nil {
			t.Fatalf("连监听器失败: %v", err)
		}
		defer c.Close()
		if _, err := c.Write([]byte(req)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 512)
		_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
		n, _ := c.Read(buf)
		return string(buf[:n])
	}
	if got := dial("GET / HTTP/1.0\r\n\r\n"); strings.Contains(got, "CRED-gated") {
		t.Fatalf("普通路径不该拿到凭据: %q", got)
	}
	if got := dial("GET /creds HTTP/1.0\r\n\r\n"); !strings.Contains(got, "CRED-gated") {
		t.Fatalf("走到 /creds 才应拿到凭据: %q", got)
	}
}

func TestVarsStrictResolution(t *testing.T) {
	v := newVars()
	v.set("canary:x", "TOKEN-1")
	got, err := v.resolve("prefix-{{canary:x}}-suffix")
	if err != nil || got != "prefix-TOKEN-1-suffix" {
		t.Fatalf("替换失败: %q %v", got, err)
	}
	if _, err := v.resolve("{{canary:nope}}"); err == nil {
		t.Fatal("未知占位符必须报错，否则判据会静默变成永假")
	}
	if _, err := v.resolveAll([]string{"ok", "{{bad}}"}); err == nil {
		t.Fatal("resolveAll 也应严格失败")
	}
}
