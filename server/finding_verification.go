package server

// 6.2 验证质量一等闸门的 HTTP 面。
//
// 对外契约（写死在代码里，改口径必须同步改 docs/finding-verification-gate-zh.md）：
//
//	GET  /api/metrics/verification                     两个指标 + 样本数
//	GET  /api/exploration/findings/{id}/verification   单条发现的验证现状 + 流水
//	POST /api/exploration/findings/{id}/verification   ground-truth 判定（verified/refuted/unreproducible）
//	POST /api/exploration/findings/{id}/replay         凭证据重放一次，落重放流水
//
// 判定入口的硬规则：**不接受 LLM / Agent 自报**。method 必须是环境事实来源
// （replay / manual_replay / target_response），且必须附 ground_truth（目标真实响应片段、
// 独立复现的命令输出、重放结果）。db.IsGroundTruthMethod 会拒绝任何带 llm/agent/model/
// self_report 字样的 method，并把原因回给调用方。这条不是风格问题：XBOW 的误报经济学
// 决定了 0–10% 误报率才有赏金正循环，而模型自报的「已验证」会把误报率变成噪声。
//
// 报告导出路径（server.findingsExport）只放行 verification_status='verified' 的发现，
// 未过闸门的一律 409 并列出被拦条目——是后端硬拦，不是前端隐藏。

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/Kur1sulab/lance/db"
	"github.com/Kur1sulab/lance/evidence"
)

// getVerificationMetrics 返回全库（或指定 task）的两个一等指标。
func (s *Server) getVerificationMetrics(w http.ResponseWriter, r *http.Request) {
	filter := db.VerificationMetricsFilter{}
	if v := strings.TrimSpace(r.URL.Query().Get("task_id")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			writeErr(w, 400, "task_id 无效: "+v)
			return
		}
		filter.TaskID = id
	}
	m, err := s.m.pg.VerificationMetrics(r.Context(), filter)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"metrics": m,
		// 口径与状态机随指标一起返回：调用方不用猜，也不会有人拿错分母。
		"statuses":    db.VerificationStatuses(),
		"transitions": verificationTransitionTable(),
		"methods": []string{
			db.VerifyMethodReplay, db.VerifyMethodManualReplay, db.VerifyMethodTargetResponse,
		},
		"ground_truth_only": true,
		"rejects_llm_self_report": true,
	})
}

func verificationTransitionTable() map[string][]string {
	out := map[string][]string{}
	for _, from := range db.VerificationStatuses() {
		for _, to := range db.VerificationStatuses() {
			if db.CanTransitionVerification(from, to) {
				out[from] = append(out[from], to)
			}
		}
	}
	return out
}

func (s *Server) getFindingVerification(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	st, err := s.m.pg.GetFindingVerification(r.Context(), id)
	if errors.Is(err, db.ErrFindingNotFound) {
		writeErr(w, 404, "finding not found")
		return
	}
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

// setFindingVerification 是唯一的判定入口：人拿环境事实来标 verified / refuted /
// unreproducible。这里不做任何「自动判定」——自动判定等于让被测对象给自己打分。
func (s *Server) setFindingVerification(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	var in struct {
		Status      string `json:"status"`
		Method      string `json:"method"`
		Actor       string `json:"actor"`
		Note        string `json:"note"`
		GroundTruth string `json:"ground_truth"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in); err != nil {
		writeErr(w, 400, "请求体不是合法 JSON: "+err.Error())
		return
	}
	st, err := s.m.pg.SetFindingVerification(r.Context(), db.VerificationInput{
		FindingID:   id,
		Status:      in.Status,
		Method:      in.Method,
		Actor:       in.Actor,
		Note:        in.Note,
		GroundTruth: in.GroundTruth,
	})
	switch {
	case errors.Is(err, db.ErrFindingNotFound):
		writeErr(w, 404, "finding not found")
		return
	case db.IsVerificationInvalid(err):
		// 非法流转 / 方法不是 ground-truth / 缺环境事实 → 400，并把原因原样回给调用方。
		writeErr(w, 400, err.Error())
		return
	case err != nil:
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, st)
}

// replayFindingEvidence 凭证据重放一次，并把结果落进 finding_replays（可重放率的事实来源）。
// 它只记录事实，不改变验证状态：判定要人拿这次真实响应去调 POST /verification。
func (s *Server) replayFindingEvidence(w http.ResponseWriter, r *http.Request) {
	id, ok := parsePathID(w, r)
	if !ok {
		return
	}
	var in struct {
		BindingID string `json:"binding_id"`
		Payload   string `json:"payload_override"`
		URL       string `json:"url_override"`
		Headers   string `json:"header_override"`
	}
	if r.Body != nil {
		_ = json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&in) // 空 body 合法：按原证据重放
	}

	ft, err := s.m.pg.GetFindingTraffic(r.Context(), id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if ft == nil || len(ft.Bindings) == 0 {
		writeErr(w, 400, "该发现没有绑定任何流量证据，无法重放；先绑定原始请求再重放")
		return
	}
	binding, err := pickReplayBinding(ft.Bindings, strings.TrimSpace(in.BindingID))
	if err != nil {
		writeErr(w, 400, err.Error())
		return
	}

	out, err := s.evidenceStore().Replay(r.Context(), binding.Snapshot, evidence.ReplayOptions{
		PayloadOverride: in.Payload,
		URLOverride:     strings.TrimSpace(in.URL),
		HeaderOverride:  in.Headers,
	})
	if err != nil {
		// 连请求都构造不出来（URL 非法 / 证据文件缺失 / 请求体超限）：这是证据缺陷，不是目标行为。
		writeErr(w, 400, err.Error())
		return
	}

	rec, err := s.m.pg.RecordFindingReplay(r.Context(), db.ReplayRecordInput{
		FindingID:       id,
		BindingID:       &binding.ID,
		SnapshotID:      binding.SnapshotID,
		Replayed:        out.Replayed,
		MatchesOriginal: out.MatchesOriginal,
		StatusCode:      out.StatusCode,
		RespLen:         out.RespLenRead,
		RespSHA256:      out.RespSHA256,
		ErrText:         out.Err,
		ElapsedMS:       out.ElapsedMS,
	})
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}

	st, err := s.m.pg.GetFindingVerification(r.Context(), id)
	if err != nil && !errors.Is(err, db.ErrFindingNotFound) {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{
		"replay":       rec,
		"request":      map[string]any{"method": out.Method, "url": out.URL, "body_source": out.BodySource, "body_len": out.BodyLen, "headers": out.Headers},
		"response":     map[string]any{"status": out.StatusCode, "status_text": out.StatusText, "resp_len_read": out.RespLenRead, "resp_sha256": out.RespSHA256, "matches_original": out.MatchesOriginal, "content_type": out.ContentType, "preview": out.Preview},
		"original":     map[string]any{"status": binding.Snapshot.Status, "resp_len": binding.Snapshot.RespLen, "resp_sha256": binding.Snapshot.RespHash, "method": binding.Snapshot.Method, "url": binding.Snapshot.URL},
		"verification": st,
		"note": "replayed=true 只说明「凭证据把请求重建出来并拿到了目标响应」，它证明证据可回放，" +
			"不等于漏洞仍存在。判定仍要人拿这次真实响应去 POST /api/exploration/findings/{id}/verification。",
	})
}

// exportVerificationBlock 是导出闸门的返回体：把被拦的条目一条条列出来，
// 让调用方能直接去补判定，而不是只看到一句「导出失败」。
type exportVerificationBlock struct {
	Error    string                `json:"error"`
	Reason   string                `json:"reason"`
	Blocked  []blockedFindingBrief `json:"blocked"`
	Verified int                   `json:"verified"`
	Hint     string                `json:"hint"`
}

type blockedFindingBrief struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	VulnClass          string `json:"vulnclass"`
	VerificationStatus string `json:"verification_status"`
}

// rejectUnverifiedExport 是后端硬拦截。返回 true 表示已经写过响应、调用方必须立即返回。
func rejectUnverifiedExport(w http.ResponseWriter, verified []*db.DBFinding, blocked []*db.DBFinding) bool {
	if len(blocked) == 0 {
		return false
	}
	brief := make([]blockedFindingBrief, 0, len(blocked))
	for _, f := range blocked {
		name := f.Name
		if name == "" {
			name = f.VulnClass
		}
		brief = append(brief, blockedFindingBrief{
			ID: i64s(f.ID), Name: name, VulnClass: f.VulnClass,
			VerificationStatus: f.VerificationStatus,
		})
	}
	writeJSON(w, http.StatusConflict, exportVerificationBlock{
		Error:  "报告导出被验证闸门拦截",
		Reason: "只有 verification_status='verified' 的发现允许进报告；判定必须来自环境 ground-truth（重放实测 / 独立复现 / 目标真实响应），不接受 LLM 或 Agent 自报",
		Blocked: brief, Verified: len(verified),
		Hint: "对每条发现调 POST /api/exploration/findings/{id}/verification 标记 verified/refuted，" +
			"或用 POST /api/exploration/findings/{id}/replay 先做一次真实重放拿到环境事实；" +
			"被标 refuted 的条目会从导出范围里排除（同时计入误报率分母）。",
	})
	return true
}

// parsePathID 解析 {id} 路径参数；失败时已写 400。
func parsePathID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	raw := r.PathValue("id")
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		writeErr(w, 400, "finding id 无效: "+raw)
		return 0, false
	}
	return id, true
}
