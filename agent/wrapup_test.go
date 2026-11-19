package agent

import (
	"strings"
	"testing"
	"unicode"
)

// A2: wrap-up / settlement 提示词语言。
//
// 这些常量在 run 或 task 因阶段/时间预算而终止时，在 settlement 阶段
// 被注入，直接指示将原样暴露给用户的最终摘要。因此（1）以中文
// 必须编写，且 (2) 不得残留韩文(Hangul)，(3) 工具名称与
// 类似"一句话纯文本"的指示含义必须保留。
//
// DB 种子将 wrapup_prompt / task_timeout_wrapup_prompt 留为空字符串(db/db.go 的
// builtin 智能体 INSERT 不填充该列)，若为空则回退到该常量。
// 即这些常量是 wrap-up 文案的唯一来源。
func TestWrapupPromptsLocalizedToChinese(t *testing.T) {
	all := map[string]string{
		"settleWrapUpPrompt":        settleWrapUpPrompt,
		"plannerWrapUpDefault":      plannerWrapUpDefault,
		"mainAgentWrapUpDefault":    mainAgentWrapUpDefault,
		"genericWrapUpDefault":      genericWrapUpDefault,
		"workerTaskTimeoutDefault":  workerTaskTimeoutDefault,
		"plannerTaskTimeoutDefault": plannerTaskTimeoutDefault,
	}

	hasScript := func(s string, table *unicode.RangeTable) bool {
		for _, r := range s {
			if unicode.Is(table, r) {
				return true
			}
		}
		return false
	}

	for name, p := range all {
		if hasScript(p, unicode.Hangul) {
			t.Errorf("%s：残留韩文，未完成中文化：%q", name, p)
		}
		if !hasScript(p, unicode.Han) {
			t.Errorf("%s：完全不含中文，未做中文化处理", name)
		}
	}

// 工具名称是标识符，因此不应翻译，必须原样保留。
	// worker 系列(per-run·task-timeout)用 record_fact 记录结论，用 report_finding
	// 写入漏洞，并在最后保留用一句纯文本作总结的指令。
	mustContain := func(name, p string, subs ...string) {
		for _, s := range subs {
			if !strings.Contains(p, s) {
				t.Errorf("%s：应保留的指令含义 %q 缺失", name, s)
			}
		}
	}
	mustContain("settleWrapUpPrompt", settleWrapUpPrompt,
		"insert_assets", "record_fact", "report_finding", "一句", "纯文本")
	mustContain("workerTaskTimeoutDefault", workerTaskTimeoutDefault,
		"insert_assets", "record_fact", "report_finding", "一句", "纯文本")
	mustContain("genericWrapUpDefault", genericWrapUpDefault, "一句", "纯文本")
	mustContain("mainAgentWrapUpDefault", mainAgentWrapUpDefault, "一句", "纯文本")
	// planner 不产出摘要句（只做判定后结束），并保留意图、目标、待办工具。
	mustContain("plannerWrapUpDefault", plannerWrapUpDefault, "add_intent", "prove_goal", "TodoWrite")
	mustContain("plannerTaskTimeoutDefault", plannerTaskTimeoutDefault, "prove_goal")
}

// per-run 与 task-timeout 的语义必须不同（尤其是 planner）：per-run 是“仅本轮
// 结束"，而 task-timeout 是 "整个任务结束"。常量映射不能改变而混淆。
func TestWrapupDefaultsRouting(t *testing.T) {
	if WrapupDefault("worker") != settleWrapUpPrompt {
		t.Error("worker 的 per-run 默认值不是 settleWrapUpPrompt")
	}
	if WrapupDefault("planner") != plannerWrapUpDefault {
		t.Error("planner 的 per-run 默认值不是 plannerWrapUpDefault")
	}
	if WrapupDefault("mainagent") != mainAgentWrapUpDefault {
		t.Error("mainagent 的 per-run 默认值不是 mainAgentWrapUpDefault")
	}
	// 未注册的键（自定义 agent）会落到 generic。
	if WrapupDefault("unknown-agent") != genericWrapUpDefault {
		t.Error("未注册的键不会落到 genericWrapUpDefault")
	}
	// task-timeout 仅存在于 worker/planner，其余为空字符串(调用方回退到 per-run)。
	if TaskTimeoutWrapupDefault("worker") != workerTaskTimeoutDefault {
		t.Error("worker 的 task-timeout 默认值不是 workerTaskTimeoutDefault")
	}
	if TaskTimeoutWrapupDefault("planner") != plannerTaskTimeoutDefault {
		t.Error("planner 的 task-timeout 默认值不是 plannerTaskTimeoutDefault")
	}
	if TaskTimeoutWrapupDefault("mainagent") != "" {
		t.Error("mainagent 不应有 task-timeout 文案（空字符串）")
	}
	// 如果 per-run 与 task-timeout 的文案相同，语义区分就消失了。
	if workerTaskTimeoutDefault == settleWrapUpPrompt {
		t.Error("worker 的 per-run 与 task-timeout 文案相同")
	}
	if plannerTaskTimeoutDefault == plannerWrapUpDefault {
		t.Error("planner 的 per-run 与 task-timeout 文案相同")
	}
}
