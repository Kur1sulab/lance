// Package exec 把 action 变成真实的工具调用。
//
// 它是 AI 与目标之间唯一的闸门：这里只实现了白名单里的几个 Kind，
// 而且一律走 exec.Command 的参数数组（不经 shell），
// 模型再怎么"想"也构造不出管道、重定向或第二条命令。
package exec

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Kur1sulab/lance/orchestrator/internal/action"
)

type Verdict string

const (
	VerdictReproduced Verdict = "reproduced" // 仍可复现
	VerdictUnverified Verdict = "unverified" // 环境不一致，不能下结论
	VerdictGone       Verdict = "gone"       // 确实没了
	VerdictError      Verdict = "error"
)

type Result struct {
	Signature string        `json:"signature"`
	Kind      string        `json:"kind"`
	Verdict   Verdict       `json:"verdict"`
	Detail    string        `json:"detail"`
	Raw       string        `json:"raw"`
	Duration  time.Duration `json:"duration_ns"`
}

type Runner struct {
	LitmusPath string // litmus 可执行文件
	Findings   string // findings.json 路径（replay 用）
	OutDir     string // 产物目录
	Timeout    time.Duration
	Templates  string // nuclei 模板目录（rerun 用，可选）
}

func (r Runner) sig(a action.Action) string {
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

// Do 执行一个动作。任何失败都返回 Result（带 error 型 verdict），
// 不抛异常 —— 一个动作失败不该中断整条编排链。
func (r Runner) Do(ctx context.Context, a action.Action) Result {
	if err := a.Validate(); err != nil {
		return Result{Signature: r.sig(a), Verdict: VerdictError, Detail: "非法动作: " + err.Error()}
	}
	if a.Kind == action.KindStop {
		return Result{Signature: "stop", Kind: string(a.Kind), Verdict: VerdictReproduced, Detail: "收工：" + a.Why}
	}

	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	var args []string
	switch a.Kind {
	case action.KindReplay:
		args = []string{"replay", "-f", r.Findings, "--id", a.Args["id"]}
	case action.KindRunTpl:
		args = []string{"run", "-t", a.Args["target"], "--templates", a.Args["tpl"], "-out", r.OutDir}
	case action.KindRerun:
		targets, err := r.rerunTargets(a)
		if err != nil {
			return Result{Signature: r.sig(a), Kind: string(a.Kind), Verdict: VerdictError, Detail: err.Error()}
		}
		args = []string{"run"}
		for _, t := range targets {
			args = append(args, "-t", t)
		}
		intensity := strings.TrimSpace(a.Args["intensity"])
		if intensity == "" {
			intensity = "normal"
		}
		args = append(args, "--intensity", intensity, "--json")
		if r.Templates != "" {
			args = append(args, "--templates", r.Templates)
		}
		outDir := r.OutDir
		if outDir == "" {
			outDir = "runs" // 与 litmus 自己的默认落点一致，不散落目录
		}
		args = append(args, "--out", filepath.Join(outDir, "rerun-"+time.Now().Format("20060102-150405")))
	}

	start := time.Now()
	cmd := exec.CommandContext(cctx, r.LitmusPath, args...)
	out, err := cmd.CombinedOutput()
	text := string(out)
	res := Result{
		Signature: r.sig(a),
		Kind:      string(a.Kind),
		Raw:       text,
		Duration:  time.Since(start),
	}
	if err != nil {
		res.Verdict = VerdictError
		res.Detail = fmt.Sprintf("执行失败: %v", err)
		if len(text) > 400 {
			res.Detail += " | " + text[len(text)-400:]
		}
		return res
	}

	// rerun 的判据和单条复验不同：它跑的是一整轮，结论看 JSON 摘要里的
	// 命中条数，而不是某一行文案。
	if a.Kind == action.KindRerun {
		res.Verdict, res.Detail = rerunVerdict(text)
		return res
	}

	// 判据只认 litmus 自己打印的结论行，不猜
	switch {
	case strings.Contains(text, "复现成功"):
		res.Verdict = VerdictReproduced
		res.Detail = "仍可复现"
	case strings.Contains(text, "未复验"):
		res.Verdict = VerdictUnverified
		res.Detail = firstLineWith(text, "无法复验")
	case strings.Contains(text, "已消失"):
		res.Verdict = VerdictGone
		res.Detail = "本次未命中，判定已消失"
	default:
		res.Verdict = VerdictError
		res.Detail = "输出里没有可识别的判定行"
	}
	return res
}

func firstLineWith(s, sub string) string {
	for _, line := range strings.Split(s, "\n") {
		if strings.Contains(line, sub) {
			return strings.TrimSpace(line)
		}
	}
	return sub
}

// maxRerunTargets 单轮重跑的目标上限：现场一口气扫上百个目标既跑不完，
// 也会把时间预算烧在第一个网段上，宁可让调用方分批。
const maxRerunTargets = 64

// splitTargets 拆手写目标清单：逗号、分号、换行、空白都算分隔符。
// 从报告里复制目标时这几种混着出现是常态。
func splitTargets(raw string) []string {
	sep := func(r rune) bool {
		switch r {
		case ',', ';', '，', '；', '\n', '\r', '	', ' ':
			return true
		}
		return false
	}
	seen := map[string]bool{}
	var out []string
	for _, t := range strings.FieldsFunc(raw, sep) {
		t = strings.TrimSpace(t)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	return out
}

// rerunTargets 决定这一轮重跑打哪些目标：优先用动作里显式给的清单，
// 否则退回 findings.json 里出现过的目标。两者都没有就报错停下 ——
// 猜目标等于把扫描打到范围外，这一步不允许"聪明"。
func (r Runner) rerunTargets(a action.Action) ([]string, error) {
	if raw := strings.TrimSpace(a.Args["targets"]); raw != "" {
		ts := splitTargets(raw)
		if len(ts) == 0 {
			return nil, fmt.Errorf("rerun 的 args.targets 解析后为空: %q", raw)
		}
		if len(ts) > maxRerunTargets {
			return nil, fmt.Errorf("rerun 目标 %d 个，超过单轮上限 %d，请分批", len(ts), maxRerunTargets)
		}
		return ts, nil
	}
	if r.Findings == "" {
		return nil, fmt.Errorf("rerun 需要目标清单：动作没给 args.targets，也没有 findings.json 可兜底")
	}
	b, err := os.ReadFile(r.Findings)
	if err != nil {
		return nil, fmt.Errorf("rerun 读 findings 失败: %w", err)
	}
	ts := targetsFromFindings(b)
	if len(ts) == 0 {
		return nil, fmt.Errorf("findings.json 里没有可用 target，rerun 无从下手: %s", r.Findings)
	}
	if len(ts) > maxRerunTargets {
		ts = ts[:maxRerunTargets]
	}
	return ts, nil
}

// targetsFromFindings 兼容两种落盘形态：{"findings":[...]} 与裸数组。
// 排序是为了确定性：同一份 findings 必须拼出同样的命令行，否则历史去重会失效。
func targetsFromFindings(b []byte) []string {
	type row struct {
		Target string `json:"target"`
	}
	var wrap struct {
		Findings []row `json:"findings"`
	}
	var rows []row
	if err := json.Unmarshal(b, &wrap); err == nil && wrap.Findings != nil {
		rows = wrap.Findings
	} else if err := json.Unmarshal(b, &rows); err != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	for _, r := range rows {
		t := strings.TrimSpace(r.Target)
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// rerunVerdict 读一整轮的结论：优先解析 litmus --json 末尾的摘要，
// 拿不到再退回人类可读的「发现 N 条」统计行。
func rerunVerdict(text string) (Verdict, string) {
	trimmed := strings.TrimSpace(text)
	if i := strings.LastIndex(trimmed, "\n{"); i >= 0 {
		var sum struct {
			Findings []struct {
				ID     string `json:"id"`
				Target string `json:"target"`
			} `json:"findings"`
			Out string `json:"out"`
		}
		if err := json.Unmarshal([]byte(strings.TrimSpace(trimmed[i:])), &sum); err == nil {
			if n := len(sum.Findings); n > 0 {
				return VerdictReproduced, fmt.Sprintf("重跑命中 %d 条（产物 %s）", n, sum.Out)
			}
			return VerdictUnverified, fmt.Sprintf("重跑完成，本轮无命中（产物 %s）", sum.Out)
		}
	}
	if n, ok := countFromSummary(trimmed); ok {
		if n > 0 {
			return VerdictReproduced, fmt.Sprintf("重跑命中 %d 条", n)
		}
		return VerdictUnverified, "重跑完成，本轮无命中"
	}
	return VerdictError, "重跑输出里既没有 JSON 摘要也没有发现统计行"
}

// countFromSummary 读人类可读摘要里的「发现 N 条」。
func countFromSummary(s string) (int, bool) {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		line := strings.TrimSpace(lines[i])
		if !strings.HasPrefix(line, "发现 ") {
			continue
		}
		rest := strings.TrimPrefix(line, "发现 ")
		if end := strings.IndexAny(rest, " 	"); end > 0 {
			rest = rest[:end]
		}
		n := 0
		if _, err := fmt.Sscanf(rest, "%d", &n); err == nil {
			return n, true
		}
	}
	return 0, false
}
