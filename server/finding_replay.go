package server

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/Kur1sulab/lance/db"
	"github.com/Kur1sulab/lance/evidence"
	"github.com/Kur1sulab/lance/intercept"
	actool "github.com/Kur1sulab/norma/tool"
)

// 复测重放的判定语义：它只负责「发一次请求并把事实摆出来」，不产生 verdict。
// verdict 由 record_finding_retest_result 依据本次实测 +
// 原证据给出，两者分开才能让「一次请求失败不等于已修复」这条规则落得下地。
//
// 传输层实现在 evidence.Replay —— 与 6.2 的可重放率指标共用同一份代码，
// 避免「工具能重放、指标算不出可重放」这种两套实现的口径分裂。
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

	out, err := s.evidenceStore().Replay(ctx, snap, evidence.ReplayOptions{
		PayloadOverride: a.Payload,
		URLOverride:     strings.TrimSpace(a.URL),
		HeaderOverride:  a.Headers,
	})
	if err != nil {
		return actool.Errorf(err.Error()), nil
	}

	findingID := strconv.FormatInt(r.FindingID, 10)
	bindingID := strconv.FormatInt(binding.ID, 10)
	request := map[string]any{"method": out.Method, "url": out.URL}

	if !out.Replayed {
		res := map[string]any{
			"finding_id": findingID,
			"binding_id": bindingID,
			"replayed":   false,
			"request":    request,
			"error":      out.Err,
			"elapsed_ms": out.ElapsedMS,
			"note":       strings.TrimSpace(a.Note),
			"interpretation": "本次请求根本没有拿到响应（连接/DNS/TLS/超时层面失败），既不能证明仍可复现，" +
				"也不能证明已修复；结论应为 inconclusive 并写清失败原因。",
		}
		if out.StatusCode != 0 {
			res["response"] = map[string]any{"status": out.StatusCode}
		}
		return jsonResult(res)
	}

	request["body_source"] = out.BodySource
	request["body_len"] = out.BodyLen
	request["headers"] = out.Headers

	return jsonResult(map[string]any{
		"finding_id": findingID,
		"binding_id": bindingID,
		"replayed":   true,
		"request":    request,
		"response": map[string]any{
			"status":           out.StatusCode,
			"status_text":      out.StatusText,
			"resp_len_read":    out.RespLenRead,
			"resp_sha256":      out.RespSHA256,
			"matches_original": out.MatchesOriginal,
			"content_type":     out.ContentType,
			"preview":          out.Preview,
		},
		"original": map[string]any{
			"status":      snap.Status,
			"resp_len":    snap.RespLen,
			"resp_sha256": snap.RespHash,
			"method":      snap.Method,
			"url":         snap.URL,
		},
		"elapsed_ms": out.ElapsedMS,
		"note":       strings.TrimSpace(a.Note),
		"interpretation": "这是本次真实发出的一个请求。matches_original=true 表示响应体与原始证据逐字节一致（行为未变）；" +
			"不同则要结合状态码与原 PoC 的关键特征判断是「已修复」还是「环境/前置条件不同导致未命中」。单次未命中不足以判 fixed。",
	})
}

// pickReplayBinding 选中要重放的绑定：显式 id 优先，否则第一条 request 角色，再退第一条。
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
