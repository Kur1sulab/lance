package db

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"testing"
	"time"
)

// openTestDB 复用 package 级 TestMain 的连接约定：拿不到 PG 就跳过。
func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres 不可用 (%v) — 跳过", err)
	}
	t.Cleanup(func() { d.Close() })
	return d
}

// newVerificationFixture 造一条真实 finding 行并登记清理。
func newVerificationFixture(t *testing.T, d *DB) int64 {
	t.Helper()
	id, err := d.AddFinding(0, 0, "sqli", "验证闸门测试 SQLi", SeverityHigh,
		"测试用发现", "id=1' OR '1'='1", "test", nil)
	if err != nil {
		t.Fatalf("add finding: %v", err)
	}
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM findings WHERE id=$1`, id) })
	return id
}

// TestVerificationStateMachine 覆盖状态机的合法性/非法性，以及模型自报被硬拒。
func TestVerificationStateMachine(t *testing.T) {
	legal := [][2]string{
		{VerificationUnverified, VerificationVerified},
		{VerificationUnverified, VerificationRefuted},
		{VerificationUnverified, VerificationUnreproducible},
		{VerificationVerified, VerificationRefuted},
		{VerificationVerified, VerificationUnreproducible},
		{VerificationRefuted, VerificationVerified},
		{VerificationUnreproducible, VerificationVerified},
		{VerificationUnreproducible, VerificationRefuted},
		{VerificationVerified, VerificationVerified}, // 重申判定
		{VerificationRefuted, VerificationRefuted},  // 重申误报（补了新证据后重判）
	}
	for _, p := range legal {
		if !CanTransitionVerification(p[0], p[1]) {
			t.Errorf("%s → %s 应合法", p[0], p[1])
		}
	}
	illegal := [][2]string{
		{VerificationVerified, VerificationUnverified}, // 撤销必须走 refuted
		{VerificationRefuted, VerificationUnverified},
		{VerificationUnreproducible, VerificationUnverified},
		{VerificationUnverified, VerificationUnverified},     // 不能「重申未判定」
		{VerificationUnverified, "bogus"},
		{VerificationUnreproducible, VerificationUnreproducible}, // 不可复现没有「重申」语义
	}
	for _, p := range illegal {
		if CanTransitionVerification(p[0], p[1]) {
			t.Errorf("%s → %s 应被拒", p[0], p[1])
		}
	}

	// 判定方法白名单：只有环境事实。LLM/Agent 自报一律拒绝。
	if !IsGroundTruthMethod(VerifyMethodReplay) || !IsGroundTruthMethod(VerifyMethodManualReplay) ||
		!IsGroundTruthMethod(VerifyMethodTargetResponse) {
		t.Error("环境事实方法应在白名单内")
	}
	for _, bad := range []string{"", "llm", "LLM_reasoning", "gpt-4o", "claude", "agent_self_check", "self_report", "model", "ai_verdict"} {
		if IsGroundTruthMethod(bad) {
			t.Errorf("方法 %q 不该被接受：模型自报不是判定依据", bad)
		}
	}
}

// TestVerificationMetricsMath 只测算法本身（纯函数），覆盖两个分母口径与除零。
func TestVerificationMetricsMath(t *testing.T) {
	cases := []struct {
		name       string
		in         VerificationMetrics
		wantReplay float64
		wantFP     float64
		wantRSamp  int
		wantFSamp  int
	}{
		{
			name:       "典型：3 verified 其中 2 可重放，1 refuted",
			in:         VerificationMetrics{Verified: 3, Replayable: 2, Refuted: 1},
			wantReplay: 2.0 / 3.0, wantFP: 1.0 / 4.0, wantRSamp: 3, wantFSamp: 4,
		},
		{
			name:       "全绿：无 refuted",
			in:         VerificationMetrics{Verified: 5, Replayable: 5},
			wantReplay: 1, wantFP: 0, wantRSamp: 5, wantFSamp: 5,
		},
		{
			name:       "全误报：verified 0",
			in:         VerificationMetrics{Verified: 0, Replayable: 0, Refuted: 4},
			wantReplay: 0, wantFP: 1, wantRSamp: 0, wantFSamp: 4,
		},
		{
			name:       "空库：两个分母都为 0，不得 NaN/panic",
			in:         VerificationMetrics{},
			wantReplay: 0, wantFP: 0, wantRSamp: 0, wantFSamp: 0,
		},
		{
			name:       "unreproducible/unverified 不进误报分母",
			in:         VerificationMetrics{Verified: 2, Replayable: 1, Refuted: 1, Unreproducible: 5, Unverified: 9},
			wantReplay: 0.5, wantFP: 1.0 / 3.0, wantRSamp: 2, wantFSamp: 3,
		},
	}
	for _, c := range cases {
		got := ComputeVerificationMetrics(c.in)
		if !almost(got.ReplayableRate, c.wantReplay) {
			t.Errorf("%s: 可重放率 = %v, want %v", c.name, got.ReplayableRate, c.wantReplay)
		}
		if !almost(got.FalsePositiveRate, c.wantFP) {
			t.Errorf("%s: 误报率 = %v, want %v", c.name, got.FalsePositiveRate, c.wantFP)
		}
		if got.ReplaySample != c.wantRSamp {
			t.Errorf("%s: 可重放样本 = %d, want %d", c.name, got.ReplaySample, c.wantRSamp)
		}
		if got.FalsePositiveSample != c.wantFSamp {
			t.Errorf("%s: 误报样本 = %d, want %d", c.name, got.FalsePositiveSample, c.wantFSamp)
		}
		if !got.GroundTruthOnly || got.Rule == "" {
			t.Errorf("%s: 必须标明 ground_truth_only 与口径", c.name)
		}
	}
}

func almost(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-4
}

// TestSetFindingVerificationRejects 覆盖 API 层的四类拒绝：未知状态、回退到 unverified、
// 模型自报方法、缺环境事实。
func TestSetFindingVerificationRejects(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	id := newVerificationFixture(t, d)

	bad := []VerificationInput{
		{FindingID: id, Status: "maybe", Method: VerifyMethodReplay, Actor: "op", GroundTruth: "x"},
		{FindingID: id, Status: VerificationUnverified, Method: VerifyMethodReplay, Actor: "op", GroundTruth: "x"},
		// 这条是 6.2 的核心红线：LLM 自报不算证据。
		{FindingID: id, Status: VerificationVerified, Method: "llm_reasoning", Actor: "gpt", GroundTruth: "模型认为存在 SQL 注入"},
		{FindingID: id, Status: VerificationVerified, Method: VerifyMethodReplay, Actor: "op", GroundTruth: ""},
		{FindingID: id, Status: VerificationVerified, Method: VerifyMethodReplay, Actor: "", GroundTruth: "HTTP/1.1 200 OK"},
	}
	for i, in := range bad {
		if _, err := d.SetFindingVerification(ctx, in); !IsVerificationInvalid(err) {
			t.Errorf("case %d 应被判为非法流转，got err=%v", i, err)
		}
	}

	// 非法输入不该留下任何流水。
	var n int
	if err := d.QueryRow(`SELECT count(*) FROM finding_verifications WHERE finding_id=$1`, id).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("被拒的判定不应落流水，got %d 条", n)
	}
	st, err := d.GetFindingVerification(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != VerificationUnverified {
		t.Errorf("被拒后状态应保持 unverified，got %s", st.Status)
	}
}

// TestSetFindingVerificationFlow 走通 verified → refuted → verified 的完整流转，
// 并检查流水、allowed_next 与 verified_at。
func TestSetFindingVerificationFlow(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	id := newVerificationFixture(t, d)

	st, err := d.SetFindingVerification(ctx, VerificationInput{
		FindingID: id, Status: VerificationVerified, Method: VerifyMethodReplay,
		Actor: "operator:test", GroundTruth: "POST /login → HTTP/1.1 200 OK, body 含 admin 会话 token",
		Note: "按证据重放复现",
	})
	if err != nil {
		t.Fatalf("verified: %v", err)
	}
	if st.Status != VerificationVerified || st.VerifiedAt == nil {
		t.Fatalf("状态/时间未写入: %+v", st)
	}
	if len(st.History) != 1 || st.History[0].FromStatus != VerificationUnverified || st.History[0].ToStatus != VerificationVerified {
		t.Fatalf("流水不对: %+v", st.History)
	}
	for _, want := range []string{VerificationRefuted, VerificationUnreproducible} {
		found := false
		for _, a := range st.AllowedNext {
			if a == want {
				found = true
			}
		}
		if !found {
			t.Errorf("allowed_next 缺 %s: %v", want, st.AllowedNext)
		}
	}
	for _, bad := range []string{VerificationUnverified} {
		for _, a := range st.AllowedNext {
			if a == bad {
				t.Errorf("allowed_next 不该包含 %s", bad)
			}
		}
	}

	// verified → refuted：复核推翻。
	st, err = d.SetFindingVerification(ctx, VerificationInput{
		FindingID: id, Status: VerificationRefuted, Method: VerifyMethodManualReplay,
		Actor: "operator:test2", GroundTruth: "curl 独立复现返回 403 且无注入点，原证据为网关错误页",
	})
	if err != nil {
		t.Fatalf("refuted: %v", err)
	}
	if st.Status != VerificationRefuted || st.VerifiedAt != nil {
		t.Fatalf("refuted 后 verified_at 应清空: %+v", st)
	}
	if len(st.History) != 2 {
		t.Fatalf("流水应为 2 条, got %d", len(st.History))
	}

	// refuted → verified：纠错需带新证据。
	if _, err = d.SetFindingVerification(ctx, VerificationInput{
		FindingID: id, Status: VerificationVerified, Method: VerifyMethodTargetResponse,
		Actor: "operator:test3", GroundTruth: "复测返回 200 且泄露 12 条用户记录",
	}); err != nil {
		t.Fatalf("refuted→verified: %v", err)
	}

	// 指标必须反映这条发现：verified=1，可重放 0（没有成功重放记录），误报样本 1。
	m, err := d.VerificationMetrics(ctx, VerificationMetricsFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if m.Verified < 1 || m.GroundTruthOnly == false || m.Rule == "" {
		t.Fatalf("指标异常: %+v", m)
	}
}

// TestVerificationInvalidatedByEvidenceChange 证明「证据一变，原判定失效」这条闸门内建行为。
func TestVerificationInvalidatedByEvidenceChange(t *testing.T) {
	d := openTestDB(t)
	ctx := context.Background()
	id := newVerificationFixture(t, d)

	snap := TrafficEvidenceSnapshot{
		SourceTrafficID: "verify-test-" + time.Now().Format("150405.000000000"),
		CapturedAt:      time.Now().UnixMilli(), URL: "http://127.0.0.1:1/x", Method: "GET",
		Status: 200, ContentType: "text/plain", ReqHead: "GET /x HTTP/1.1\r\nHost: 127.0.0.1\r\n",
		RespHead: "HTTP/1.1 200 OK\r\n", ReqHash: hashOf(""), RespHash: hashOf("ok"),
		ReqLen: 0, RespLen: 2,
	}
	snap.ID = TrafficSnapshotID(snap)
	bind := func(s TrafficEvidenceSnapshot, role string) {
		t.Helper()
		if err := d.WithEvidenceTx(context.Background(), func(tx *sql.Tx) error {
			if err := LockFindingEvidenceTx(tx, id, nil); err != nil {
				return err
			}
			return AddFindingTrafficTx(tx, id, []PreparedTrafficEvidence{{Ref: TrafficRef{Role: role}, Snapshot: s}})
		}); err != nil {
			t.Fatalf("bind 证据: %v", err)
		}
	}
	bind(snap, "request")
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM traffic_evidence_snapshots WHERE id=$1`, snap.ID) })

	if _, err := d.SetFindingVerification(ctx, VerificationInput{
		FindingID: id, Status: VerificationVerified, Method: VerifyMethodReplay,
		Actor: "operator:test", GroundTruth: "GET /x → 200 ok",
	}); err != nil {
		t.Fatalf("verified: %v", err)
	}

	// 再绑一次证据（模拟证据更新）→ 判定必须失效。
	snap2 := snap
	snap2.CapturedAt++
	snap2.SourceTrafficID += "-b"
	snap2.ID = TrafficSnapshotID(snap2)
	bind(snap2, "supporting")
	t.Cleanup(func() { _, _ = d.Exec(`DELETE FROM traffic_evidence_snapshots WHERE id=$1`, snap2.ID) })

	st, err := d.GetFindingVerification(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if st.Status != VerificationUnverified {
		t.Fatalf("证据变更后应退回 unverified，got %s", st.Status)
	}
	if len(st.History) < 2 || st.History[0].Method != VerifyMethodEvidenceChanged {
		t.Fatalf("应留一条 evidence_changed 流水: %+v", st.History)
	}
}

// TestSplitByVerification 是导出闸门的纯逻辑：只有 verified 进白名单。
func TestSplitByVerification(t *testing.T) {
	fs := []*DBFinding{
		{ID: 1, VerificationStatus: VerificationVerified},
		{ID: 2, VerificationStatus: VerificationUnverified},
		{ID: 3, VerificationStatus: VerificationRefuted},
		{ID: 4, VerificationStatus: ""},
		{ID: 5, VerificationStatus: VerificationUnreproducible},
	}
	verified, blocked := SplitByVerification(fs)
	if len(verified) != 1 || verified[0].ID != 1 {
		t.Fatalf("verified = %v", ids(verified))
	}
	if len(blocked) != 4 {
		t.Fatalf("blocked = %v", ids(blocked))
	}
}

func ids(fs []*DBFinding) []int64 {
	out := make([]int64, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.ID)
	}
	return out
}

func hashOf(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}
