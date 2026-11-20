package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lance/internal/action"
	"lance/internal/brain"
)

// fakeBrain 让测试不依赖网络与真实模型：想让它出什么决策就出什么。
type fakeBrain struct {
	name string
	acts []action.Action
	err  error
}

func (f fakeBrain) Name() string { return f.name }
func (f fakeBrain) Decide(context.Context, brain.Input) ([]action.Action, error) {
	return f.acts, f.err
}

func writeFindings(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "findings.json")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return path
}

// clearLLMEnv 清空全部三代 LLM 环境变量并在用例结束后恢复。
// EnvLLM 认 LANCE_/LAUGHING_/ORCH_ 三套名字，漏清任何一套都会让
// fallback 用例在配了模型的机器上假通过。
func clearLLMEnv(t *testing.T) {
	t.Helper()
	keys := []string{
		"LANCE_LLM_BASE", "LANCE_LLM_KEY", "LANCE_LLM_MODEL",
		"LAUGHING_LLM_BASE", "LAUGHING_LLM_KEY", "LAUGHING_LLM_MODEL",
		"ORCH_LLM_BASE", "ORCH_LLM_KEY", "ORCH_LLM_MODEL",
	}
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

// 签名必须和账本落盘形态逐字一致，否则 bench 的集合与历史对不上。
// 期望值取自 internal/exec Runner.sig 的四个分支。
func TestSignatureMatchesLedger(t *testing.T) {
	cases := []struct {
		a    action.Action
		want string
	}{
		{action.Action{Kind: action.KindReplay, Args: map[string]string{"id": "L-1"}}, "replay:L-1"},
		{action.Action{Kind: action.KindRunTpl, Args: map[string]string{"target": "10.0.0.9:8080", "tpl": "tpl/web"}}, "run_tpl:10.0.0.9:8080:tpl/web"},
		{action.Action{Kind: action.KindRerun, Args: map[string]string{"intensity": "high"}}, "rerun::high"},
		{action.Action{Kind: action.KindRerun, Args: map[string]string{"targets": "10.0.0.1,10.0.0.2", "intensity": "low"}}, "rerun:10.0.0.1,10.0.0.2:low"},
		{action.Action{Kind: action.KindStop}, "stop"},
	}
	for _, c := range cases {
		if got := Signature(c.a); got != c.want {
			t.Errorf("Signature(%+v) = %q，期望 %q", c.a, got, c.want)
		}
	}
}

func TestCompareIntersectDiffOverlap(t *testing.T) {
	a := []string{"replay:L-1", "replay:L-2", "rerun::high"}
	b := []string{"replay:L-2", "run_tpl:10.0.0.9:tpl/x", "replay:L-1"}
	d := Compare(a, b)
	if want := []string{"replay:L-1", "replay:L-2"}; !reflect.DeepEqual(d.Intersect, want) {
		t.Errorf("交集 = %v，期望 %v", d.Intersect, want)
	}
	if want := []string{"rerun::high"}; !reflect.DeepEqual(d.OnlyA, want) {
		t.Errorf("仅 A = %v，期望 %v", d.OnlyA, want)
	}
	if want := []string{"run_tpl:10.0.0.9:tpl/x"}; !reflect.DeepEqual(d.OnlyB, want) {
		t.Errorf("仅 B = %v，期望 %v", d.OnlyB, want)
	}
	if d.Union != 4 || d.Overlap != 0.5 {
		t.Errorf("并集/重合率 = %d/%v，期望 4/0.5", d.Union, d.Overlap)
	}
}

func TestCompareOverlapRounding(t *testing.T) {
	// 1/3 必须落 0.33 而不是 0.33333：产物字段约定保留两位小数。
	d := Compare([]string{"x", "y"}, []string{"x", "z"})
	if d.Overlap != 0.33 {
		t.Errorf("重合率 = %v，期望 0.33", d.Overlap)
	}
}

func TestCompareEdgeCases(t *testing.T) {
	// 完全相同 → 1；完全不相交 → 0；两边都空 → 0 且不能是 NaN。
	if d := Compare([]string{"x"}, []string{"x"}); d.Overlap != 1 {
		t.Errorf("相同集合重合率 = %v，期望 1", d.Overlap)
	}
	if d := Compare([]string{"x"}, []string{"y"}); d.Overlap != 0 || d.Union != 2 {
		t.Errorf("不相交集合 = %+v，期望重合率 0 并集 2", d)
	}
	if d := Compare(nil, nil); d.Overlap != 0 || d.Union != 0 {
		t.Errorf("空集合 = %+v，期望重合率 0 并集 0", d)
	}
	// 重复签名按集合去重，重合率不被灌水。
	if d := Compare([]string{"x", "x"}, []string{"x"}); d.Union != 1 || d.Overlap != 1 {
		t.Errorf("去重失败 = %+v，期望并集 1 重合率 1", d)
	}
}

func TestCompareOrderIndependent(t *testing.T) {
	a := []string{"replay:L-2", "rerun::high", "replay:L-1"}
	b := []string{"run_tpl:t:x", "replay:L-1"}
	if d1, d2 := Compare(a, b), Compare([]string{"replay:L-1", "replay:L-2", "rerun::high"}, []string{"replay:L-1", "run_tpl:t:x"}); !reflect.DeepEqual(d1, d2) {
		t.Errorf("输入顺序不该影响 diff：%+v vs %+v", d1, d2)
	}
}

func TestParseBrains(t *testing.T) {
	got, err := ParseBrains(" llm , rules ,llm,")
	if err != nil || !reflect.DeepEqual(got, []string{"llm", "rules"}) {
		t.Errorf("去重去空白失败: %v %v", got, err)
	}
	if _, err := ParseBrains("rules,gpt"); err == nil {
		t.Error("未知档位必须报错，不能静默跳过")
	}
	for _, empty := range []string{"", " , ,"} {
		if _, err := ParseBrains(empty); err == nil {
			t.Errorf("空清单 %q 必须报错", empty)
		}
	}
}

// 核心回落分支：llm 未配置时 bench 必须正常出报告，而不是报错退出。
func TestRunLLMUnavailableFallback(t *testing.T) {
	clearLLMEnv(t)
	path := writeFindings(t, `{"findings":[{"id":"L-1","check":"redis-unauth","severity":"high","level":"L2","confidence":"0.9","target":"10.0.0.1:6379"}],"coverage":[{"item":"web-check","state":"skip","reason":"上一轮超时"}]}`)
	rep, err := Run(context.Background(), Options{Findings: path, Budget: 8, Brains: []string{"rules", "llm"}})
	if err != nil {
		t.Fatalf("llm 未配置不该让 bench 报错退出: %v", err)
	}
	if len(rep.Brains) != 2 {
		t.Fatalf("两档都该有记录，实际 %d", len(rep.Brains))
	}
	rules, llm := rep.Brains[0], rep.Brains[1]
	// 覆盖表必须原样喂进 brain：高危复验 + skip 补测各一条。
	if rules.Status != StatusOK || rules.Count != 2 ||
		!reflect.DeepEqual(rules.Signatures, []string{"replay:L-1", "rerun::high"}) {
		t.Fatalf("rules 档应跑出 replay:L-1 与 rerun::high，实际 %+v", rules)
	}
	if llm.Status != StatusUnavailable || llm.Detail == "" {
		t.Fatalf("llm 档应标记 unavailable 并带原因，实际 %+v", llm)
	}
	if rep.Diff != nil {
		t.Fatalf("llm 没跑就不该算重合率（会得出 0%% 的假结论），实际 %+v", rep.Diff)
	}
	if !strings.Contains(rep.DiffNote, "llm=unavailable") {
		t.Fatalf("diff_note 应说明原因，实际 %q", rep.DiffNote)
	}
}

// 两档都 ok 时的完整链路：kind 分布、签名去重排序、diff、耗时。
func TestRunDiffBetweenBrains(t *testing.T) {
	path := writeFindings(t, `{"findings":[{"id":"L-1","severity":"high","target":"10.0.0.1"}]}`)
	ra := fakeBrain{name: "rules", acts: []action.Action{
		{Kind: action.KindReplay, Args: map[string]string{"id": "L-1"}, Why: "高危未复验", Conf: 0.9},
		{Kind: action.KindRerun, Args: map[string]string{"intensity": "high"}, Why: "覆盖空档", Conf: 0.6},
	}}
	rb := fakeBrain{name: "llm:test-model", acts: []action.Action{
		{Kind: action.KindReplay, Args: map[string]string{"id": "L-1"}, Why: "模型也要复验", Conf: 0.8},
		{Kind: action.KindRunTpl, Args: map[string]string{"target": "10.0.0.9:80", "tpl": "tpl/web"}, Why: "模型想打新目标", Conf: 0.7},
	}}
	resolve := func(name string) (brain.Brain, bool) {
		switch name {
		case "rules":
			return ra, true
		case "llm":
			return rb, true
		}
		return nil, false
	}
	rep, err := Run(context.Background(), Options{Findings: path, Budget: 8, Brains: []string{"rules", "llm"}, Resolve: resolve})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Brains {
		if r.Status != StatusOK || r.Count != 2 || r.DurationMS < 0 {
			t.Fatalf("档位结果不完整: %+v", r)
		}
	}
	if rep.Brains[1].Name != "llm:test-model" {
		t.Errorf("llm 档应记录 brain.Name() 便于追溯，实际 %q", rep.Brains[1].Name)
	}
	if k := rep.Brains[0].Kinds; k["replay"] != 1 || k["rerun"] != 1 {
		t.Errorf("rules kind 分布不对: %v", k)
	}
	if k := rep.Brains[1].Kinds; k["replay"] != 1 || k["run_tpl"] != 1 {
		t.Errorf("llm kind 分布不对: %v", k)
	}
	d := rep.Diff
	if d == nil {
		t.Fatal("两档都 ok 必须算 diff")
	}
	if d.A != "rules" || d.B != "llm" {
		t.Errorf("diff 档位名不对: %s vs %s", d.A, d.B)
	}
	if want := []string{"replay:L-1"}; !reflect.DeepEqual(d.Intersect, want) {
		t.Errorf("交集 = %v，期望 %v", d.Intersect, want)
	}
	if want := []string{"rerun::high"}; !reflect.DeepEqual(d.OnlyA, want) {
		t.Errorf("仅 rules = %v，期望 %v", d.OnlyA, want)
	}
	if want := []string{"run_tpl:10.0.0.9:80:tpl/web"}; !reflect.DeepEqual(d.OnlyB, want) {
		t.Errorf("仅 llm = %v，期望 %v", d.OnlyB, want)
	}
	if d.Union != 3 || d.Overlap != 0.33 {
		t.Errorf("并集/重合率 = %d/%v，期望 3/0.33", d.Union, d.Overlap)
	}
}

// 一档 Decide 报错（如网关 502）只记进该档，另一档照常出结果。
func TestRunBrainErrorDoesNotKillReport(t *testing.T) {
	path := writeFindings(t, `{"findings":[{"id":"L-1","severity":"high","target":"10.0.0.1"}]}`)
	resolve := func(name string) (brain.Brain, bool) {
		if name == "rules" {
			return brain.Rules{}, true
		}
		return fakeBrain{name: "llm:x", err: errors.New("llm 返回 HTTP 502")}, true
	}
	rep, err := Run(context.Background(), Options{Findings: path, Budget: 8, Brains: []string{"rules", "llm"}, Resolve: resolve})
	if err != nil {
		t.Fatalf("单档失败不该中断整份报告: %v", err)
	}
	if rep.Brains[0].Status != StatusOK {
		t.Fatalf("rules 档应不受影响: %+v", rep.Brains[0])
	}
	llm := rep.Brains[1]
	if llm.Status != StatusError || !strings.Contains(llm.Detail, "502") {
		t.Fatalf("llm 档应记 error 与原因: %+v", llm)
	}
	if rep.Diff != nil || !strings.Contains(rep.DiffNote, "llm=error") {
		t.Fatalf("diff 应为 null 且注明原因: %+v / %q", rep.Diff, rep.DiffNote)
	}
}

// 走真实 LLM 脑 + DefaultResolve 的端到端链路：用本地 httptest 桩代替
// 网关（仅回环、用例结束即关），证明配置了 LANCE_LLM_* 时 llm 档能真正
// 跑出决策并参与 diff，而不是只测了假 brain 注入。
func TestRunLLMConfiguredAgainstLocalStub(t *testing.T) {
	clearLLMEnv(t)
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/chat/completions" {
			t.Errorf("请求路径不对: %s", r.URL.Path)
		}
		content := `{"actions":[{"kind":"replay","args":{"id":"L-1"},"why":"复验高危","conf":0.9},{"kind":"run_tpl","args":{"target":"10.0.0.9:80","tpl":"tpl/web"},"why":"补新目标","conf":0.7}]}`
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"choices":[{"message":{"content":%q}}]}`, content)
	}))
	t.Cleanup(srv.Close)
	t.Setenv("LANCE_LLM_BASE", srv.URL)
	t.Setenv("LANCE_LLM_MODEL", "stub-model")
	t.Setenv("LANCE_LLM_KEY", "stub-key")

	path := writeFindings(t, `{"findings":[{"id":"L-1","check":"redis-unauth","severity":"high","target":"10.0.0.1:6379"}]}`)
	rep, err := Run(context.Background(), Options{Findings: path, Budget: 8, Brains: []string{"rules", "llm"}})
	if err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Errorf("合法 JSON 一次就该过，实际请求 %d 次", calls)
	}
	llm := rep.Brains[1]
	if llm.Status != StatusOK || llm.Name != "llm:stub-model" || llm.Count != 2 {
		t.Fatalf("llm 档应真正跑出 2 条决策: %+v", llm)
	}
	if rep.Diff == nil || rep.Diff.Overlap != 0.5 || rep.Diff.Union != 2 {
		t.Fatalf("diff 应为交集 1 并集 2 重合率 0.5: %+v", rep.Diff)
	}
}

func TestRunRejectsBadOptions(t *testing.T) {
	path := writeFindings(t, `{"findings":[]}`)
	if _, err := Run(context.Background(), Options{Budget: 8, Brains: []string{"rules"}}); err == nil {
		t.Error("缺 findings 路径必须报错")
	}
	if _, err := Run(context.Background(), Options{Findings: path, Budget: 0, Brains: []string{"rules"}}); err == nil {
		t.Error("budget<=0 必须报错，不能悄悄用默认值")
	}
	if _, err := Run(context.Background(), Options{Findings: path, Budget: 8}); err == nil {
		t.Error("空档位清单必须报错")
	}
}

// 产物要能落盘、能读回、字段不丢；摘要要含人读的关键数字。
func TestWriteAndSummary(t *testing.T) {
	path := writeFindings(t, `{"findings":[{"id":"L-1","severity":"high","target":"10.0.0.1"}]}`)
	resolve := func(name string) (brain.Brain, bool) {
		if name == "rules" {
			return fakeBrain{name: "rules", acts: []action.Action{
				{Kind: action.KindReplay, Args: map[string]string{"id": "L-1"}, Why: "x", Conf: 0.9},
			}}, true
		}
		return nil, false
	}
	rep, err := Run(context.Background(), Options{Findings: path, Budget: 8, Brains: []string{"rules", "llm"}, Resolve: resolve})
	if err != nil {
		t.Fatal(err)
	}
	out := t.TempDir()
	file, err := Write(out, rep)
	if err != nil {
		t.Fatal(err)
	}
	base := filepath.Base(file)
	if !strings.HasPrefix(base, "bench-") || !strings.HasSuffix(base, ".json") {
		t.Errorf("产物文件名不符合 bench-<时间戳>.json: %s", base)
	}
	b, err := os.ReadFile(file)
	if err != nil {
		t.Fatal(err)
	}
	var back Report
	if err := json.Unmarshal(b, &back); err != nil {
		t.Fatalf("产物必须能读回: %v", err)
	}
	if back.Findings != path || back.Budget != 8 || len(back.Brains) != 2 || back.Brains[1].Status != StatusUnavailable {
		t.Errorf("读回的字段丢了: %+v", back)
	}
	sum := Summary(rep)
	for _, want := range []string{"rules", "llm", "unavailable", "replay=1", "对比不可用"} {
		if !strings.Contains(sum, want) {
			t.Errorf("摘要缺少 %q:\n%s", want, sum)
		}
	}
}
