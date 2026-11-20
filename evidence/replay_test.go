package evidence

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Kur1sulab/lance/db"
)

// writeBlob 把字节按证据库的布局落盘（blobs/<hash[:2]>/<hash>.bin）。
func writeBlob(t *testing.T, store *Store, body string) string {
	t.Helper()
	sum := sha256.Sum256([]byte(body))
	hash := hex.EncodeToString(sum[:])
	path := filepath.Join(store.Dir, "blobs", hash[:2], hash+".bin")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return hash
}

// snapshotWithBody 造一条「请求体/响应体都在库里」的快照。
func snapshotWithBody(t *testing.T, store *Store, url, method, reqHead, reqBody, respBody string) db.TrafficEvidenceSnapshot {
	t.Helper()
	reqHash := writeBlob(t, store, reqBody)
	respHash := writeBlob(t, store, respBody)
	snap := db.TrafficEvidenceSnapshot{
		SourceTrafficID: "replay-engine-test",
		URL:             url, Method: method, Status: 200,
		ContentType: "text/plain",
		ReqHead:     reqHead,
		RespHead:    "HTTP/1.1 200 OK\r\nContent-Type: text/plain\r\n",
		ReqHash:     reqHash, RespHash: respHash,
		ReqLen: int64(len(reqBody)), RespLen: int64(len(respBody)),
	}
	snap.ID = db.TrafficSnapshotID(snap)
	return snap
}

// TestReplayEngineRoundTrip 是「凭证据重放」的核心：请求体从证据库取回、真发一次、
// 拿到目标响应、响应哈希与原始证据一致 → replayed + matches_original 都为真。
func TestReplayEngineRoundTrip(t *testing.T) {
	var gotBody, gotMethod, gotCustom string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotCustom = r.Header.Get("X-Evidence")
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		_, _ = w.Write([]byte("ok-from-target"))
	}))
	defer target.Close()

	store := New(nil, nil, t.TempDir())
	snap := snapshotWithBody(t, store, target.URL+"/x", "POST",
		"POST /x HTTP/1.1\r\nHost: 127.0.0.1\r\nContent-Length: 999\r\nX-Evidence: 1\r\n\r\n",
		"a=1", "ok-from-target")

	out, err := store.Replay(context.Background(), snap, ReplayOptions{})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if !out.Replayed {
		t.Fatalf("应重放成功: %+v", out)
	}
	if gotMethod != "POST" || gotBody != "a=1" {
		t.Errorf("请求没按证据重建: method=%s body=%q", gotMethod, gotBody)
	}
	if gotCustom != "1" {
		t.Errorf("快照请求头没还原: X-Evidence=%q", gotCustom)
	}
	if out.StatusCode != 200 || out.Preview != "ok-from-target" {
		t.Errorf("响应事实不对: %+v", out)
	}
	if !out.MatchesOriginal {
		t.Errorf("响应体与原始证据逐字节一致，应 matches_original=true: %+v", out)
	}
	if out.BodySource != "snapshot" {
		t.Errorf("body_source 应为 snapshot，got %s", out.BodySource)
	}
}

// TestReplayEnginePayloadOverride 证明「对照验证」会真的换请求体，且此时不再与原证据同哈希。
func TestReplayEnginePayloadOverride(t *testing.T) {
	var gotBody string
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 1024)
		n, _ := r.Body.Read(buf)
		gotBody = string(buf[:n])
		_, _ = w.Write([]byte("different-response"))
	}))
	defer target.Close()

	store := New(nil, nil, t.TempDir())
	snap := snapshotWithBody(t, store, target.URL+"/x", "POST",
		"POST /x HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n", "a=1", "ok-from-target")

	out, err := store.Replay(context.Background(), snap, ReplayOptions{PayloadOverride: "a=1' OR '1'='1"})
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if gotBody != "a=1' OR '1'='1" {
		t.Errorf("payload_override 未生效: %q", gotBody)
	}
	if out.BodySource != "override" {
		t.Errorf("body_source 应为 override，got %s", out.BodySource)
	}
	if out.MatchesOriginal {
		t.Error("换了 payload 后响应已变，不该冒充 matches_original")
	}
}

// TestReplayEngineConnectionFailure 证明「连不上」是 replayed=false + 原因，
// 而不是伪造一个成功：可重放率必须靠真实响应，不能靠乐观默认值。
func TestReplayEngineConnectionFailure(t *testing.T) {
	store := New(nil, nil, t.TempDir())
	// 127.0.0.1:1 基本必然拒绝连接。
	snap := snapshotWithBody(t, store, "http://127.0.0.1:1/x", "GET",
		"GET /x HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n", "", "whatever")

	out, err := store.Replay(context.Background(), snap, ReplayOptions{})
	if err != nil {
		t.Fatalf("构造请求本身不该失败: %v", err)
	}
	if out.Replayed {
		t.Fatal("连不上目标时不得记为可重放")
	}
	if out.Err == "" {
		t.Error("应记录失败原因")
	}
}

// TestReplayEngineRejectsCrossHostOverride 证明 URL 改写不能把请求引到别的目标。
func TestReplayEngineRejectsCrossHostOverride(t *testing.T) {
	store := New(nil, nil, t.TempDir())
	snap := snapshotWithBody(t, store, "http://127.0.0.1:8080/x", "GET",
		"GET /x HTTP/1.1\r\nHost: 127.0.0.1\r\n\r\n", "", "x")
	if _, err := store.Replay(context.Background(), snap, ReplayOptions{URLOverride: "http://evil.example.com/x"}); err == nil {
		t.Fatal("跨 host 的 url_override 必须被拒")
	}
	if _, err := SameHostOverride("http://127.0.0.1:8080/x", "https://127.0.0.1:8080/x"); err == nil {
		t.Fatal("换协议也必须被拒")
	}
	if got, err := SameHostOverride("http://127.0.0.1:8080/x", "http://127.0.0.1:8080/y?id=2"); err != nil || got != "http://127.0.0.1:8080/y?id=2" {
		t.Fatalf("同 host 改路径应放行: %q %v", got, err)
	}
}

// TestApplyReplayHeaders 覆盖逐跳头剥离、Host 走 req.Host、覆盖项优先。
func TestApplyReplayHeaders(t *testing.T) {
	req, _ := http.NewRequest("GET", "http://example.com/x", nil)
	sent := ApplyReplayHeaders(req,
		"GET /x HTTP/1.1\r\nHost: original.example\r\nAccept: */*\r\nContent-Length: 12\r\nCookie: a=1\r\n",
		"Cookie: b=2\r\nX-Extra: yes")
	if req.Host != "original.example" {
		t.Errorf("Host 应写入 req.Host, got %q", req.Host)
	}
	if req.Header.Get("Cookie") != "b=2" {
		t.Errorf("覆盖项应生效, got %q", req.Header.Get("Cookie"))
	}
	if req.Header.Get("X-Extra") != "yes" {
		t.Error("追加头应生效")
	}
	if req.Header.Get("Content-Length") != "" {
		t.Error("逐跳头 Content-Length 必须剥离")
	}
	if len(sent) == 0 {
		t.Error("应回显最终发出的头")
	}
}
