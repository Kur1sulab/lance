package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Kur1sulab/lance/db"
)

// newVerificationServer 起一个真实 mux + 真实 PG 的测试实例（无 PG 就跳过）。
func newVerificationServer(t *testing.T) (*Server, http.Handler, func(method, path string, body any) (int, string, map[string]any)) {
	t.Helper()
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	t.Cleanup(func() { _ = m.Close() })
	td := t.TempDir()
	s := New(context.Background(), m, td, td, td)
	h := s.Handler()
	// 认证签名要用「当前口令哈希派生的 key」而不是裸 jwtKey：本机 DB 已设管理员口令，
	// 裸 jwtKey 签出来的 token 会被 verifyToken 拒掉（401）。
	hash, err := s.readPassHash()
	if err != nil {
		t.Fatalf("read pass hash: %v", err)
	}
	tok, err := signJWT(s.signingKey(hash))
	if err != nil {
		t.Fatalf("sign jwt: %v", err)
	}
	do := func(method, path string, body any) (int, string, map[string]any) {
		var r *http.Request
		if body != nil {
			b, _ := json.Marshal(body)
			r = httptest.NewRequest(method, path, bytes.NewReader(b))
		} else {
			r = httptest.NewRequest(method, path, nil)
		}
		r.Header.Set("Authorization", "Bearer "+tok)
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		var out map[string]any
		_ = json.Unmarshal(rec.Body.Bytes(), &out)
		return rec.Code, rec.Body.String(), out
	}
	return s, h, do
}

// markVerifiedForExport 让「只测导出格式」的老测试满足 6.2 闸门：
// 导出只放行 verified，格式测试不该因为闸门而失败，但也不能绕过它。
func markVerifiedForExport(t *testing.T, s *Server, findingID int64) {
	t.Helper()
	if _, err := s.m.pg.SetFindingVerification(context.Background(), db.VerificationInput{
		FindingID: findingID, Status: db.VerificationVerified, Method: db.VerifyMethodReplay,
		Actor: "test:fixture", GroundTruth: "fixture：按快照重放得到目标真实响应（测试数据）",
	}); err != nil {
		t.Fatalf("mark verified: %v", err)
	}
}

func addVerificationFinding(t *testing.T, s *Server) int64 {
	t.Helper()
	id, err := s.m.pg.AddFinding(0, 0, "sqli", "验证闸门 HTTP 测试", db.SeverityHigh,
		"export gate test", "id=1' OR '1'='1", "test", nil)
	if err != nil {
		t.Fatalf("add finding: %v", err)
	}
	t.Cleanup(func() { _, _ = s.m.pg.Exec(`DELETE FROM findings WHERE id=$1`, id) })
	return id
}

// TestFindingsExportGateBlocksUnverified 是闸门的主验收：未判定 → 409 且列出被拦条目；
// 标 verified 后 → 200 且报告头部带两个指标。
func TestFindingsExportGateBlocksUnverified(t *testing.T) {
	s, _, do := newVerificationServer(t)
	id := addVerificationFinding(t, s)
	path := "/api/exploration/findings/export?scope=selected&format=md-single&ids=" + i64s(id)

	code, body, out := do("GET", path, nil)
	if code != http.StatusConflict {
		t.Fatalf("未验证发现导出应被拦为 409，got %d: %s", code, body)
	}
	blocked, _ := out["blocked"].([]any)
	if len(blocked) != 1 {
		t.Fatalf("应列出 1 条被拦发现，got %v", out["blocked"])
	}
	first, _ := blocked[0].(map[string]any)
	if first["verification_status"] != db.VerificationUnverified {
		t.Errorf("被拦条目的状态应回显为 unverified，got %v", first["verification_status"])
	}
	if !strings.Contains(body, "验证闸门") {
		t.Errorf("409 响应应说明是验证闸门拦截: %s", body)
	}

	// csv/json 也必须被拦（闸门在所有导出格式之前，不是某一种格式的特例）。
	for _, format := range []string{"csv", "json", "md-zip"} {
		code, body, _ = do("GET", "/api/exploration/findings/export?scope=selected&format="+format+"&ids="+i64s(id), nil)
		if code != http.StatusConflict {
			t.Errorf("format=%s 也应被拦为 409，got %d: %s", format, code, vfirstLine(body))
		}
	}

	// 用环境事实判 verified。
	code, body, _ = do("POST", "/api/exploration/findings/"+i64s(id)+"/verification", map[string]string{
		"status":       db.VerificationVerified,
		"method":       db.VerifyMethodManualReplay,
		"actor":        "operator:test",
		"ground_truth": "curl -s 'http://target/api/users?id=2' → HTTP/1.1 200 OK，返回他人用户记录",
	})
	if code != 200 {
		t.Fatalf("标记 verified 失败 %d: %s", code, body)
	}

	// 列表/详情 DTO 必须带出验证状态：前端「未验证」角标靠这个字段，不能只靠导出报错。
	code, body, out = do("GET", "/api/exploration/findings/"+i64s(id), nil)
	if code != 200 {
		t.Fatalf("get finding %d: %s", code, body)
	}
	if out["verification_status"] != db.VerificationVerified {
		t.Errorf("详情 DTO 应回 verification_status=verified，got %v", out["verification_status"])
	}

	code, body, _ = do("GET", path, nil)
	if code != 200 {
		t.Fatalf("verified 后导出应放行，got %d: %s", code, body)
	}
	for _, want := range []string{"验证质量", "可重放率", "误报率", "环境 ground-truth"} {
		if !strings.Contains(body, want) {
			t.Errorf("报告头部缺 %q:\n%s", want, vfirstLines(body, 20))
		}
	}
	// 其他格式在 verified 后也应放行。
	if code, body, _ = do("GET", "/api/exploration/findings/export?scope=selected&format=csv&ids="+i64s(id), nil); code != 200 {
		t.Errorf("csv 应放行，got %d: %s", code, vfirstLine(body))
	} else if !strings.Contains(body, "验证状态") {
		t.Errorf("csv 应带验证状态列: %s", vfirstLine(body))
	}
}

// TestVerificationAPIRejectsLLMSelfReport 把「不接受 LLM 自报」这条红线钉在 HTTP 层。
func TestVerificationAPIRejectsLLMSelfReport(t *testing.T) {
	s, _, do := newVerificationServer(t)
	id := addVerificationFinding(t, s)
	base := "/api/exploration/findings/" + i64s(id) + "/verification"

	cases := []map[string]string{
		{"status": "verified", "method": "llm_reasoning", "actor": "agent:main", "ground_truth": "模型分析认为该参数存在 SQL 注入"},
		{"status": "verified", "method": "agent_self_report", "actor": "agent:main", "ground_truth": "子代理回报已验证"},
		{"status": "verified", "method": "replay", "actor": "operator:test", "ground_truth": ""},          // 缺环境事实
		{"status": "unverified", "method": "replay", "actor": "operator:test", "ground_truth": "想反悔"}, // 不允许直接退回
		{"status": "totally_made_up", "method": "replay", "actor": "op", "ground_truth": "x"},
	}
	for i, c := range cases {
		code, body, _ := do("POST", base, c)
		if code != 400 {
			t.Errorf("case %d 应 400，got %d: %s", i, code, vfirstLine(body))
		}
	}
	// 被拒的判定不能改状态。
	code, body, out := do("GET", base, nil)
	if code != 200 {
		t.Fatalf("读验证状态失败 %d: %s", code, body)
	}
	if out["status"] != db.VerificationUnverified {
		t.Errorf("被拒后应仍为 unverified，got %v", out["status"])
	}
	if out["replayable"] != false {
		t.Errorf("没有重放记录时 replayable 应为 false，got %v", out["replayable"])
	}
}

// TestReplayEndpointMakesFindingReplayable 走完 可重放率 的端到端链路：
// 绑定证据 → 重放真实请求 → 落流水 → 指标里算作可重放。
func TestReplayEndpointMakesFindingReplayable(t *testing.T) {
	s, _, do := newVerificationServer(t)
	id := addVerificationFinding(t, s)

	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(200)
		_, _ = w.Write([]byte(`{"users":[{"id":2,"token":"leaked"}]}`))
	}))
	defer target.Close()

	snapID := seedReplaySnapshot(t, s, id, target.URL+"/api/users?id=2", "GET",
		"GET /api/users?id=2 HTTP/1.1\r\nHost: 127.0.0.1\r\nAccept: */*\r\n", "", `{"users":[{"id":2,"token":"leaked"}]}`)
	if snapID == "" {
		t.Skip("seed snapshot failed")
	}

	before, err := s.m.pg.VerificationMetrics(context.Background(), db.VerificationMetricsFilter{})
	if err != nil {
		t.Fatalf("metrics before: %v", err)
	}

	code, body, out := do("POST", "/api/exploration/findings/"+i64s(id)+"/replay", map[string]string{})
	if code != 200 {
		t.Fatalf("重放失败 %d: %s", code, body)
	}
	rep, _ := out["replay"].(map[string]any)
	if rep["replayed"] != true {
		t.Fatalf("应记为重放成功: %s", body)
	}
	if st, _ := rep["status_code"].(float64); st != 200 {
		t.Errorf("重放应拿到目标真实状态码 200，got %v", rep["status_code"])
	}
	if !strings.Contains(body, "leaked") {
		t.Errorf("应回显目标真实响应预览: %s", vfirstLine(body))
	}

	// 重放本身不改验证状态——判定仍要人拿环境事实去做。
	code, body, out = do("GET", "/api/exploration/findings/"+i64s(id)+"/verification", nil)
	if code != 200 {
		t.Fatalf("读状态失败: %s", body)
	}
	if out["status"] != db.VerificationUnverified {
		t.Errorf("重放不应自动改验证状态，got %v", out["status"])
	}
	if out["replayable"] != true {
		t.Errorf("重放成功后可重放标记应为 true: %v", out["replayable"])
	}

	// 判 verified 后，指标里这条应同时进 verified 与 replayable。
	if code, body, _ = do("POST", "/api/exploration/findings/"+i64s(id)+"/verification", map[string]string{
		"status": db.VerificationVerified, "method": db.VerifyMethodReplay, "actor": "operator:test",
		"ground_truth": "POST /replay 拿到 200 + {\"users\":[...token...]}",
	}); code != 200 {
		t.Fatalf("verified 失败: %s", body)
	}

	after, err := s.m.pg.VerificationMetrics(context.Background(), db.VerificationMetricsFilter{})
	if err != nil {
		t.Fatalf("metrics after: %v", err)
	}
	if after.Verified-before.Verified != 1 || after.Replayable-before.Replayable != 1 {
		t.Errorf("指标应各 +1：verified %d→%d, replayable %d→%d",
			before.Verified, after.Verified, before.Replayable, after.Replayable)
	}
	if after.ReplayAttempts <= before.ReplayAttempts {
		t.Errorf("重放尝试数应增加：%d→%d", before.ReplayAttempts, after.ReplayAttempts)
	}

	// API 形态：GET /api/metrics/verification
	code, body, out = do("GET", "/api/metrics/verification", nil)
	if code != 200 {
		t.Fatalf("metrics API %d: %s", code, body)
	}
	t.Logf("GET /api/metrics/verification → %s", vfirstLine(body))
	m, _ := out["metrics"].(map[string]any)
	if m == nil {
		t.Fatalf("缺少 metrics 字段: %s", vfirstLine(body))
	}
	for _, k := range []string{"replayable_rate", "false_positive_rate", "replay_sample", "false_positive_sample", "verified", "refuted"} {
		if _, ok := m[k]; !ok {
			t.Errorf("metrics 缺字段 %s: %s", k, vfirstLine(body))
		}
	}
	if out["ground_truth_only"] != true || out["rejects_llm_self_report"] != true {
		t.Errorf("指标 API 必须声明只接受 ground-truth: %s", vfirstLine(body))
	}
}

func vfirstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}

func vfirstLines(s string, n int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > n {
		lines = lines[:n]
	}
	return strings.Join(lines, "\n")
}
