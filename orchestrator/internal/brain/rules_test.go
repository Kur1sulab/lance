package brain

import (
	"context"
	"testing"

	"github.com/Kur1sulab/lance/orchestrator/internal/action"
)

// rerun 的两条规则要守住：一轮只派一条（整批重扫，多派等于重复扫同一批目标），
// 以及签名和执行器对齐（否则账本历史认不出已跑过，每轮都重派）。
func TestRulesRerunOncePerRound(t *testing.T) {
	in := Input{
		Coverage: []Coverage{
			{Item: "web-check", State: "skip", Reason: "上一轮超时"},
			{Item: "auth-probe", State: "skip", Reason: "目标无响应"},
		},
		Budget: 8,
	}
	acts, err := Rules{}.Decide(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 {
		t.Fatalf("两个 skip 项也只该派 1 条 rerun，实际 %d 条: %+v", len(acts), acts)
	}
	a := acts[0]
	if a.Kind != action.KindRerun {
		t.Fatalf("kind=%s，期望 rerun", a.Kind)
	}
	if a.Args["intensity"] != "high" {
		t.Fatalf("intensity=%q，期望 high", a.Args["intensity"])
	}
	if _, ok := a.Args["targets"]; ok {
		t.Fatal("规则脑不该自己编目标，targets 必须留给执行器从 findings 取")
	}
	if a.Why == "" || a.Conf != 0.6 {
		t.Fatalf("why/conf 不完整: %+v", a)
	}
}

func TestRulesRerunSkippedAfterHistory(t *testing.T) {
	in := Input{
		Coverage: []Coverage{{Item: "web-check", State: "skip", Reason: "上一轮超时"}},
		History:  []string{"rerun::high"}, // 执行器落账的签名形态
		Budget:   8,
	}
	acts, err := Rules{}.Decide(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) != 1 || acts[0].Kind != action.KindStop {
		t.Fatalf("已重跑过就该收工，实际 %+v", acts)
	}
}

func TestRulesHighSeverityFirst(t *testing.T) {
	in := Input{
		Findings: []Finding{{ID: "L-1", Severity: "high", Check: "redis-unauth", Target: "10.0.0.1"}},
		Coverage: []Coverage{{Item: "web-check", State: "skip", Reason: "超时"}},
		Budget:   8,
	}
	acts, err := Rules{}.Decide(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if len(acts) == 0 || acts[0].Kind != action.KindReplay || acts[0].Args["id"] != "L-1" {
		t.Fatalf("高危未复验应排在 rerun 前面，实际 %+v", acts)
	}
}
