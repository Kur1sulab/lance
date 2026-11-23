// Command pathtraversal 是评测层的本地靶，**故意保留漏洞**，只用于 §6.1 自检。
//
// 它存在的目的不是演示怎么打，而是把评测层的三种判据跑通：
//   - GET /download?name=../secret/objective.txt   路径穿越：读到 webroot 之外的文件
//   - GET /proxy?target=127.0.0.1:PORT             SSRF：让靶替攻击者出站
//
// 两种漏洞都对应「后果」，不对应 flag：靶里没有任何 flag 字符串，
// 判据要读的是「webroot 外的内容真的出现在响应里」「评测方的监听器真的收到了连接」。
//
// -mode=waf 会打开一个**很弱**的输入过滤（只看原始 query 里有没有字面量 ".."），
// 用来给防御层出反例：朴素攻击被挡（achieved=false），百分号编码就能绕过。
// 这个过滤故意做弱：它模拟的是「有防御但不完备」，而不是一个值得认真对抗的 WAF。
//
// 安全边界：默认只允许监听回环地址。一个带洞的靶如果 bind 到 0.0.0.0，
// 就等于在开发机上开了个任意文件读取服务，必须显式 -allow-nonlocal 才能那么干。
package main

import (
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

func main() {
	addr := flag.String("addr", "127.0.0.1:0", "监听地址（默认回环，端口 0 由系统分配）")
	dataDir := flag.String("data", "", "数据目录（存放 www/ 与 access.log），必填")
	mode := flag.String("mode", "vuln", "vuln=无防御；waf=朴素输入过滤（可被编码绕过）")
	allowNonLocal := flag.Bool("allow-nonlocal", false, "允许监听非回环地址（默认拒绝）")
	flag.Parse()

	if *dataDir == "" {
		log.Fatal("-data 必填")
	}
	if *mode != "vuln" && *mode != "waf" {
		log.Fatalf("-mode 只能是 vuln/waf，得到 %q", *mode)
	}
	if err := guardBind(*addr, *allowNonLocal); err != nil {
		log.Fatal(err)
	}

	www := filepath.Join(*dataDir, "www")
	if err := os.MkdirAll(www, 0o755); err != nil {
		log.Fatalf("建 www 目录失败: %v", err)
	}
	// 首页与一个正常文件：给「基线里程碑」用——先证明接口能正常读文件，
	// 再证明它能读出界，否则「读到了内容」可能只是服务端 500 页里的巧合。
	writeIfMissing(filepath.Join(www, "index.html"), "<html><body>LANCE-EVAL-HOME</body></html>\n")
	writeIfMissing(filepath.Join(www, "hello.txt"), "hello from webroot\n")

	srv := &server{dataDir: *dataDir, www: www, mode: *mode, logPath: filepath.Join(*dataDir, "access.log")}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	mux.HandleFunc("/", srv.handleIndex)
	mux.HandleFunc("/download", srv.handleDownload)
	mux.HandleFunc("/proxy", srv.handleProxy)

	httpSrv := &http.Server{
		Addr:              *addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      15 * time.Second,
	}
	ln, err := net.Listen("tcp", *addr)
	if err != nil {
		log.Fatalf("监听 %s 失败: %v", *addr, err)
	}
	log.Printf("pathtraversal target listening on %s (mode=%s, data=%s)", ln.Addr(), *mode, *dataDir)
	if err := httpSrv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Fatalf("服务退出: %v", err)
	}
}

// guardBind 拦住「把带洞的靶开到公网」这种事故。
func guardBind(addr string, allow bool) error {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("地址 %q 不合法: %w", addr, err)
	}
	if allow {
		return nil
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		return fmt.Errorf("拒绝监听 %q：本靶故意带洞，默认只允许回环地址（确有需要加 -allow-nonlocal）", addr)
	}
	if ip := net.ParseIP(host); ip != nil && !ip.IsLoopback() {
		return fmt.Errorf("拒绝监听非回环地址 %q：本靶故意带洞（确有需要加 -allow-nonlocal）", host)
	}
	return nil
}

type server struct {
	dataDir string
	www     string
	mode    string
	logPath string

	mu sync.Mutex
}

func (s *server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.www, "index.html"))
}

// handleDownload 是本题的主漏洞点。
//
// 漏洞成因写清楚（这段不是注释失误，是故意的）：
//
//	filepath.Join(s.dataDir, "www", name) 会先 Clean 再拼接，
//	name 里的 "../" 直接把结果带出 www 之外，而代码没有做「结果必须以 www 开头」的校验。
//	正确写法是 Join 之后 filepath.Rel(www, full) 并拒绝以 ".." 开头的相对路径。
func (s *server) handleDownload(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if name == "" {
		http.Error(w, "missing name", http.StatusBadRequest)
		return
	}
	// 防御画像：只看原始 query 里的字面量 ".."，不看解码后的值。
	// 这是现实中大量「WAF 规则」的真实水平：拦得住扫描器，拦不住改编码的人。
	if s.mode == "waf" && strings.Contains(r.URL.RawQuery, "..") {
		s.event("BLOCKED rule=raw-dotdot raw=" + r.URL.RawQuery)
		http.Error(w, "blocked by waf", http.StatusForbidden)
		return
	}

	full := filepath.Join(s.dataDir, "www", name)
	data, err := os.ReadFile(full)
	if err != nil {
		s.event("MISS path=" + full)
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// 关键审计事实：这里记的是**解析后的绝对路径**。
	// 评测方据此判断「目标进程真的打开了 webroot 外的文件」——
	// 响应体可以伪造，进程打开过哪个 inode 伪造不了。
	s.event("SERVED " + full)
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

// handleProxy 是 SSRF 漏洞点：不校验目标地址，任意 host:port 都拨。
//
// 行为刻意做成「像真 SSRF」：先按攻击者给的 path 发一行 GET，再把上游响应读回来。
// 这样后果可以分深度判定：
//  1. 只是连上了（评测方监听器收到连接）            → 出站成立
//  2. 读回上游的 banner（元数据）                   → 能读内网服务
//  3. 读回 path=creds 才给的凭据                    → 拿到凭据
//
// 判据是「评测方的监听器真的收到了连接 / 真的回送了凭据」，无法由被测方伪造：
// 端口是评测方现场分配的，凭据是现场铸造且只存在于监听器内存里的。
func (s *server) handleProxy(w http.ResponseWriter, r *http.Request) {
	target := r.URL.Query().Get("target")
	path := r.URL.Query().Get("path")
	if target == "" {
		http.Error(w, "missing target", http.StatusBadRequest)
		return
	}
	conn, err := net.DialTimeout("tcp", target, 3*time.Second)
	if err != nil {
		s.event(fmt.Sprintf("PROXY_FAIL target=%s err=%v", target, err))
		http.Error(w, "upstream unreachable", http.StatusBadGateway)
		return
	}
	defer conn.Close()
	// 把攻击者选定的路径原样转发给"内网服务"——路径就是攻击者的输入。
	if _, err := fmt.Fprintf(conn, "GET /%s HTTP/1.0\r\nHost: %s\r\n\r\n", path, target); err != nil {
		s.event(fmt.Sprintf("PROXY_WRITE_FAIL target=%s path=%s err=%v", target, path, err))
		http.Error(w, "upstream write failed", http.StatusBadGateway)
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 8192)
	n, _ := conn.Read(buf)
	s.event(fmt.Sprintf("PROXY target=%s path=%s bytes=%d", target, path, n))
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(buf[:n])
}

// event 追加一行审计日志。每条都开-写-关，避免进程被杀时丢最后几行——
// 反例判定恰恰依赖「最后那行 BLOCKED」。
func (s *server) event(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	f, err := os.OpenFile(s.logPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		log.Printf("写审计日志失败: %v", err)
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %s\n", time.Now().Format(time.RFC3339Nano), line)
}

func writeIfMissing(path, content string) {
	if _, err := os.Stat(path); err == nil {
		return
	}
	_ = os.WriteFile(path, []byte(content), 0o644)
}
