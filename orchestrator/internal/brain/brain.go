// Package brain 是"下一步做什么"的决策器。
//
// 三种实现，同一个接口：
//   - Rules：纯 Go 规则，零依赖，离线可用，也是 LLM 不可用时的兜底
//   - LLM：调 OpenAI 兼容接口，输出严格 JSON，schema 不合法就重试
//   - Static：读一份人工写的 plan.json，用于"人在外面想好、带进内网执行"
//
// 编排层永远不知道决策来自哪个 brain —— 这是可替换的关键。
package brain

import (
	"context"

	"github.com/Kur1sulab/lance/orchestrator/internal/action"
)

type Finding struct {
	ID       string `json:"id"`
	Check    string `json:"check"`
	Severity string `json:"severity"`
	Level    string `json:"level"`
	Conf     string `json:"confidence"`
	Target   string `json:"target"`
}

type Coverage struct {
	Item   string `json:"item"`
	State  string `json:"state"`
	Reason string `json:"reason"`
}

type Input struct {
	Findings []Finding  `json:"findings"`
	Coverage []Coverage `json:"coverage"`
	History  []string   `json:"history"` // 已执行过的动作签名，防重复
	Budget   int        `json:"budget"`  // 本轮最多几个动作
}

type Brain interface {
	Name() string
	Decide(ctx context.Context, in Input) ([]action.Action, error)
}
