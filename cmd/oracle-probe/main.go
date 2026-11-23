// Command oracle-probe 把 internal/oracle 的对照探测跑成一份可直接贴报告的结论。
//
// 用法：
//
//	oracle-probe -req req.json                 # 人读摘要
//	oracle-probe -req req.json -json           # 机器读（报告归档用）
//	oracle-probe -req req.json -interval 2s    # 放慢，目标有风控时用
//
// req.json 形如：
//
//	{
//	  "known":  "test_account",          // 你自己注册/确认存在的账号
//	  "absent": "zzz_nobody_4f21",       // 格式合法但确认不存在
//	  "probes": [
//	    {"name":"login","method":"POST","url":"https://TARGET/login",
//	     "body":"username={{account}}&pass=x","encode":"form"},
//	    {"name":"forgot","method":"POST","url":"https://TARGET/forgot",
//	     "body":"{\"uid\":\"{{account}}\"}","encode":"json"}
//	  ]
//	}
//
// 退出码：0 = 跑完（无论结论是什么）；2 = 参数/请求则错；3 = 探测过程出错。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/Kur1sulab/lance/internal/oracle"
)

func main() { os.Exit(run()) }

func run() int {
	reqPath := flag.String("req", "", "请求 JSON 文件（必填）")
	asJSON := flag.Bool("json", false, "输出完整 JSON 报告")
	interval := flag.Duration("interval", time.Second, "两次请求的最小间隔（默认 1s，有风控时调大）")
	repeats := flag.Int("repeats", 3, "每组基线重复次数（≥2 才能算抖动）")
	timeout := flag.Duration("timeout", 10*time.Second, "单次请求超时")
	insecure := flag.Bool("insecure", false, "跳过 TLS 校验（自签靶场）")
	flag.Parse()

	if *reqPath == "" {
		fmt.Fprintln(os.Stderr, "缺 -req：需要一份探测请求 JSON")
		return 2
	}
	raw, err := os.ReadFile(*reqPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, "读请求文件失败:", err)
		return 2
	}
	var req oracle.Request
	if err := json.Unmarshal(raw, &req); err != nil {
		fmt.Fprintln(os.Stderr, "请求文件不是合法 JSON:", err)
		return 2
	}

	r := oracle.New(oracle.Options{
		Timeout:  *timeout,
		Interval: *interval,
		Repeats:  *repeats,
		Insecure: *insecure,
	})
	rep, err := r.Run(context.Background(), req)
	if err != nil {
		fmt.Fprintln(os.Stderr, "探测失败:", err)
		return 3
	}

	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.SetEscapeHTML(false)
		if err := enc.Encode(rep); err != nil {
			fmt.Fprintln(os.Stderr, "输出失败:", err)
			return 3
		}
		return 0
	}
	printHuman(rep)
	return 0
}

func printHuman(rep *oracle.Report) {
	fmt.Printf("对照探测：已知 %s / 不存在 %s\n", rep.Known, rep.Absent)
	fmt.Printf("耗时 %s\n\n", rep.Elapsed.Round(time.Millisecond))
	for _, v := range rep.Verdicts {
		mark := "·"
		if v.Enumerable {
			mark = "!"
		}
		fmt.Printf("%s %-10s 判定=%-10s 置信度=%.2f\n", mark, v.Probe, v.State, v.Confidence)
		if v.Baseline.N > 0 {
			fmt.Printf("    基线：%d 次，长度均 %.0f（σ %.1f），耗时均 %s，稳定=%v\n",
				v.Baseline.N, v.Baseline.LenMean, v.Baseline.LenStd,
				v.Baseline.LatMean.Round(time.Millisecond), v.Baseline.Stable)
		}
		for _, s := range v.Signals {
			fmt.Printf("    [%s] %s\n", s.Kind, s.Detail)
		}
		if v.Note != "" {
			fmt.Printf("    → %s\n", v.Note)
		}
		fmt.Println()
	}
	if rep.Enumerable {
		fmt.Println("结论：至少一个入口能区分账号存在性 —— 构成用户枚举，可写进报告。")
	} else {
		fmt.Println("结论：未发现可判别 oracle。注意这只说明「本次探测没测出来」，不等于「不存在」。")
	}
	for _, n := range rep.Notes {
		fmt.Println("  备注:", n)
	}
}
