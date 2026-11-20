package evidence

// 证据重放引擎（6.2 可重放率的执行体）。
//
// 语义边界，和复测工具一致：
//   - 这里只负责「凭证据把请求重建出来、真的发一次、把事实摆出来」；
//     它不产生 verdict，也不判断漏洞是否仍然存在。
//   - replayed=true 的定义是「请求真的发出并拿到了目标响应」。这是可重放率的分子。
//   - matches_original=true 是更强的保真度信号（响应体逐字节等于原始证据），
//     单独统计，不与可重放率混为一谈。
//
// 判定依据只能是环境事实：重放拿到的是目标真实响应，不是模型推断。
// 因此本文件产出的结果可以直接作为 ground-truth 判定输入；LLM 自报不行。

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Kur1sulab/lance/db"
)

// 单次重放的边界。与 server 侧复测工具共用同一组量级：
//   - 请求体按快照 ReqLen 读，超限直接判失败而不是截断（截断等于换了请求，结论失真）。
//   - 响应只读前 MaxRespRead 字节做预览；哈希对完整响应流计算，因此 matches_original 仍可比。
const (
	MaxReplayBody     = 4 << 20
	MaxReplayRespRead = 1 << 20
	ReplayPreviewByte = 1500
	ReplayTimeout     = 45 * time.Second
)

// ReplayOptions 是一次重放的改写项（用于对照验证）。
type ReplayOptions struct {
	PayloadOverride string // 替换请求体
	URLOverride     string // 仅允许同 host 改路径/查询串
	HeaderOverride  string // 追加/覆盖请求头，每行一个
}

// ReplayOutcome 是一次重放的全部事实。
type ReplayOutcome struct {
	Method          string   `json:"method"`
	URL             string   `json:"url"`
	BodySource      string   `json:"body_source"`
	BodyLen         int      `json:"body_len"`
	Headers         []string `json:"headers"`
	Replayed        bool     `json:"replayed"`
	StatusCode      int      `json:"status_code"`
	StatusText      string   `json:"status_text"`
	RespLenRead     int64    `json:"resp_len_read"`
	RespSHA256      string   `json:"resp_sha256"`
	ContentType     string   `json:"content_type"`
	Preview         string   `json:"preview"`
	MatchesOriginal bool     `json:"matches_original"`
	Err             string   `json:"error,omitempty"`
	ElapsedMS       int64    `json:"elapsed_ms"`
}

// RequestBody 取回快照里的请求体：优先用 override，否则按 ReqLen 从证据库读。
func (s *Store) RequestBody(snap db.TrafficEvidenceSnapshot, override string) ([]byte, string, error) {
	if override != "" {
		return []byte(override), "override", nil
	}
	if snap.ReqLen <= 0 {
		return nil, "empty", nil
	}
	if snap.ReqLen > MaxReplayBody {
		return nil, "", fmt.Errorf("原始请求体 %d 字节超过重放上限 %d，请显式给出要重放的内容", snap.ReqLen, MaxReplayBody)
	}
	f, n, err := s.OpenBody(snap, "request")
	if err != nil {
		return nil, "", fmt.Errorf("读取原始请求体失败: %w", err)
	}
	defer f.Close()
	body, err := io.ReadAll(io.LimitReader(f, n))
	if err != nil {
		return nil, "", fmt.Errorf("读取原始请求体失败: %w", err)
	}
	return body, "snapshot", nil
}

// Replay 按快照重放一次真实请求。返回 error 只在「连请求都构造不出来」时
// （URL 非法、证据文件缺失、请求体超限）；连接层失败走 outcome.Err + Replayed=false，
// 因为那本身就是要记录的事实（不能证明可复现，也不能证明已修复）。
func (s *Store) Replay(ctx context.Context, snap db.TrafficEvidenceSnapshot, opts ReplayOptions) (ReplayOutcome, error) {
	out := ReplayOutcome{}

	target := strings.TrimSpace(snap.URL)
	var err error
	if opts.URLOverride != "" {
		if target, err = SameHostOverride(snap.URL, strings.TrimSpace(opts.URLOverride)); err != nil {
			return out, err
		}
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return out, fmt.Errorf("快照 URL 无效，无法重放: %s", snap.URL)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return out, fmt.Errorf("快照 URL 协议不支持重放: %s", u.Scheme)
	}

	body, bodySource, err := s.RequestBody(snap, opts.PayloadOverride)
	if err != nil {
		return out, err
	}
	method := strings.TrimSpace(snap.Method)
	if method == "" {
		method = http.MethodGet
	}
	out.Method = method
	out.URL = target
	out.BodySource = bodySource
	out.BodyLen = len(body)

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return out, fmt.Errorf("构造重放请求失败: %w", err)
	}
	out.Headers = ApplyReplayHeaders(req, snap.ReqHead, opts.HeaderOverride)

	client := &http.Client{
		Timeout: ReplayTimeout,
		// 要看到目标对这一次请求的原始回应：不跟随跳转、不走环境代理、不校验证书
		// （渗透目标常见自签/内部 CA）。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:             nil,
			TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
			DisableCompression: true,
		},
	}

	started := time.Now()
	resp, err := client.Do(req)
	out.ElapsedMS = time.Since(started).Milliseconds()
	if err != nil {
		out.Err = err.Error()
		return out, nil
	}
	defer resp.Body.Close()
	out.StatusCode = resp.StatusCode
	out.StatusText = resp.Status
	out.ContentType = resp.Header.Get("Content-Type")

	hasher := sha256.New()
	preview := make([]byte, 0, ReplayPreviewByte)
	limited := io.LimitReader(resp.Body, MaxReplayRespRead)
	buf := make([]byte, 32<<10)
	var total int64
	for {
		n, rerr := limited.Read(buf)
		if n > 0 {
			total += int64(n)
			hasher.Write(buf[:n])
			if room := ReplayPreviewByte - len(preview); room > 0 {
				take := n
				if take > room {
					take = room
				}
				preview = append(preview, buf[:take]...)
			}
		}
		if rerr == io.EOF {
			break
		}
		if rerr != nil {
			out.RespLenRead = total
			out.Err = "读取响应体中断: " + rerr.Error()
			return out, nil
		}
		if total >= MaxReplayRespRead {
			break
		}
	}
	out.RespLenRead = total
	out.RespSHA256 = hex.EncodeToString(hasher.Sum(nil))
	out.Preview = UTF8Preview(preview)
	out.MatchesOriginal = snap.RespHash != "" && strings.EqualFold(snap.RespHash, out.RespSHA256)
	// 拿到响应即算「凭证据重放过一次」——目标回了什么（200/500/403）不改变这一点。
	out.Replayed = true
	return out, nil
}

// SameHostOverride 允许改路径/查询串做参数对照，但不允许把请求引到别的目标。
func SameHostOverride(original, override string) (string, error) {
	base, err := url.Parse(original)
	if err != nil || base.Host == "" {
		return "", fmt.Errorf("快照 URL 无效，无法比对替换 URL: %s", original)
	}
	next, err := url.Parse(override)
	if err != nil || next.Host == "" {
		return "", fmt.Errorf("url_override 无效: %s", override)
	}
	if !strings.EqualFold(next.Scheme, base.Scheme) || !strings.EqualFold(next.Host, base.Host) {
		return "", fmt.Errorf("url_override 只能改路径或查询串，不能换协议或 host（原 %s://%s）", base.Scheme, base.Host)
	}
	return next.String(), nil
}

// ApplyReplayHeaders 还原快照请求头，再叠加覆盖项，返回最终发出去的头摘要。
//
// 兼容两种落库形态：带请求行的完整报文头（抓包原样）与只有头的块。Go 自管的
// 逐跳头（Content-Length/Transfer-Encoding/Connection）一律跳过，否则请求会被
// 自己写坏；Host 走 req.Host 才能生效。
func ApplyReplayHeaders(req *http.Request, head, override string) []string {
	sent := make([]string, 0, 12)
	set := func(name, value string) {
		lower := strings.ToLower(name)
		switch lower {
		case "host":
			req.Host = value
		case "content-length", "transfer-encoding", "connection", "proxy-connection", "keep-alive", "upgrade":
			return
		default:
			req.Header.Set(name, value)
		}
		for i, line := range sent {
			if strings.HasPrefix(strings.ToLower(line), lower+":") {
				sent[i] = name + ": " + value
				return
			}
		}
		sent = append(sent, name+": "+value)
	}
	first := true
	for _, line := range strings.Split(head, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.TrimSpace(line) == "" {
			continue
		}
		if first && LooksLikeRequestLine(line) {
			first = false
			continue
		}
		first = false
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		set(name, strings.TrimSpace(value))
	}
	for _, line := range strings.Split(override, "\n") {
		line = strings.TrimRight(line, "\r")
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		if name = strings.TrimSpace(name); name == "" {
			continue
		}
		set(name, strings.TrimSpace(value))
	}
	return sent
}

// LooksLikeRequestLine 判断一行是否是 "GET /x HTTP/1.1" 形态的请求行。
func LooksLikeRequestLine(line string) bool {
	fields := strings.Fields(line)
	if len(fields) < 3 {
		return false
	}
	if !strings.HasPrefix(strings.ToUpper(fields[len(fields)-1]), "HTTP/") {
		return false
	}
	for _, c := range fields[0] {
		if c < 'A' || c > 'Z' {
			return false
		}
	}
	return fields[0] != ""
}

// UTF8Preview 把响应预览转成合法 UTF-8（二进制响应不炸 JSON）。
func UTF8Preview(b []byte) string {
	s := string(b)
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}
