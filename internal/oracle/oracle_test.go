package oracle

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fastOpts 让测试跑得快：间隔压到 5ms，重复 3 次。
func fastOpts() Options {
	return Options{Timeout: 2 * time.Second, Interval: 5 * time.Millisecond, Repeats: 3, LockProbes: 3}
}

// ── 靶 1：存在直白的 oracle（文案差异 20+ 字节）────────────────────────────
func oracleTarget() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		u := r.FormValue("username")
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if u == "alice" {
			fmt.Fprint(w, "密码错误，请重试")
			return
		}
		fmt.Fprint(w, "该用户不存在，请检查后重试，或联系管理员")
	}))
}

// ── 靶 2：无 oracle（统一错误页，长度完全一致）──────────────────────────
func uniformTarget() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		fmt.Fprint(w, "用户名或密码错误")
	}))
}

// ── 靶 3：抖动大（每次响应长度都不同）──────────────────────────────────
func noisyTarget() *httptest.Server {
	var n int
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n++
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		// 长度 = 20 + n*7：σ 远超任何阈值，任何差异都不该被当成信号。
		fmt.Fprint(w, "err:"+strings.Repeat("x", 20+n*7))
	}))
}

// ── 靶 4：锁定回显（剩余次数递减）─────────────────────────────────────
func lockEchoTarget() *httptest.Server {
	left := 4
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		if r.FormValue("username") != "alice" {
			fmt.Fprint(w, "该用户不存在")
			return
		}
		left--
		if left <= 0 {
			fmt.Fprint(w, "账号已锁定，请 5 分钟后再试")
			return
		}
		fmt.Fprintf(w, "密码错误，你还可以尝试 %d 次", left)
	}))
}

func TestDetectsObviousOracle(t *testing.T) {
	srv := oracleTarget()
	defer srv.Close()

	r := New(fastOpts())
	rep, err := r.Run(context.Background(), Request{
		Probes: []Probe{{
			Name: "login", Method: "POST", URL: srv.URL + "/login",
			Body: "username={{account}}&pass=whatever", Encode: "form",
		}},
		Known: "alice", Absent: "zzz_nobody_9f3a",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !rep.Enumerable {
		t.Fatalf("应判为可枚举，实际 verdicts=%+v", rep.Verdicts)
	}
	v := rep.Verdicts[0]
	if v.State != StateExist {
		t.Errorf("State=%s，期望 exist（note=%s）", v.State, v.Note)
	}
	if v.Confidence < 0.6 {
		t.Errorf("置信度 %.2f 偏低，至少应有长度+文案两条信号", v.Confidence)
	}
	kinds := signalKinds(v.Signals)
	if !strings.Contains(kinds, "len_diff") {
		t.Errorf("缺 len_diff 信号，实得 %v", kinds)
	}
	t.Logf("信号=%v 置信度=%.2f", kinds, v.Confidence)
}

func TestNoOracleWhenResponsesIdentical(t *testing.T) {
	srv := uniformTarget()
	defer srv.Close()

	r := New(fastOpts())
	rep, err := r.Run(context.Background(), Request{
		Probes: []Probe{{Name: "login", Method: "POST", URL: srv.URL + "/login", Body: "u={{account}}"}},
		Known:  "alice", Absent: "zzz_nobody_9f3a",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if rep.Enumerable {
		t.Errorf("统一错误页不该判可枚举，实得 %+v", rep.Verdicts)
	}
	if got := rep.Verdicts[0].State; got != StateNoSignal {
		t.Errorf("State=%s，期望 no_signal", got)
	}
	if rep.Verdicts[0].Note == "" {
		t.Error("判不出时必须写原因，不许留空")
	}
}

func TestNoisyBaselineRefusesToJudge(t *testing.T) {
	srv := noisyTarget()
	defer srv.Close()

	r := New(fastOpts())
	rep, err := r.Run(context.Background(), Request{
		Probes: []Probe{{Name: "login", Method: "POST", URL: srv.URL + "/login", Body: "u={{account}}"}},
		Known:  "alice", Absent: "zzz_nobody_9f3a",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	v := rep.Verdicts[0]
	if rep.Enumerable {
		t.Errorf("抖动大于阈值的站点不许判可枚举，实得 %+v", v)
	}
	if v.State != StateNoSignal {
		t.Errorf("State=%s，期望 no_signal（抖动不可判）", v.State)
	}
	if !strings.Contains(v.Note, "基线不稳定") {
		t.Errorf("note 应说明基线不稳定，实得 %q", v.Note)
	}
}

func TestLockEchoDetected(t *testing.T) {
	srv := lockEchoTarget()
	defer srv.Close()

	r := New(fastOpts())
	rep, err := r.Run(context.Background(), Request{
		Probes: []Probe{{Name: "login", Method: "POST", URL: srv.URL + "/login", Body: "username={{account}}"}},
		Known:  "alice", Absent: "zzz_nobody_9f3a",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	v := rep.Verdicts[0]
	kinds := signalKinds(v.Signals)
	if !strings.Contains(kinds, "lock_echo") {
		t.Fatalf("未检出锁定回显，信号=%v note=%s", kinds, v.Note)
	}
	if v.State != StateLocked {
		t.Errorf("State=%s，期望 locked", v.State)
	}
	t.Logf("检出：%v", kinds)
}

// ── 单元级判据 ────────────────────────────────────────────────────

func TestRemainingAttempts(t *testing.T) {
	cases := []struct {
		body string
		n    int
		ok   bool
	}{
		{"密码错误，你还可以尝试 3 次", 3, true},
		{"Your account will be locked after 4 times failed attempts", 4, true},
		{"2 attempts remaining", 2, true},
		{"登录失败", 0, false},
		{"还可以尝试 99 次", 0, false}, // 超范围，当噪声丢弃
	}
	for _, c := range cases {
		n, ok := remainingAttempts(c.body)
		if ok != c.ok || (ok && n != c.n) {
			t.Errorf("%q → (%d,%v)，期望 (%d,%v)", c.body, n, ok, c.n, c.ok)
		}
	}
}

func TestStatsStableAndJitter(t *testing.T) {
	stable := []Observation{{Length: 100, Status: 200}, {Length: 100, Status: 200}, {Length: 100, Status: 200}}
	if s := stats(stable); !s.Stable || s.LenStd != 0 {
		t.Errorf("同输入同输出应判稳定，实得 %+v", s)
	}
	jittery := []Observation{{Length: 100, Status: 200}, {Length: 140, Status: 200}, {Length: 70, Status: 200}}
	if s := stats(jittery); s.Stable {
		t.Errorf("长度抖动大不该判稳定，实得 std=%.1f", s.LenStd)
	}
	// 网络失败的观测不进统计
	withErr := []Observation{{Length: 100, Status: 200}, {Err: "timeout"}, {Length: 100, Status: 200}}
	if s := stats(withErr); s.N != 2 {
		t.Errorf("失败观测应被排除，N=%d", s.N)
	}
}

func TestSubstituteEncoding(t *testing.T) {
	if got := substitute(`{"u":"{{account}}"}`, `a"b`, "json"); !strings.Contains(got, `a\"b`) {
		t.Errorf("json 模式未转义：%s", got)
	}
	if got := substitute("u={{account}}", "a b&c", "form"); !strings.Contains(got, "a+b%26c") {
		t.Errorf("form 模式未 urlencode：%s", got)
	}
	if got := substitute("u={{account}}", "x", ""); got != "u=x" {
		t.Errorf("默认 raw 模式出错：%s", got)
	}
	if got := substitute("no-placeholder", "x", ""); got != "no-placeholder" {
		t.Errorf("无占位符时应原样返回：%s", got)
	}
}

func TestTextFragmentIgnoresTinyDiff(t *testing.T) {
	if _, ok := textFragment("abcd", "abce"); ok {
		t.Error("单字节差异不算文案信号")
	}
	if frag, ok := textFragment(
		"<div>该用户不存在，请检查后重试</div>",
		"<div>密码错误，请重试</div>"); !ok || frag == "" {
		t.Error("明显文案差异应产出片段")
	}
}

func TestRunRequiresBothAccounts(t *testing.T) {
	r := New(fastOpts())
	if _, err := r.Run(context.Background(), Request{Probes: []Probe{{Name: "x", URL: "http://127.0.0.1:1/"}}}); err == nil {
		t.Error("缺对照组必须报错")
	}
	if _, err := r.Run(context.Background(), Request{Known: "a", Absent: "b"}); err == nil {
		t.Error("缺探测点必须报错")
	}
}

func signalKinds(sigs []Signal) string {
	var out []string
	for _, s := range sigs {
		out = append(out, s.Kind)
	}
	return strings.Join(out, ",")
}
