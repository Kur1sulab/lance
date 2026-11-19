package server

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/Kur1sulab/lance/db"
	"github.com/Kur1sulab/lance/intercept"
	actool "github.com/Kur1sulab/norma/tool"
)

// 复测重放的一次性边界。这些都是「一次请求」的量级，不涉及全量扫描：
//   - 请求体按快照的 ReqLen 读，超限直接判失败而不是截断（截断后重放等于换了
//     一个请求，结论会失真，不如明确报错）。
//   - 响应只读前 replayMaxRespRead 字节做预览；哈希是对完整响应流计算的，
//     因此 matches_original 仍然可比。
const (
	replayMaxBody     = 4 << 20
	replayMaxRespRead = 1 << 20
	replayPreviewByte = 1500
	replayTimeout     = 45 * time.Second
)

// replayFindingTraffic 的判定语义：它只负责「发一次请求并把事实摆出来」，
// 不产生 verdict。verdict 由 record_finding_retest_result 依据本次实测 +
// 原证据给出，两者分开才能让「一次请求失败不等于已修复」这条规则落得下地。
func (s *Server) replayFindingTrafficTool() actool.CoreTool {
	return wrTool("replay_finding_traffic",
		"按当前复测漏洞绑定的原始流量快照重放一次真实请求（不是读取历史记录，是真的再发一次），返回本次实际状态码、响应长度、响应哈希与原响应的比对及响应预览。可选改写请求体/URL 查询串/补充请求头用于对照验证（如把 payload 换回原值、补登录 Cookie、加 X-Forwarded-For）。只能重放已绑定到本漏洞的快照，不能指定任意目标。一次未命中不能证明已修复，请结合原证据判断。",
		objSchema(map[string]any{
			"binding_id":       strParam("可选：要重放的流量绑定 ID（取自 get_finding_retest_context 的 bindings[].id）。留空则用该漏洞第一条 request 角色的绑定"),
			"payload_override": strParam("可选：替换原始请求体，用于对照验证。留空则按快照原样重放"),
			"url_override":     strParam("可选：替换请求 URL（仅允许与快照同 host 改路径/查询串，用于调节参数做对照）"),
			"header_override":  strParam("可选：追加或覆盖请求头，每行一个，如 \"Cookie: sid=abc\"。留空则用快照原始请求头"),
			"note":             strParam("可选：本次重放的意图说明，会回显在结果里"),
		}), func(ctx context.Context, in json.RawMessage) (actool.Result, error) {
			return s.replayFindingTraffic(ctx, in)
		})
}

func (s *Server) replayFindingTraffic(ctx context.Context, in json.RawMessage) (actool.Result, error) {
	var a struct {
		BindingID string `json:"binding_id"`
		Payload   string `json:"payload_override"`
		URL       string `json:"url_override"`
		Headers   string `json:"header_override"`
		Note      string `json:"note"`
	}
	if err := json.Unmarshal(in, &a); err != nil {
		return actool.Errorf("参数解析失败: " + err.Error()), nil
	}
	r, err := s.m.pg.FindingRetestForConversation(ctx, intercept.ConvIDFromContext(ctx))
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if r == nil {
		return actool.Errorf("当前会话未关联复测记录，请从漏洞详情发起复测"), nil
	}
	ft, err := s.m.pg.GetFindingTraffic(ctx, r.FindingID)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	if ft == nil || len(ft.Bindings) == 0 {
		return actool.Errorf(fmt.Sprintf("漏洞 #%d 没有绑定任何流量快照，无法按原报文重放；请先在漏洞详情里绑定原始请求，或改用其他方式验证", r.FindingID)), nil
	}
	binding, err := pickReplayBinding(ft.Bindings, a.BindingID)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	snap := binding.Snapshot

	target := strings.TrimSpace(snap.URL)
	if a.URL != "" {
		if target, err = sameHostOverride(snap.URL, strings.TrimSpace(a.URL)); err != nil {
			return actool.Errorf(err.Error()), nil
		}
	}
	u, err := url.Parse(target)
	if err != nil || u.Host == "" {
		return actool.Errorf("快照 URL 无效，无法重放: " + snap.URL), nil
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return actool.Errorf("快照 URL 协议不支持重放: " + u.Scheme), nil
	}

	body, bodySource, err := s.replayRequestBody(snap, a.Payload)
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}
	method := strings.TrimSpace(snap.Method)
	if method == "" {
		method = http.MethodGet
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bytes.NewReader(body))
	if err != nil {
		return actool.Errorf("构造重放请求失败: " + err.Error()), nil
	}
	sent := applyReplayHeaders(req, snap.ReqHead, a.Headers)

	client := &http.Client{
		Timeout: replayTimeout,
		// 复测要看到目标对这一次请求的原始回应：不跟随跳转、不走环境代理、
		// 不校验证书（渗透目标常见自签/内部 CA）。
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport: &http.Transport{
			Proxy:              nil,
			TLSClientConfig:    &tls.Config{InsecureSkipVerify: true},
			DisableCompression: true,
		},
	}

	started := time.Now()
	resp, err := client.Do(req)
	elapsed := time.Since(started).Milliseconds()
	if err != nil {
		return jsonResult(map[string]any{
			"finding_id":     strconv.FormatInt(r.FindingID, 10),
			"binding_id":     strconv.FormatInt(binding.ID, 10),
			"replayed":       false,
			"request":        map[string]any{"method": method, "url": target},
			"error":          err.Error(),
			"elapsed_ms":     elapsed,
			"note":           strings.TrimSpace(a.Note),
			"interpretation": "本次请求根本没有拿到响应（连接/DNS/TLS/超时层面失败），既不能证明仍可复现，也不能证明已修复；结论应为 inconclusive 并写清失败原因。",
		})
	}
	defer resp.Body.Close()

	hasher := sha256.New()
	preview := make([]byte, 0, replayPreviewByte)
	limited := io.LimitReader(resp.Body, replayMaxRespRead)
	buf := make([]byte, 32<<10)
	var total int64
	for {
		n, rerr := limited.Read(buf)
		if n > 0 {
			total += int64(n)
			hasher.Write(buf[:n])
			if room := replayPreviewByte - len(preview); room > 0 {
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
			return jsonResult(map[string]any{
				"finding_id": strconv.FormatInt(r.FindingID, 10),
				"binding_id": strconv.FormatInt(binding.ID, 10),
				"replayed":   false,
				"request":    map[string]any{"method": method, "url": target},
				"response":   map[string]any{"status": resp.StatusCode},
				"error":      "读取响应体中断: " + rerr.Error(),
				"elapsed_ms": elapsed,
			})
		}
		if total >= replayMaxRespRead {
			break
		}
	}
	respHash := hex.EncodeToString(hasher.Sum(nil))
	matches := snap.RespHash != "" && strings.EqualFold(snap.RespHash, respHash)

	return jsonResult(map[string]any{
		"finding_id": strconv.FormatInt(r.FindingID, 10),
		"binding_id": strconv.FormatInt(binding.ID, 10),
		"replayed":   true,
		"request": map[string]any{
			"method":      method,
			"url":         target,
			"body_source": bodySource,
			"body_len":    len(body),
			"headers":     sent,
		},
		"response": map[string]any{
			"status":           resp.StatusCode,
			"status_text":      resp.Status,
			"resp_len_read":    total,
			"resp_sha256":      respHash,
			"matches_original": matches,
			"content_type":     resp.Header.Get("Content-Type"),
			"preview":          utf8Preview(preview),
		},
		"original": map[string]any{
			"status":      snap.Status,
			"resp_len":    snap.RespLen,
			"resp_sha256": snap.RespHash,
			"method":      snap.Method,
			"url":         snap.URL,
		},
		"elapsed_ms":     elapsed,
		"note":           strings.TrimSpace(a.Note),
		"interpretation": "这是本次真实发出的一个请求。matches_original=true 表示响应体与原始证据逐字节一致（行为未变）；不同则要结合状态码与原 PoC 的关键特征判断是「已修复」还是「环境/前置条件不同导致未命中」。单次未命中不足以判 fixed。",
	})
}

// replayRequestBody 优先用调用方给的 payload 做对照，否则按快照原样取回请求体。
func (s *Server) replayRequestBody(snap db.TrafficEvidenceSnapshot, override string) ([]byte, string, error) {
	if override != "" {
		return []byte(override), "override", nil
	}
	if snap.ReqLen <= 0 {
		return nil, "empty", nil
	}
	if snap.ReqLen > replayMaxBody {
		return nil, "", fmt.Errorf("原始请求体 %d 字节超过重放上限 %d，请用 payload_override 显式给出要重放的内容", snap.ReqLen, replayMaxBody)
	}
	f, n, err := s.evidenceStore().OpenBody(snap, "request")
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

func pickReplayBinding(list []db.FindingTrafficBinding, want string) (db.FindingTrafficBinding, error) {
	if want != "" {
		id, err := strconv.ParseInt(strings.TrimSpace(want), 10, 64)
		if err != nil {
			return db.FindingTrafficBinding{}, fmt.Errorf("binding_id 无效: %s", want)
		}
		for _, b := range list {
			if b.ID == id {
				return b, nil
			}
		}
		return db.FindingTrafficBinding{}, fmt.Errorf("漏洞未绑定 ID 为 %d 的流量快照", id)
	}
	for _, b := range list {
		if strings.EqualFold(b.Role, "request") {
			return b, nil
		}
	}
	return list[0], nil
}

// sameHostOverride 允许改路径/查询串做参数对照，但不允许把请求引到别的目标。
func sameHostOverride(original, override string) (string, error) {
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

// applyReplayHeaders 还原快照请求头，再叠加调用方的覆盖项，返回最终发出去的头摘要。
//
// 兼容两种落库形态：带请求行的完整报文头（抓包原样）与只有头的块。Go 自管的
// 逐跳头（Content-Length/Transfer-Encoding/Connection）一律跳过，否则请求会被
// 自己写坏；Host 走 req.Host 才能生效。
func applyReplayHeaders(req *http.Request, head, override string) []string {
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
		if first && looksLikeRequestLine(line) {
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

func looksLikeRequestLine(line string) bool {
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

func utf8Preview(b []byte) string {
	s := string(b)
	if utf8.ValidString(s) {
		return s
	}
	return strings.ToValidUTF8(s, "\uFFFD")
}
