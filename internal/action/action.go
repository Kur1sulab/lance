// Package action 定义编排层能执行的**全部**动作。
//
// 设计前提：AI 的输出是不可信输入。它只能产出这里的几个 Kind，
// 参数名必须在白名单里，执行器也只实现这几个 Kind —— AI 没有任何
// 途径让编排层执行任意命令。
package action

import (
	"errors"
	"fmt"
)

type Kind string

const (
	KindReplay  Kind = "replay"   // 复验一条已有发现
	KindRunTpl  Kind = "run_tpl"  // 用指定模板打一个新目标
	KindRerun   Kind = "rerun"    // 换强度重跑一轮
	KindStop    Kind = "stop"     // 收工
)

// allowedArgs 每个 Kind 允许出现的参数名，多一个都拒绝。
var allowedArgs = map[Kind]map[string]bool{
	KindReplay: {"id": true},
	KindRunTpl: {"target": true, "tpl": true},
	KindRerun:  {"targets": true, "intensity": true},
	KindStop:   {},
}

type Action struct {
	Kind   Kind              `json:"kind"`
	Target string            `json:"target,omitempty"`
	Args   map[string]string `json:"args,omitempty"`
	Why    string            `json:"why"`  // 决策理由，落盘备查
	Conf   float64           `json:"conf"` // 0~1，低于阈值不执行
}

func (a Action) Validate() error {
	if a.Kind == "" {
		return errors.New("kind 为空")
	}
	allowed, ok := allowedArgs[a.Kind]
	if !ok {
		return fmt.Errorf("未知动作 %q（只允许 replay/run_tpl/rerun/stop）", a.Kind)
	}
	for k := range a.Args {
		if !allowed[k] {
			return fmt.Errorf("动作 %s 不接受参数 %q", a.Kind, k)
		}
	}
	switch a.Kind {
	case KindReplay:
		if a.Args["id"] == "" {
			return errors.New("replay 必须带 args.id")
		}
	case KindRunTpl:
		if a.Args["target"] == "" || a.Args["tpl"] == "" {
			return errors.New("run_tpl 必须带 args.target 和 args.tpl")
		}
	case KindRerun:
		// targets 允许留空（执行器会退回 findings.json 取目标），
		// 但强度必须是我们认识的档位，否则 litmus 会直接退出。
		if it := a.Args["intensity"]; it != "" && !validIntensity(it) {
			return fmt.Errorf("rerun 的强度 %q 不合法（只允许 low/normal/high）", it)
		}
	}
	if a.Conf < 0 || a.Conf > 1 {
		return fmt.Errorf("conf=%v 越界（0~1）", a.Conf)
	}
	if a.Why == "" {
		return errors.New("why 不能为空 —— 没有理由的决策无法审计")
	}
	return nil
}

// validIntensity 与 litmus 的 INTENSITY 档位保持一致；
// 拼错档位在 litmus 侧是 argparse 直接退出，这里提前拦住更省一轮现场时间。
func validIntensity(s string) bool {
	switch s {
	case "low", "normal", "high":
		return true
	}
	return false
}

// ValidateAll 校验一批动作，遇到第一个不合法的就整体拒绝
// （半批执行会让现场状态变成说不清的中间态）。
func ValidateAll(as []Action) error {
	for i, a := range as {
		if err := a.Validate(); err != nil {
			return fmt.Errorf("第 %d 个动作不合法: %w", i+1, err)
		}
	}
	return nil
}
