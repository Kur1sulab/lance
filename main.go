// lance —— 安全验证的编排层。
//
// 它与 litmus2 的分工是硬边界：
//   litmus2  = 确定性执行器（模板匹配、三态覆盖、证据链、replay），可交给客户
//   lance = 决策器（下一步验证哪个），只产出 action JSON，从不直接接触目标
//
// 三个子命令可以单独用，也可以串成闭环：
//   lance next  -f findings.json            只出计划，不执行
//   lance exec  -a actions.json             只执行，不决策
//   lance loop  -f findings.json -litmus X  决策-执行-记账，跑到收工
//   lance prompt -f findings.json [-check]  打印出站 prompt，供人工核查
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"lance/internal/action"
	"lance/internal/bench"
	"lance/internal/brain"
	"lance/internal/exec"
	"lance/internal/state"
	"lance/internal/web"
)

// version 是产品版本号，与 README 同步维护。
const version = "1.4.0"

func usage() {
	fmt.Printf(`朗基努斯 · Lance v%s —— 验证编排层（决策与执行分离）

用法：
  lance next  -f findings.json [-brain rules|llm] [-budget 8] [-out actions.json]
  lance exec  -a actions.json -litmus <litmus 路径> -findings <findings.json> [-out 目录]
  lance loop  -f findings.json -litmus <litmus 路径> [-brain rules|llm] [-rounds 4]
  lance prompt -f findings.json [-budget 8] [-check]   打印出站 prompt 供核查
  lance serve [-f findings.json] [-litmus <litmus 路径>] [-port 8877] [-open]
  lance bench -f findings.json [-budget 8] [-brain rules,llm] [-out .lance/bench]
        对比 rules 与 llm 的动作差异（kind 分布/签名交集/重合率/耗时），落 JSON 产物。
        llm 未配置时标记 unavailable 继续，不中断对比。

设计约束：
  · 决策层只输出 action JSON，参数走白名单，执行层不经 shell
  · 每条动作的理由（why）都落进 lance-ledger.jsonl，可回放审计
  · 出站 prompt 过中性化词表（LANCE_LLM_SANITIZE=0 可关），本地账本保留原词
  · 没配 LANCE_LLM_BASE/MODEL 时自动回落纯规则 brain，断网也能用
`, version)
}

type findingsFile struct {
	Findings []brain.Finding  `json:"findings"`
	Coverage []brain.Coverage `json:"coverage"`
}

func loadFindings(p string) (findingsFile, error) {
	var ff findingsFile
	b, err := os.ReadFile(p)
	if err != nil {
		return ff, err
	}
	if err := json.Unmarshal(b, &ff); err != nil {
		return ff, fmt.Errorf("%s 不是合法的 findings.json: %w", p, err)
	}
	return ff, nil
}

func pickBrain(name string) (brain.Brain, string) {
	if name == "llm" {
		if l, ok := brain.EnvLLM(); ok {
			return l, "llm"
		}
		return brain.Rules{}, "rules（未配置 LANCE_LLM_BASE/LANCE_LLM_MODEL，已回落）"
	}
	return brain.Rules{}, "rules"
}

func main() {
	if len(os.Args) < 2 {
		usage()
		return
	}
	ctx := context.Background()
	switch os.Args[1] {
	case "next":
		cmdNext(ctx, os.Args[2:])
	case "exec":
		cmdExec(ctx, os.Args[2:])
	case "loop":
		cmdLoop(ctx, os.Args[2:])
	case "prompt":
		cmdPrompt(ctx, os.Args[2:])
	case "serve":
		cmdServe(ctx, os.Args[2:])
	case "bench":
		cmdBench(ctx, os.Args[2:])
	case "version", "-version", "--version", "-v":
		fmt.Printf("朗基努斯 · Lance v%s\n", version)
	default:
		usage()
	}
}

// cmdPrompt 打印决策层真正会发给外部模型的文本。
//
// 存在的理由很具体：带内容审查的推理通道被拦时，出问题的往往不是代码而是
// prompt 措辞，而 prompt 是运行时拼的，看不见就只能猜。把它做成一条可
// diff、可进 CI 的命令，改词表后跑 -check 就能确认出站内容干净。
func cmdPrompt(ctx context.Context, argv []string) {
	fs := flag.NewFlagSet("prompt", flag.ExitOnError)
	fFindings := fs.String("f", "", "findings.json 路径")
	fBudget := fs.Int("budget", 8, "本轮最多动作数")
	fCheck := fs.Bool("check", false, "只做词表自检，不打印全文")
	fs.Parse(argv)

	if *fFindings == "" {
		fmt.Fprintln(os.Stderr, "缺 -f findings.json")
		os.Exit(2)
	}
	ff, err := loadFindings(*fFindings)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	in := brain.Input{Findings: ff.Findings, Coverage: ff.Coverage, Budget: *fBudget}
	p := brain.RenderPrompt(in)
	if *fCheck {
		if hits := brain.SensitiveHits(p); len(hits) > 0 {
			fmt.Printf("出站 prompt 仍命中 %d 个敏感词：%s\n", len(hits), strings.Join(hits, "、"))
			os.Exit(1)
		}
		fmt.Printf("出站 prompt 自检通过（%d 字节，未命中词表）\n", len(p))
		return
	}
	fmt.Print(p)
}

func cmdNext(ctx context.Context, argv []string) {
	fs := flag.NewFlagSet("next", flag.ExitOnError)
	fFindings := fs.String("f", "", "findings.json 路径")
	fBrain := fs.String("brain", "rules", "决策器：rules / llm")
	fBudget := fs.Int("budget", 8, "本轮最多动作数")
	fOut := fs.String("out", "", "把动作写到文件（默认打印到 stdout）")
	fState := fs.String("state", ".lance", "状态目录")
	fs.Parse(argv)

	if *fFindings == "" {
		fmt.Fprintln(os.Stderr, "缺 -f findings.json")
		os.Exit(2)
	}
	ff, err := loadFindings(*fFindings)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	st, err := state.Open(*fState)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hist, _ := st.History()

	br, label := pickBrain(*fBrain)
	acts, err := br.Decide(ctx, brain.Input{
		Findings: ff.Findings, Coverage: ff.Coverage,
		History: hist, Budget: *fBudget,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "决策失败: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("决策器：%s  发现 %d 条 / 覆盖 %d 项 / 历史 %d 条 → 本轮 %d 个动作\n\n",
		label, len(ff.Findings), len(ff.Coverage), len(hist), len(acts))
	for i, a := range acts {
		fmt.Printf("  %d) %-8s %-34s conf=%.2f\n     %s\n", i+1, a.Kind, argBrief(a), a.Conf, a.Why)
	}

	out := struct {
		Actions []action.Action `json:"actions"`
		Brain   string          `json:"brain"`
	}{acts, label}
	b, _ := json.MarshalIndent(out, "", "  ")
	if *fOut != "" {
		os.WriteFile(*fOut, b, 0o644)
		fmt.Printf("\n已写 %s\n", *fOut)
	}
}

func argBrief(a action.Action) string {
	switch a.Kind {
	case action.KindReplay:
		return a.Args["id"]
	case action.KindRunTpl:
		return a.Args["target"] + " " + a.Args["tpl"]
	case action.KindRerun:
		ts := a.Args["targets"]
		if ts == "" {
			ts = "(取自 findings)"
		}
		if len(ts) > 24 {
			ts = ts[:24] + "…"
		}
		return ts + " @" + a.Args["intensity"]
	}
	return ""
}

func cmdExec(ctx context.Context, argv []string) {
	fs := flag.NewFlagSet("exec", flag.ExitOnError)
	fAct := fs.String("a", "", "actions.json 路径")
	fLitmus := fs.String("litmus", "litmus", "litmus 可执行文件")
	fFindings := fs.String("findings", "", "findings.json（replay 需要）")
	fOut := fs.String("out", "", "run_tpl 产物目录")
	fTpl := fs.String("templates", "", "nuclei 模板目录（rerun 用，可选）")
	fState := fs.String("state", ".lance", "状态目录")
	fs.Parse(argv)

	if *fAct == "" {
		fmt.Fprintln(os.Stderr, "缺 -a actions.json")
		os.Exit(2)
	}
	raw, err := os.ReadFile(*fAct)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var wrap struct {
		Actions []action.Action `json:"actions"`
		Brain   string          `json:"brain"`
	}
	if err := json.Unmarshal(raw, &wrap); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if err := action.ValidateAll(wrap.Actions); err != nil {
		fmt.Fprintf(os.Stderr, "动作校验失败，整批拒绝: %v\n", err)
		os.Exit(1)
	}

	st, err := state.Open(*fState)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	round := st.Round()
	rn := exec.Runner{LitmusPath: *fLitmus, Findings: *fFindings, OutDir: *fOut, Timeout: 45 * time.Second, Templates: *fTpl}

	fmt.Printf("执行 %d 个动作（脑=%s，第 %d 轮）\n\n", len(wrap.Actions), wrap.Brain, round)
	bad := 0
	for i, a := range wrap.Actions {
		fmt.Printf("  %d) %-8s %-34s ... ", i+1, a.Kind, argBrief(a))
		res := rn.Do(ctx, a)
		fmt.Printf("%s  (%.1fs)\n", res.Verdict, res.Duration.Seconds())
		if res.Verdict == exec.VerdictError {
			bad++
		}
		st.Append(state.Record{
			Round: round, Brain: wrap.Brain, Kind: string(a.Kind),
			Signature: res.Signature, Why: a.Why,
			Verdict: string(res.Verdict), Detail: res.Detail,
		})
	}
	fmt.Printf("\n账本：%s\n", st.Path)
	if bad > 0 {
		fmt.Printf("其中 %d 个动作执行失败\n", bad)
	}
}

func cmdLoop(ctx context.Context, argv []string) {
	fs := flag.NewFlagSet("loop", flag.ExitOnError)
	fFindings := fs.String("f", "", "findings.json 路径")
	fLitmus := fs.String("litmus", "litmus", "litmus 可执行文件")
	fBrain := fs.String("brain", "rules", "决策器：rules / llm")
	fRounds := fs.Int("rounds", 4, "最多几轮")
	fTpl := fs.String("templates", "", "nuclei 模板目录（rerun 用，可选）")
	fState := fs.String("state", ".lance", "状态目录")
	fs.Parse(argv)

	if *fFindings == "" {
		fmt.Fprintln(os.Stderr, "缺 -f findings.json")
		os.Exit(2)
	}
	abs, _ := filepath.Abs(*fFindings)
	st, _ := state.Open(*fState)
	br, label := pickBrain(*fBrain)

	for r := 1; r <= *fRounds; r++ {
		ff, err := loadFindings(abs)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		hist, _ := st.History()
		acts, err := br.Decide(ctx, brain.Input{
			Findings: ff.Findings, Coverage: ff.Coverage, History: hist, Budget: 6,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "第 %d 轮决策失败: %v\n", r, err)
			return
		}
		fmt.Printf("\n═══ 第 %d 轮（脑=%s）═══\n", r, label)
		stopped := false
		rn := exec.Runner{LitmusPath: *fLitmus, Findings: abs, Timeout: 45 * time.Second,
			OutDir: filepath.Join(*fState, "runs"), Templates: *fTpl}
		for _, a := range acts {
			if a.Kind == action.KindStop {
				fmt.Printf("  收工：%s\n", a.Why)
				stopped = true
				break
			}
			fmt.Printf("  %-8s %-34s ... ", a.Kind, argBrief(a))
			res := rn.Do(ctx, a)
			fmt.Printf("%s\n", res.Verdict)
			st.Append(state.Record{
				Round: r, Brain: label, Kind: string(a.Kind),
				Signature: res.Signature, Why: a.Why,
				Verdict: string(res.Verdict), Detail: res.Detail,
			})
		}
		if stopped {
			return
		}
	}
	fmt.Println("\n达到轮次上限，停止。")
}

func cmdServe(ctx context.Context, argv []string) {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	fFindings := fs.String("f", "", "findings.json 路径")
	fLitmus := fs.String("litmus", "litmus", "litmus 可执行文件")
	fState := fs.String("state", ".lance", "状态目录")
	fPort := fs.Int("port", 8877, "监听端口（只绑 127.0.0.1）")
	fOpen := fs.Bool("open", true, "启动后自动打开浏览器")
	fs.Parse(argv)

	cfg := web.Config{Findings: *fFindings, Litmus: *fLitmus, StateDir: *fState}
	// 状态目录里存过 serve.json 就以它为准，命令行显式给了就覆盖
	if st, err := state.Open(*fState); err == nil {
		saved := filepath.Join(filepath.Dir(st.Path), "serve.json")
		if b, err := os.ReadFile(saved); err == nil {
			var prev web.Config
			if json.Unmarshal(b, &prev) == nil {
				if cfg.Findings == "" {
					cfg.Findings = prev.Findings
				}
				if cfg.Litmus == "litmus" && prev.Litmus != "" {
					cfg.Litmus = prev.Litmus
				}
			}
		}
	}
	if cfg.Findings != "" {
		abs, err := filepath.Abs(cfg.Findings)
		if err == nil {
			cfg.Findings = abs
		}
	}

	srv := web.New(cfg, version)
	addr := fmt.Sprintf("127.0.0.1:%d", *fPort)
	if err := srv.Run(ctx, addr, *fOpen); err != nil {
		fmt.Fprintf(os.Stderr, "服务启动失败: %v\n", err)
		os.Exit(1)
	}
}

// cmdBench 对同一份 findings 跑多个决策脑，落对比产物。
// 与 next/loop 的回落策略刻意不同：bench 不悄悄换脑，llm 未配置就明说
// unavailable —— 否则「对比报告」里两档其实是同一个脑，重合率 1.00 全是假象。
func cmdBench(ctx context.Context, argv []string) {
	fs := flag.NewFlagSet("bench", flag.ExitOnError)
	fFindings := fs.String("f", "", "findings.json 路径")
	fBudget := fs.Int("budget", 8, "本轮动作预算（两档共用）")
	fBrain := fs.String("brain", "rules,llm", "参与对比的决策脑，逗号分隔")
	fOut := fs.String("out", filepath.Join(".lance", "bench"), "产物目录")
	fs.Parse(argv)

	if *fFindings == "" {
		fmt.Fprintln(os.Stderr, "缺 -f findings.json")
		os.Exit(2)
	}
	names, err := bench.ParseBrains(*fBrain)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	rep, err := bench.Run(ctx, bench.Options{
		Findings: *fFindings,
		Budget:   *fBudget,
		Brains:   names,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	path, err := bench.Write(*fOut, rep)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Print(bench.Summary(rep))
	fmt.Printf("产物：%s\n", path)
}
