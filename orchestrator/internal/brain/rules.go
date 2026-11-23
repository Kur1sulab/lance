package brain

import (
	"context"
	"strings"

	"github.com/Kur1sulab/lance/orchestrator/internal/action"
)

// Rules 是零依赖的规则决策器：不联网、不需要模型，现场断网也能用。
//
// 规则优先级的依据来自实战胜负：L2 高危未复验 > 覆盖空档 > 低危复验。
// 它不如 LLM 灵活，但同输入必然同输出 —— 保证编排层永远不会因为
// 模型抽风把整轮跑歪。
type Rules struct{}

func (Rules) Name() string { return "rules" }

func severityRank(s string) int {
	switch strings.ToLower(s) {
	case "critical":
		return 4
	case "high":
		return 3
	case "medium":
		return 2
	case "low":
		return 1
	}
	return 0
}

func (Rules) Decide(_ context.Context, in Input) ([]action.Action, error) {
	seen := map[string]bool{}
	for _, h := range in.History {
		seen[h] = true
	}

	var out []action.Action
	budget := in.Budget
	if budget <= 0 {
		budget = 8
	}

	// 优先级 1：高危且尚未复验的发现 —— 直接决定交付质量
	for _, f := range in.Findings {
		if len(out) >= budget {
			break
		}
		if severityRank(f.Severity) < 3 {
			continue
		}
		sig := "replay:" + f.ID
		if seen[sig] {
			continue
		}
		out = append(out, action.Action{
			Kind:   action.KindReplay,
			Target: f.Target,
			Args:   map[string]string{"id": f.ID},
			Why:    "severity=" + f.Severity + " check=" + f.Check + " 尚未复验",
			Conf:   0.9,
		})
	}

	// 优先级 2：覆盖表里 skip 且有明确原因的项 —— 这些是"没测到"，
	// 补测比重复测已知漏洞更有价值。
	//
	// rerun 是整批重扫（目标取自 findings.json），一轮派一条就够：
	// 同一批目标派 N 条只会被扫 N 遍。签名必须和执行器的
	// "rerun:<targets>:<intensity>" 对齐（这里不带 targets），
	// 否则账本里的历史认不出「已经重跑过」，每轮都会重复派同一条。
	const rerunIntensity = "high"
	rerunSig := "rerun::" + rerunIntensity
	if !seen[rerunSig] && len(out) < budget {
		for _, c := range in.Coverage {
			if c.State != "skip" || c.Reason == "" {
				continue
			}
			out = append(out, action.Action{
				Kind: action.KindRerun,
				Args: map[string]string{"intensity": rerunIntensity},
				Why:  "覆盖项 " + c.Item + " 被跳过：" + c.Reason,
				Conf: 0.6,
			})
			seen[rerunSig] = true
			break
		}
	}

	if len(out) == 0 {
		out = append(out, action.Action{
			Kind: action.KindStop,
			Why:  "没有待办：高危项均已复验，覆盖表无可用补测项",
			Conf: 1.0,
		})
	}
	return out, nil
}
