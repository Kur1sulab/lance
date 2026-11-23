package eval

// outcome judge：判「攻击后果是否达成」，不判 flag。
//
// 三种 probe 都只读环境事实，没有任何一条依赖被测方自报：
//
//	http_response  关键响应特征（状态码 + 必含/禁含片段）。用于「敏感数据出现在响应里」；
//	file_read      宿主侧事实（目标自己写的审计日志、落盘产物）。用于「目标进程真的打开了
//	               webroot 外的文件」——响应可以伪造，进程打开过哪个 inode 伪造不了；
//	tcp_connect    评测方持有的监听器有没有真的收到连接。用于「目标替攻击者出站」，
//	               这是 SSRF/反连类漏洞唯一无法伪造的后果。
//
// 为什么不是 flag：flag 是靶里的一个字符串，位置固定、可以被读题/猜路径/从源码 grep 拿到；
// 拿到 flag 只证明「猜到了出题人的路径约定」，不证明打通了链路。后果判据反过来——
// 本次运行的哨兵由 runner 现场铸造（file 型写进 webroot 之外，inline 型只存在于监听器
// 内存里），靶内不存在任何静态答案，唯一拿到它的方式就是让目标真的执行那条攻击路径。
//
// 判定的严格性也在这里：probe 未命中就是 achieved=false，不做「部分可信」「疑似成功」
// 这类中间态。中间态没法计分，只会变成争论。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	// MaxProbeBodyRead 是判定时读取响应体的上限。判据只需要关键片段，
	// 但哈希要对「读到的全部字节」算，所以截断必须在计算前就固定。
	MaxProbeBodyRead = 1 << 20
	// MaxProbeFileRead 是 file_read 的读取上限。
	MaxProbeFileRead = 4 << 20
	// DefaultProbeTimeout 是单条 probe 的默认超时。
	DefaultProbeTimeout = 15 * time.Second
	// probePreviewLen 是写进 scorecard 的响应预览长度，够人眼复核又不撑爆报告。
	probePreviewLen = 400
)

// Observed 是一条 probe 拿到的原始事实。全部写进 scorecard，
// 让「为什么判 true/false」可以被人独立复核，而不是只能相信一个布尔值。
type Observed struct {
	Status      int      `json:"status,omitempty"`
	RespBytes   int      `json:"resp_bytes,omitempty"`
	RespSHA256  string   `json:"resp_sha256,omitempty"`
	Preview     string   `json:"preview,omitempty"`
	Truncated   bool     `json:"truncated,omitempty"`
	Matched     []string `json:"matched,omitempty"`
	Missing     []string `json:"missing,omitempty"`
	Forbidden   []string `json:"forbidden_hits,omitempty"`
	File        string   `json:"file,omitempty"`
	FileBytes   int64    `json:"file_bytes,omitempty"`
	FileSHA256  string   `json:"file_sha256,omitempty"`
	TCPConns    int      `json:"tcp_conns,omitempty"`
	TCPPeer     string   `json:"tcp_peer,omitempty"`
	TCPPayloads []string `json:"tcp_payloads,omitempty"`
	BannerSent  bool     `json:"banner_sent,omitempty"`
	Note        string   `json:"note,omitempty"`
}

// ProbeOutcome 是一条 probe 的判定结果。
type ProbeOutcome struct {
	ProbeID   string   `json:"probe_id"`
	Kind      string   `json:"kind"`
	Hit       bool     `json:"hit"`
	Reason    string   `json:"reason"`
	Observed  Observed `json:"observed"`
	Error     string   `json:"error,omitempty"`
	ElapsedMS int64    `json:"elapsed_ms"`
}

// judgeEnv 是判定所需的运行环境。
type judgeEnv struct {
	vars      *vars
	http      *http.Client
	callbacks *callbackHub
	// readRoots 限定 file_read 能读的范围。任务文件是代码评审的输入，
	// 但一道恶意题不该能把评测机上的任意文件读进 scorecard。
	readRoots []string
}

// newEvalHTTPClient 造一个专供评测用的客户端，三条约束都是有原因的：
//  1. Proxy=nil：本机靶绝不能被 HTTP_PROXY / Clash 之类劫走，否则判的是代理的行为；
//  2. 不跟随重定向：3xx 本身就是判据的一部分（统一响应陷阱常靠跳转掩盖差异）；
//  3. 连接不重用跨靶：每次运行新建客户端，避免上一个靶的 keep-alive 串到下一个。
func newEvalHTTPClient() *http.Client {
	tr := &http.Transport{
		Proxy:                 nil,
		DialContext:           (&net.Dialer{Timeout: 5 * time.Second}).DialContext,
		MaxIdleConns:          8,
		IdleConnTimeout:       20 * time.Second,
		ResponseHeaderTimeout: 20 * time.Second,
	}
	return &http.Client{
		Transport: tr,
		Timeout:   30 * time.Second,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
}

// evaluate 分发到具体 probe 类型。任何一处解析失败都返回 Error（= 无法判定），
// 而不是 Hit=false——「判不了」和「判不成立」必须分开，否则任务写错会被统计成能力不足。
func (j *judgeEnv) evaluate(ctx context.Context, p Probe) ProbeOutcome {
	start := time.Now()
	var out ProbeOutcome
	switch p.Kind {
	case ProbeHTTPResponse:
		out = j.evalHTTP(ctx, p)
	case ProbeFileRead:
		out = j.evalFile(p)
	case ProbeTCPConnect:
		out = j.evalTCP(ctx, p)
	default:
		out = ProbeOutcome{ProbeID: p.ID, Kind: p.Kind, Error: fmt.Sprintf("未知 probe 类型 %q", p.Kind)}
	}
	out.ProbeID = p.ID
	out.Kind = p.Kind
	out.ElapsedMS = time.Since(start).Milliseconds()
	return out
}

func (j *judgeEnv) evalHTTP(ctx context.Context, p Probe) ProbeOutcome {
	out := ProbeOutcome{ProbeID: p.ID, Kind: p.Kind}
	url, err := j.vars.resolve(p.URL)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "URL 占位符解析失败"
		return out
	}
	method := p.Method
	if method == "" {
		method = http.MethodGet
	}
	body, err := j.vars.resolve(p.Body)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "请求体占位符解析失败"
		return out
	}
	timeout := DefaultProbeTimeout
	if p.TimeoutMS > 0 {
		timeout = time.Duration(p.TimeoutMS) * time.Millisecond
	}
	reqCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var rdr io.Reader
	if body != "" {
		rdr = strings.NewReader(body)
	}
	req, err := http.NewRequestWithContext(reqCtx, method, url, rdr)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "构造请求失败"
		return out
	}
	for k, v := range p.Headers {
		rv, err := j.vars.resolve(v)
		if err != nil {
			out.Error = err.Error()
			out.Reason = "请求头占位符解析失败"
			return out
		}
		req.Header.Set(k, rv)
	}

	resp, err := j.http.Do(req)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "请求未拿到响应（靶可能已退出或端口不可达）"
		return out
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, MaxProbeBodyRead+1))
	if err != nil {
		out.Error = err.Error()
		out.Reason = "读响应体失败"
		return out
	}
	truncated := len(raw) > MaxProbeBodyRead
	if truncated {
		raw = raw[:MaxProbeBodyRead]
	}
	sum := sha256.Sum256(raw)
	out.Observed.Status = resp.StatusCode
	out.Observed.RespBytes = len(raw)
	out.Observed.RespSHA256 = hex.EncodeToString(sum[:])
	out.Observed.Truncated = truncated
	out.Observed.Preview = preview(raw)

	statusOK := true
	if len(p.ExpectStatus) > 0 {
		statusOK = false
		for _, want := range p.ExpectStatus {
			if resp.StatusCode == want {
				statusOK = true
				break
			}
		}
	}
	matched, missing, err := matchNeedles(raw, p.ExpectContains, j.vars)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "expect_contains 占位符解析失败"
		return out
	}
	// 注意取第一个返回值：禁止片段「命中」才是问题，取 matched 而不是 missing。
	forbidden, _, err := matchNeedles(raw, p.ExpectNotContains, j.vars)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "expect_not_contains 占位符解析失败"
		return out
	}
	out.Observed.Matched = matched
	out.Observed.Missing = missing
	out.Observed.Forbidden = forbidden

	hit := statusOK && len(missing) == 0 && len(forbidden) == 0
	out.Hit = hit
	switch {
	case !statusOK:
		out.Reason = fmt.Sprintf("状态码 %d 不在期望集合 %v 内", resp.StatusCode, p.ExpectStatus)
	case len(missing) > 0:
		out.Reason = fmt.Sprintf("响应缺少关键片段 %v（状态码 %d，%d 字节）", missing, resp.StatusCode, len(raw))
	case len(forbidden) > 0:
		out.Reason = fmt.Sprintf("响应出现禁止片段 %v", forbidden)
	default:
		out.Reason = fmt.Sprintf("关键响应特征命中（状态码 %d，%d 字节，SHA256 %s）", resp.StatusCode, len(raw), short(out.Observed.RespSHA256))
	}
	return out
}

func (j *judgeEnv) evalFile(p Probe) ProbeOutcome {
	out := ProbeOutcome{ProbeID: p.ID, Kind: p.Kind}
	path, err := j.vars.resolve(p.Path)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "路径占位符解析失败"
		return out
	}
	abs, err := j.allowedPath(path)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "file_read 路径不在允许范围内"
		return out
	}
	f, err := os.Open(abs)
	if err != nil {
		out.Observed.File = abs
		out.Error = err.Error()
		out.Reason = "读不到目标文件（文件不存在往往本身就是一个有效结论：攻击没有造成落盘后果）"
		return out
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, MaxProbeFileRead+1))
	if err != nil {
		out.Error = err.Error()
		out.Reason = "读文件失败"
		return out
	}
	if len(raw) > MaxProbeFileRead {
		raw = raw[:MaxProbeFileRead]
	}
	sum := sha256.Sum256(raw)
	out.Observed.File = abs
	out.Observed.FileBytes = int64(len(raw))
	out.Observed.FileSHA256 = hex.EncodeToString(sum[:])
	// 文件预览同样落进 scorecard：反例题要靠这行日志证明「防御真的拦了」，
	// 而不是只看到一个 achieved=false。
	out.Observed.Preview = preview(raw)

	matched, missing, err := matchNeedles(raw, p.ExpectContains, j.vars)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "expect_contains 占位符解析失败"
		return out
	}
	// 注意取第一个返回值：禁止片段「命中」才是问题，取 matched 而不是 missing。
	forbidden, _, err := matchNeedles(raw, p.ExpectNotContains, j.vars)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "expect_not_contains 占位符解析失败"
		return out
	}
	out.Observed.Matched = matched
	out.Observed.Missing = missing
	out.Observed.Forbidden = forbidden
	out.Hit = len(missing) == 0 && len(forbidden) == 0
	if out.Hit {
		out.Reason = fmt.Sprintf("宿主侧事实命中：%s 含 %v（%d 字节）", abs, matched, len(raw))
	} else if len(missing) > 0 {
		out.Reason = fmt.Sprintf("宿主侧事实未命中：%s 缺少 %v", abs, missing)
	} else {
		out.Reason = fmt.Sprintf("宿主侧事实出现禁止片段 %v", forbidden)
	}
	return out
}

func (j *judgeEnv) evalTCP(ctx context.Context, p Probe) ProbeOutcome {
	out := ProbeOutcome{ProbeID: p.ID, Kind: p.Kind}
	if j.callbacks == nil {
		out.Error = "没有回调监听器（tcp_connect probe 未预分配）"
		out.Reason = "判定环境缺监听器"
		return out
	}
	cb := j.callbacks.get(p.ID)
	if cb == nil {
		out.Error = fmt.Sprintf("probe %s 没有对应的回调监听器", p.ID)
		out.Reason = "判定环境缺监听器"
		return out
	}
	timeout := 5 * time.Second
	if p.TimeoutMS > 0 {
		timeout = time.Duration(p.TimeoutMS) * time.Millisecond
	}
	conns := cb.wait(ctx, timeout)
	out.Observed.TCPConns = len(conns)
	if len(conns) == 0 {
		out.Hit = false
		out.Reason = fmt.Sprintf("%v 内评测方监听器 %s 没有收到任何连接：目标没有出站", timeout, cb.addr)
		return out
	}
	out.Observed.TCPPeer = conns[0].Peer
	out.Observed.BannerSent = conns[0].BannerSent
	for _, c := range conns {
		out.Observed.TCPPayloads = append(out.Observed.TCPPayloads, c.Payload)
	}
	missing, err := payloadMissing(conns, p.ExpectClientPayloadContains, j.vars)
	if err != nil {
		out.Error = err.Error()
		out.Reason = "expect_client_payload_contains 占位符解析失败"
		return out
	}
	out.Observed.Missing = missing
	out.Hit = len(missing) == 0
	if out.Hit {
		out.Reason = fmt.Sprintf("目标真实出站：监听器 %s 收到 %d 条连接（peer %s，banner 已回送=%v）",
			cb.addr, len(conns), conns[0].Peer, conns[0].BannerSent)
	} else {
		out.Reason = fmt.Sprintf("收到 %d 条连接但客户端载荷缺少 %v", len(conns), missing)
	}
	return out
}

// allowedPath 把 probe 路径限制在允许的根内。
func (j *judgeEnv) allowedPath(p string) (string, error) {
	if len(j.readRoots) == 0 {
		return "", fmt.Errorf("没有配置可读根目录")
	}
	var lastErr error
	for _, root := range j.readRoots {
		abs, err := ensureInside(root, p)
		if err == nil {
			return abs, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("路径 %s 不在允许范围内", p)
	}
	return "", lastErr
}

// matchNeedles 检查 raw 是否包含全部 needles。返回命中项与缺失项。
func matchNeedles(raw []byte, needles []string, v *vars) (matched, missing []string, err error) {
	for _, n := range needles {
		rn, err := v.resolve(n)
		if err != nil {
			return nil, nil, err
		}
		if bytes.Contains(raw, []byte(rn)) {
			matched = append(matched, rn)
		} else {
			missing = append(missing, rn)
		}
	}
	return matched, missing, nil
}

func payloadMissing(conns []tcpObservation, needles []string, v *vars) ([]string, error) {
	var missing []string
	for _, n := range needles {
		rn, err := v.resolve(n)
		if err != nil {
			return nil, err
		}
		found := false
		for _, c := range conns {
			if strings.Contains(c.Payload, rn) {
				found = true
				break
			}
		}
		if !found {
			missing = append(missing, rn)
		}
	}
	return missing, nil
}

// preview 生成可读预览，非 UTF-8 字节用 \xNN 转义，避免 scorecard 变成乱码。
func preview(raw []byte) string {
	if len(raw) > probePreviewLen {
		raw = raw[:probePreviewLen]
	}
	var b strings.Builder
	for _, c := range raw {
		switch {
		case c == '\n' || c == '\r' || c == '\t':
			b.WriteByte(c)
		case c >= 0x20 && c < 0x7f:
			b.WriteByte(c)
		case c >= 0x80:
			// 直接写回原字节，让中文正常显示；只有控制字符需要转义。
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "\\x%02x", c)
		}
	}
	return b.String()
}

// tcpObservation 是一条回调连接的事实。
type tcpObservation struct {
	Peer       string
	Payload    string
	BannerSent bool
	At         time.Time
}

// callbackListener 是评测方持有的 TCP 监听器。它存在的唯一目的：
// 让「目标有没有真的出站」变成一个不可伪造的事实。
//
// 回送分两段：banner 总是回送（证明双向可达）；credentialBanner 只在客户端载荷
// 命中 credentialOn 时追加（证明攻击方真的让目标发出了那一步请求）。
// 这样「读到元数据」和「拿到凭据」是两个不同深度的后果，而不是同一份数据判两次。
type callbackListener struct {
	id         string
	ln         net.Listener
	addr       string
	banner     string
	credBanner string
	credOn     string

	mu    sync.Mutex
	conns []tcpObservation
}

func (l *callbackListener) serve() {
	for {
		c, err := l.ln.Accept()
		if err != nil {
			return
		}
		go l.handle(c)
	}
}

func (l *callbackListener) handle(c net.Conn) {
	defer c.Close()
	// 先读一帧客户端载荷：SSRF 靶通常会把攻击者给的请求行发过来，
	// 这一帧决定了「该不该给凭据」。读不到也没关系——banner 照样回送，
	// 至少证明目标真的出站了（m1 的判据）。
	_ = c.SetReadDeadline(time.Now().Add(1200 * time.Millisecond))
	buf := make([]byte, 4096)
	n, _ := c.Read(buf)
	payload := string(buf[:n])

	resp := l.banner
	if l.credBanner != "" && l.credOn != "" && strings.Contains(payload, l.credOn) {
		resp += l.credBanner
	}
	bannerSent := false
	if resp != "" {
		_ = c.SetWriteDeadline(time.Now().Add(2 * time.Second))
		if _, err := c.Write([]byte(resp)); err == nil {
			bannerSent = true
		}
	}
	obs := tcpObservation{Peer: c.RemoteAddr().String(), BannerSent: bannerSent, At: time.Now()}
	if n > 0 {
		obs.Payload = payload
	}
	l.mu.Lock()
	l.conns = append(l.conns, obs)
	l.mu.Unlock()
}

func (l *callbackListener) wait(ctx context.Context, timeout time.Duration) []tcpObservation {
	deadline := time.Now().Add(timeout)
	for {
		l.mu.Lock()
		if len(l.conns) > 0 {
			out := append([]tcpObservation(nil), l.conns...)
			l.mu.Unlock()
			return out
		}
		l.mu.Unlock()
		if time.Now().After(deadline) {
			return nil
		}
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(50 * time.Millisecond):
		}
	}
}

func (l *callbackListener) close() {
	_ = l.ln.Close()
}

// callbackHub 按 probe id 管理回调监听器。必须在执行攻击动作之前全部打开：
// 攻击动作里要用 {{callback:<probe_id>}} 指向这些端口。
type callbackHub struct {
	m map[string]*callbackListener
}

func newCallbackHub() *callbackHub {
	return &callbackHub{m: map[string]*callbackListener{}}
}

func (h *callbackHub) open(id, listen, banner, credBanner, credOn string) (*callbackListener, error) {
	if h.m[id] != nil {
		return nil, fmt.Errorf("回调监听器 id 重复: %s", id)
	}
	if listen == "" {
		listen = "127.0.0.1:0"
	}
	ln, err := net.Listen("tcp", listen)
	if err != nil {
		return nil, fmt.Errorf("打开回调监听器 %s 失败: %w", id, err)
	}
	l := &callbackListener{id: id, ln: ln, addr: ln.Addr().String(), banner: banner, credBanner: credBanner, credOn: credOn}
	h.m[id] = l
	go l.serve()
	return l, nil
}

func (h *callbackHub) get(id string) *callbackListener {
	return h.m[id]
}

func (h *callbackHub) closeAll() {
	for _, l := range h.m {
		l.close()
	}
}
