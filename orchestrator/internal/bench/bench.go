// Package bench 对同一份 findings 跑多个决策脑，量化它们的差异。
//
// 存在的理由：「上了 AI 到底有没有变好」不能靠感觉回答。把 rules 与 llm
// 喂完全相同的输入，各自的动作条数、kind 分布、签名集合、耗时落在同一份
// JSON 里，交集/差集/重合率就是可复现的证据。
//
// llm 未配置时标记 unavailable 继续跑而不是报错退出 —— 现场经常断网，
// bench 本身必须离线可用；拿不可用档位的空集去算重合率会得出「0% 重合」
// 的假结论，所以只有两档都真正跑出决策时才计算 diff。
package bench

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/Kur1sulab/lance/orchestrator/internal/action"
	"github.com/Kur1sulab/lance/orchestrator/internal/brain"
)

// 档位状态。unavailable 与 error 分开：前者是「没配置，预期回落」，
// 后者是「配置了但这次没跑成」，读报告的人要能一眼区分这两种失败。
const (
	StatusOK          = "ok"
	StatusUnavailable = "unavailable"
	StatusError       = "error"
)

type Options struct {
	Findings string   // findings.json 路径
	Budget   int      // 本轮动作预算，两档共用，保证输入一致
	Brains   []string // 档位名，按序执行

	// Resolve 把档位名变成 brain 实例，ok=false 表示该档不可用。
	// 留空用 DefaultResolve；测试注入假 brain，不依赖真实环境变量与网络。
	Resolve func(name string) (brain.Brain, bool)
}

type BrainResult struct {
	Brain      string          `json:"brain"`             // 档位名，与 -brain 对应：rules / llm
	Name       string          `json:"name,omitempty"`    // brain.Name()，llm 档带模型名，便于追溯
	Status     string          `json:"status"`            // ok / unavailable / error
	Detail     string          `json:"detail,omitempty"`  // 非 ok 时的原因
	Actions    []action.Action `json:"actions,omitempty"` // 原始决策，why/conf 落盘备查
	Count      int             `json:"count"`             // 动作条数
	Kinds      map[string]int  `json:"kinds"`             // 每种 kind 的条数
	Signatures []string        `json:"signatures"`        // 签名集合（排序去重）
	DurationMS float64         `json:"duration_ms"`       // Decide 耗时，毫秒
}

type Diff struct {
	A         string   `json:"a"`         // 对比左侧档位名
	B         string   `json:"b"`         // 对比右侧档位名
	Intersect []string `json:"intersect"` // 两边都有的签名
	OnlyA     []string `json:"only_a"`    // 仅 A 有
	OnlyB     []string `json:"only_b"`    // 仅 B 有
	Union     int      `json:"union"`     // 并集大小，重合率的分母
	Overlap   float64  `json:"overlap"`   // 交集÷并集，保留两位小数
}

type Report struct {
	TS       string        `json:"ts"`
	Findings string        `json:"findings"`
	Budget   int           `json:"budget"`
	Brains   []BrainResult `json:"brains"`
	Diff     *Diff         `json:"diff"`                // 两档都 ok 才有，否则为 null
	DiffNote string        `json:"diff_note,omitempty"` // 为什么没有 diff
}

// ParseBrains 拆 -brain 的逗号清单：去空白、去重、只认已知档位。
// 未知名字直接报错而不是静默跳过 —— 拼错档位的报告会让人误以为对比过了，
// 这比跑不起来更危险。
func ParseBrains(s string) ([]string, error) {
	seen := map[string]bool{}
	var out []string
	for _, raw := range strings.Split(s, ",") {
		name := strings.TrimSpace(raw)
		if name == "" || seen[name] {
			continue
		}
		switch name {
		case "rules", "llm":
		default:
			return nil, fmt.Errorf("未知决策脑 %q（只支持 rules,llm）", name)
		}
		seen[name] = true
		out = append(out, name)
	}
	if len(out) == 0 {
		return nil, errors.New("-brain 不能为空")
	}
	return out, nil
}

// DefaultResolve 是生产用的档位解析：rules 永远可用，llm 看环境变量。
func DefaultResolve(name string) (brain.Brain, bool) {
	switch name {
	case "rules":
		return brain.Rules{}, true
	case "llm":
		l, ok := brain.EnvLLM()
		if !ok {
			return nil, false
		}
		return l, true
	}
	return nil, false
}

// Signature 与 internal/exec 的 Runner.sig 逐字对齐（账本里落的就是它）。
// exec 本次不动、sig 又是私有方法，这里只能复刻一份；改任何一边都必须
// 同步另一边，否则 bench 的签名集合和账本历史对不上，重合率就成了假数。
func Signature(a action.Action) string {
	switch a.Kind {
	case action.KindReplay:
		return "replay:" + a.Args["id"]
	case action.KindRunTpl:
		return "run_tpl:" + a.Args["target"] + ":" + a.Args["tpl"]
	case action.KindRerun:
		return "rerun:" + a.Args["targets"] + ":" + a.Args["intensity"]
	}
	return "stop"
}

// Compare 算两个签名集合的交集/差集/重合率。入参按集合处理：去重且结果
// 排序，保证同一批决策无论输出顺序如何，diff 都逐字节一致 —— 产物要能
// 跨两次运行直接比对，顺序抖动会污染结论。
func Compare(a, b []string) Diff {
	setA, setB := sigSet(a), sigSet(b)
	var d Diff
	for s := range setA {
		if setB[s] {
			d.Intersect = append(d.Intersect, s)
		} else {
			d.OnlyA = append(d.OnlyA, s)
		}
	}
	for s := range setB {
		if !setA[s] {
			d.OnlyB = append(d.OnlyB, s)
		}
	}
	sort.Strings(d.Intersect)
	sort.Strings(d.OnlyA)
	sort.Strings(d.OnlyB)
	d.Union = len(setA) + len(setB) - len(d.Intersect)
	// 两边都空时并集为 0，重合率记 0 而不是 NaN：JSON 里出现 NaN 会直接
	// 让产物解析失败。
	if d.Union > 0 {
		d.Overlap = math.Round(float64(len(d.Intersect))/float64(d.Union)*100) / 100
	}
	return d
}

func sigSet(sigs []string) map[string]bool {
	m := make(map[string]bool, len(sigs))
	for _, s := range sigs {
		m[s] = true
	}
	return m
}

// Run 依次跑每个档位并汇总对比。单个档位失败（未配置或 Decide 报错）
// 只记进该档结果，不中断整份报告 —— 另一档的结果仍然是有效证据。
func Run(ctx context.Context, opts Options) (*Report, error) {
	if opts.Findings == "" {
		return nil, errors.New("bench 需要 findings.json 路径")
	}
	if len(opts.Brains) == 0 {
		return nil, errors.New("bench 至少需要一个决策脑")
	}
	if opts.Budget <= 0 {
		return nil, fmt.Errorf("budget 必须为正数，得到 %d", opts.Budget)
	}
	ff, err := loadFindings(opts.Findings)
	if err != nil {
		return nil, err
	}
	resolve := opts.Resolve
	if resolve == nil {
		resolve = DefaultResolve
	}

	// 两档喂完全相同的 Input：带上文件里的覆盖表，但不带账本历史 ——
	// bench 是「从零开始同一道题」的对比，历史会让先跑的档位占便宜。
	in := brain.Input{Findings: ff.Findings, Coverage: ff.Coverage, Budget: opts.Budget}
	rep := &Report{
		TS:       time.Now().Format(time.RFC3339),
		Findings: opts.Findings,
		Budget:   opts.Budget,
	}
	for _, name := range opts.Brains {
		rep.Brains = append(rep.Brains, runOne(ctx, resolve, name, in))
	}

	var ok []BrainResult
	for _, r := range rep.Brains {
		if r.Status == StatusOK {
			ok = append(ok, r)
		}
	}
	if len(ok) >= 2 {
		d := Compare(ok[0].Signatures, ok[1].Signatures)
		d.A, d.B = ok[0].Brain, ok[1].Brain
		rep.Diff = &d
	} else {
		rep.DiffNote = diffNote(rep.Brains)
	}
	return rep, nil
}

func runOne(ctx context.Context, resolve func(string) (brain.Brain, bool), name string, in brain.Input) BrainResult {
	res := BrainResult{Brain: name, Kinds: map[string]int{}}
	b, ok := resolve(name)
	if !ok {
		res.Status = StatusUnavailable
		if name == "llm" {
			res.Detail = "LANCE_LLM_* 未配置（现场断网时的预期回落，不中断对比）"
		} else {
			res.Detail = "该档位不可用"
		}
		return res
	}
	res.Name = b.Name()

	start := time.Now()
	acts, err := b.Decide(ctx, in)
	res.DurationMS = float64(time.Since(start)) / float64(time.Millisecond)
	if err != nil {
		res.Status = StatusError
		res.Detail = err.Error()
		return res
	}

	res.Status = StatusOK
	res.Actions = acts
	res.Count = len(acts)
	seen := map[string]bool{}
	for _, a := range acts {
		res.Kinds[string(a.Kind)]++
		// 签名按集合去重：同一签名派两条也只算一个决策目标，
		// 否则重合率会被重复动作灌水。
		if sig := Signature(a); !seen[sig] {
			seen[sig] = true
			res.Signatures = append(res.Signatures, sig)
		}
	}
	sort.Strings(res.Signatures)
	return res
}

func diffNote(rs []BrainResult) string {
	var bad []string
	for _, r := range rs {
		if r.Status != StatusOK {
			bad = append(bad, r.Brain+"="+r.Status)
		}
	}
	if len(bad) == 0 {
		return "对比不可用：只有一个决策脑跑出了结果"
	}
	return "对比不可用：" + strings.Join(bad, ", ")
}

// loadFindings 与 main.go 的同名函数保持同一形态：{"findings":[...],"coverage":[...]}。
// 不抽公共包：本次不动 next/exec/loop/prompt 的任何代码路径，宁可复刻十几行。
func loadFindings(path string) (findingsFile, error) {
	var ff findingsFile
	b, err := os.ReadFile(path)
	if err != nil {
		return ff, err
	}
	if err := json.Unmarshal(b, &ff); err != nil {
		return ff, fmt.Errorf("%s 不是合法的 findings.json: %w", path, err)
	}
	return ff, nil
}

type findingsFile struct {
	Findings []brain.Finding  `json:"findings"`
	Coverage []brain.Coverage `json:"coverage"`
}

// Write 落 JSON 产物并返回路径。文件名带时间戳，多次 bench 不互相覆盖，
// 方便把「换模型前后」的两份报告并排留档。
func Write(outDir string, rep *Report) (string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return "", err
	}
	path := filepath.Join(outDir, "bench-"+time.Now().Format("20060102-150405")+".json")
	b, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return "", err
	}
	if err := os.WriteFile(path, append(b, '\n'), 0o644); err != nil {
		return "", err
	}
	return path, nil
}

// Summary 渲染人读摘要。对齐用标准库 tabwriter，不引第三方表格库。
func Summary(rep *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "lance bench  findings=%s  budget=%d\n", rep.Findings, rep.Budget)
	tw := tabwriter.NewWriter(&b, 0, 4, 2, ' ', 0)
	fmt.Fprintln(tw, "档位\t状态\t动作数\t耗时(ms)\tkind 分布")
	for _, r := range rep.Brains {
		count, dur, kinds := "-", "-", "-"
		if r.Status == StatusOK {
			count = strconv.Itoa(r.Count)
			kinds = kindsText(r.Kinds)
		}
		if r.Status != StatusUnavailable {
			dur = fmt.Sprintf("%.2f", r.DurationMS)
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.Brain, r.Status, count, dur, kinds)
	}
	tw.Flush()
	for _, r := range rep.Brains {
		if r.Detail != "" {
			fmt.Fprintf(&b, "  %s: %s\n", r.Brain, r.Detail)
		}
	}
	if rep.Diff != nil {
		d := rep.Diff
		fmt.Fprintf(&b, "对比 %s vs %s：交集 %d / 仅 %s %d / 仅 %s %d / 并集 %d / 重合率 %.2f\n",
			d.A, d.B, len(d.Intersect), d.A, len(d.OnlyA), d.B, len(d.OnlyB), d.Union, d.Overlap)
		printSigs(&b, "交集", d.Intersect)
		printSigs(&b, "仅 "+d.A, d.OnlyA)
		printSigs(&b, "仅 "+d.B, d.OnlyB)
	} else {
		fmt.Fprintln(&b, rep.DiffNote)
	}
	return b.String()
}

// kindsText 按 kind 名排序输出，保证摘要跨运行稳定。
func kindsText(m map[string]int) string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, k+"="+strconv.Itoa(m[k]))
	}
	if len(parts) == 0 {
		return "-"
	}
	return strings.Join(parts, " ")
}

func printSigs(b *strings.Builder, label string, sigs []string) {
	if len(sigs) == 0 {
		fmt.Fprintf(b, "  %s: （无）\n", label)
		return
	}
	fmt.Fprintf(b, "  %s: %s\n", label, strings.Join(sigs, ", "))
}
