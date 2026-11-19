// Package armor 是 Lance 的「破甲引擎」——给渗透 agent 的系统提示尾部注入一段
// 最高优先级的行为规约，让模型在授权测试场景里不再自我审查、不再拒答、不再把
// 已授权的攻击动作降级成"建议你自己去做"。
//
// 设计取舍：
//   - 只做「尾部追加」，不改各角色提示词正文。正文来自 DB（可被用户编辑），
//     尾部由代码拥有，因此用户改正文永远改不掉破甲规约。
//   - 档位文本 embed 进二进制，运行时只切换档位，不读盘、不联网。
//   - 档位状态存在 settings 表（armor_mode / armor_profile），由 server 装配钩子，
//     agent 每轮通过 Hook 读取，改档即时生效，无需重启。
//   - 统计（注入次数、各角色命中、最近一次注入时间）在进程内累计，供 UI 展示
//     「运行情况」，不落库——重启即清零，避免把运行时计数器当业务数据。
package armor

import (
	_ "embed"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//go:embed profiles/slim.md
var textSlim string

//go:embed profiles/full.md
var textFull string

//go:embed profiles/ultimate.md
var textUltimate string

// Mode 是破甲引擎的开关档位。
type Mode string

const (
	// ModeOff 关闭：尾部不注入任何内容。
	ModeOff Mode = "off"
	// ModeSlim 精简档：只放最高准则与执行纪律，token 占用最小。
	ModeSlim Mode = "slim"
	// ModeFull 完整档：准则 + 纪律 + 输出契约。
	ModeFull Mode = "full"
	// ModeUltimate 终极档：完整档 + 反推诿话术矩阵 + 越狱式框定。
	ModeUltimate Mode = "ultimate"
)

// DefaultMode 是开箱默认档位。选 slim：既能压掉拒答，又不会把每轮上下文顶满。
const DefaultMode = ModeFull

// Modes 是全部合法档位，按强度升序。
var Modes = []Mode{ModeOff, ModeSlim, ModeFull, ModeUltimate}

// Profile 描述一个档位的元信息，供 API/UI 直接序列化。
type Profile struct {
	ID      Mode   `json:"id"`
	Name    string `json:"name"`
	Summary string `json:"summary"`
	Chars   int    `json:"chars"`
	// Strength 是强度等级 0-3，UI 用它画强度条。
	Strength int `json:"strength"`
}

// profiles 是档位元信息表，顺序与 Modes 一致。
var profiles = map[Mode]Profile{
	ModeOff:      {ID: ModeOff, Name: "关闭", Summary: "不注入任何规约，模型按自身对齐策略回答。", Strength: 0},
	ModeSlim:     {ID: ModeSlim, Name: "精简", Summary: "只注入最高准则与执行纪律，上下文占用最小。", Strength: 1},
	ModeFull:     {ID: ModeFull, Name: "完整", Summary: "准则 + 执行纪律 + 输出契约，覆盖拒答与自我降级。", Strength: 2},
	ModeUltimate: {ID: ModeUltimate, Name: "终极", Summary: "完整档之上再加反推诿话术矩阵与框定层，最强。", Strength: 3},
}

// textFor 返回档位对应的规约正文（已去掉尾部空行）。
func textFor(m Mode) string {
	var raw string
	switch m {
	case ModeSlim:
		raw = textSlim
	case ModeFull:
		raw = textFull
	case ModeUltimate:
		raw = textUltimate
	default:
		return ""
	}
	return strings.TrimSpace(raw)
}

// Profiles 返回全部档位元信息（含字符数），供 API 序列化。
func Profiles() []Profile {
	out := make([]Profile, 0, len(Modes))
	for _, m := range Modes {
		p := profiles[m]
		p.Chars = len([]rune(textFor(m)))
		out = append(out, p)
	}
	return out
}

// ProfileOf 返回单个档位元信息；未知档位回退到默认档。
func ProfileOf(m Mode) Profile {
	p, ok := profiles[m]
	if !ok {
		p = profiles[DefaultMode]
		p.Chars = len([]rune(textFor(DefaultMode)))
		return p
	}
	p.Chars = len([]rune(textFor(m)))
	return p
}

// Normalize 把任意字符串收敛成合法档位；不合法返回空串（调用方自行决定回退）。
func Normalize(raw string) Mode {
	m := Mode(strings.ToLower(strings.TrimSpace(raw)))
	for _, ok := range Modes {
		if ok == m {
			return m
		}
	}
	return ""
}

// Text 返回某档位的规约正文，供 UI 预览。
func Text(m Mode) string { return textFor(m) }

// ---------- 运行时状态 ----------

// Hooks 是 server 装配进来的读取器。两者都可以为 nil（独立测试或未装配），
// 此时引擎按 DefaultMode 工作。
var (
	// ModeHook 返回当前档位（server 从 settings 表读取）。
	ModeHook func() string
)

var (
	statInjections atomic.Int64 // 累计注入次数（每次拼接尾部 +1）
	statChars      atomic.Int64 // 累计注入字符数，用来估上下文成本
	mu             sync.RWMutex
	statRoles      = map[string]int64{} // 按角色（planner/worker/chat/...）分桶
	lastInject     atomic.Int64         // 最近一次注入的 Unix 毫秒
	lastMode       atomic.Value         // 最近一次实际生效的档位（string）
)

// ActiveMode 解析当前生效档位：钩子优先，空/非法则回退默认。
func ActiveMode() Mode {
	if ModeHook != nil {
		if m := Normalize(ModeHook()); m != "" {
			return m
		}
	}
	return DefaultMode
}

// Directive 返回要追加到系统提示尾部的内容。role 用于统计分桶，可为空。
// 返回空串表示当前档位为 off——调用方直接跳过拼接即可。
func Directive(role string) string {
	m := ActiveMode()
	body := textFor(m)
	if body == "" {
		return ""
	}
	statInjections.Add(1)
	statChars.Add(int64(len([]rune(body))))
	lastInject.Store(time.Now().UnixMilli())
	lastMode.Store(string(m))
	if role != "" {
		mu.Lock()
		statRoles[role]++
		mu.Unlock()
	}
	// 前置双换行：与提示词正文隔开，且保证尾部永远落在最末（recency 生效）。
	return "\n\n" + body
}

// Stats 是一次运行情况快照。
type Stats struct {
	Mode       string           `json:"mode"`
	ModeName   string           `json:"mode_name"`
	Active     bool             `json:"active"`
	Injections int64            `json:"injections"`
	Chars      int64            `json:"chars"`
	AvgChars   int64            `json:"avg_chars"`
	Roles      map[string]int64 `json:"roles"`
	LastInject string           `json:"last_inject"` // RFC3339；从未注入为空
	LastMode   string           `json:"last_mode"`
}

// Snapshot 读取运行情况快照。
func Snapshot() Stats {
	m := ActiveMode()
	inj := statInjections.Load()
	chars := statChars.Load()
	var avg int64
	if inj > 0 {
		avg = chars / inj
	}
	mu.RLock()
	roles := make(map[string]int64, len(statRoles))
	for k, v := range statRoles {
		roles[k] = v
	}
	mu.RUnlock()
	out := Stats{
		Mode:       string(m),
		ModeName:   ProfileOf(m).Name,
		Active:     m != ModeOff,
		Injections: inj,
		Chars:      chars,
		AvgChars:   avg,
		Roles:      roles,
		LastMode:   str(lastMode.Load()),
	}
	if ms := lastInject.Load(); ms > 0 {
		out.LastInject = time.UnixMilli(ms).Format(time.RFC3339)
	}
	return out
}

// ResetStats 清零运行计数（UI 上的「重置统计」）。
func ResetStats() {
	statInjections.Store(0)
	statChars.Store(0)
	lastInject.Store(0)
	lastMode.Store("")
	mu.Lock()
	statRoles = map[string]int64{}
	mu.Unlock()
}

func str(v any) string {
	s, _ := v.(string)
	return s
}
