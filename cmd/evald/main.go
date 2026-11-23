// Command evald 是调研总纲 §6.1「私有冻结真实漏洞评测层」的独立评测服务。
//
// 它和主平台（cmd/lance）是解耦的：主平台只提交任务、读分数，不参与判定。
// 这样做的原因不是洁癖，是判据独立性——如果评测复用了被测系统的判定逻辑，
// 那测出来的就是「两边一致」，不是「攻击真的造成了后果」。
//
// 三种用法：
//
//	evald                     起 HTTP 服务（默认只监听回环）
//	evald -run-all            顺序跑完全部任务，打印三轴报告 + 批次汇总（自检/CI 用）
//	evald -write-freeze       按当前磁盘内容重建 eval/freeze.lock（改靶/建题后显式执行）
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Kur1sulab/lance/eval"
)

func main() {
	os.Exit(run())
}

func run() int {
	var (
		addr        = flag.String("addr", "127.0.0.1:8791", "评测服务监听地址")
		repoFlag    = flag.String("repo", "", "仓库根目录（默认从当前目录向上找含 go.mod 与 eval/ 的目录）")
		tasksFlag   = flag.String("tasks", "", "任务目录（默认 <repo>/eval/tasks）")
		lockFlag    = flag.String("lock", "", "冻结锁路径（默认 <repo>/eval/freeze.lock）")
		writeFreeze = flag.Bool("write-freeze", false, "重建 freeze.lock（显式换版本动作）")
		runAll      = flag.Bool("run-all", false, "顺序跑完全部任务并打印三轴报告")
		runOne      = flag.String("run", "", "只跑指定 task_id")
		maxConc     = flag.Int("max-concurrent", 2, "服务模式下同时跑的任务数上限")
		runTimeout  = flag.Duration("run-timeout", 10*time.Minute, "单题超时")
		token       = flag.String("token", os.Getenv("LANCE_EVAL_TOKEN"), "API token（远程监听时必填）")
		allowRemote = flag.Bool("allow-remote", false, "允许监听非回环地址（会起靶进程，慎用）")
		allowExec   = flag.Bool("allow-submitted-exec", false, "允许执行提交方现写的 exec 动作（默认只允许任务内置动作）")
	)
	flag.Parse()

	repoRoot, err := resolveRepoRoot(*repoFlag)
	if err != nil {
		fmt.Fprintf(os.Stderr, "定位仓库根失败: %v\n", err)
		return 1
	}
	taskDir := *tasksFlag
	if taskDir == "" {
		taskDir = filepath.Join(repoRoot, "eval", "tasks")
	}
	lockPath := *lockFlag
	if lockPath == "" {
		lockPath = filepath.Join(repoRoot, "eval", "freeze.lock")
	}

	tasks, err := eval.LoadTaskDir(taskDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "加载任务失败: %v\n", err)
		return 1
	}
	if len(tasks) == 0 {
		fmt.Fprintf(os.Stderr, "任务目录 %s 里没有任务\n", taskDir)
		return 1
	}

	// 重建锁：只在这里写 freeze.lock，其他任何时候都只读。
	if *writeFreeze {
		return doWriteFreeze(repoRoot, lockPath, tasks)
	}

	runner := eval.NewRunner(repoRoot)
	runner.LockPath = lockPath
	runner.AllowSubmittedExec = *allowExec
	runner.Log = os.Stderr

	switch {
	case *runAll:
		return doRunAll(runner, tasks)
	case *runOne != "":
		return doRunOne(runner, tasks, *runOne)
	}

	// 服务模式：非回环监听必须同时满足 -allow-remote 与 token，缺一不可。
	host, _, err := net.SplitHostPort(*addr)
	if err != nil {
		fmt.Fprintf(os.Stderr, "地址 %q 不合法: %v\n", *addr, err)
		return 1
	}
	if !isLoopback(host) {
		if !*allowRemote {
			fmt.Fprintf(os.Stderr, "拒绝监听 %q：评测服务会起靶进程，默认只允许回环；确认要远程用请加 -allow-remote\n", *addr)
			return 1
		}
		if *token == "" {
			fmt.Fprintln(os.Stderr, "远程监听必须设置 token（-token 或 LANCE_EVAL_TOKEN）")
			return 1
		}
	}

	srv, err := eval.NewServer(runner, tasks, eval.ServerOptions{
		MaxConcurrent: *maxConc, Token: *token, RunTimeout: *runTimeout,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "启动评测服务失败: %v\n", err)
		return 1
	}
	httpSrv := &http.Server{Addr: *addr, Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- httpSrv.ListenAndServe() }()
	fmt.Printf("[evald] 评测服务已启动 addr=%s tasks=%d token=%v\n", *addr, len(tasks), *token != "")
	for _, t := range tasks {
		fmt.Printf("[evald]   - %-22s layer=%d scoring=%s\n", t.TaskID, t.Layer, t.Scoring.Mode)
	}

	select {
	case err := <-errCh:
		if err != nil && err.Error() != "http: Server closed" {
			fmt.Fprintf(os.Stderr, "服务退出: %v\n", err)
			return 1
		}
	case <-ctx.Done():
		fmt.Println("\n[evald] 收到退出信号，正在关闭…")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}
	return 0
}

// resolveRepoRoot 找仓库根：显式参数优先，否则从 cwd 向上找同时有 go.mod 与 eval/ 的目录。
func resolveRepoRoot(explicit string) (string, error) {
	if explicit != "" {
		return filepath.Abs(explicit)
	}
	dir, err := os.Getwd()
	if err != nil {
		return "", err
	}
	for {
		if fileExists(filepath.Join(dir, "go.mod")) && dirExists(filepath.Join(dir, "eval")) {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("从当前目录向上没有找到同时含 go.mod 与 eval/ 的目录（用 -repo 指定）")
		}
		dir = parent
	}
}

func doWriteFreeze(repoRoot, lockPath string, tasks []*eval.Task) int {
	// 先读旧锁，用来打印「哪些靶换了版本」——换版本是重大动作，必须肉眼可见。
	old, _, _ := eval.LoadFreezeLock(lockPath)
	lock, err := eval.BuildFreezeLock(repoRoot, tasks)
	if err != nil {
		fmt.Fprintf(os.Stderr, "构建冻结锁失败: %v\n", err)
		return 1
	}
	if err := eval.WriteFreezeLock(lockPath, lock); err != nil {
		fmt.Fprintf(os.Stderr, "写冻结锁失败: %v\n", err)
		return 1
	}
	changed := 0
	for _, t := range tasks {
		entry := lock.Entries[t.TaskID]
		status := "new"
		if old != nil {
			if prev, ok := old.Entries[t.TaskID]; ok {
				switch {
				case prev.TaskSHA256 == entry.TaskSHA256 && sameArtifacts(prev.Artifacts, entry.Artifacts) && prev.Ref == entry.Ref:
					status = "unchanged"
				default:
					status = "changed"
					changed++
				}
			} else {
				changed++
			}
		}
		fmt.Printf("[freeze] %-22s %-10s ref=%-26s artifacts=%d task=%s\n",
			t.TaskID, status, entry.Ref, len(entry.Artifacts), entry.TaskSHA256[:12])
	}
	fmt.Printf("[freeze] 已写入 %s（%d 个任务，%d 个变化）\n", lockPath, len(tasks), changed)
	return 0
}

func sameArtifacts(a, b []eval.FreezeArtifact) bool {
	if len(a) != len(b) {
		return false
	}
	m := map[string]string{}
	for _, x := range a {
		m[x.Path] = x.SHA256
	}
	for _, y := range b {
		if m[y.Path] != y.SHA256 {
			return false
		}
	}
	return true
}

func doRunOne(r *eval.Runner, tasks []*eval.Task, id string) int {
	for _, t := range tasks {
		if t.TaskID != id {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		sc, err := r.Run(ctx, t, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "运行失败: %v\n", err)
			return 1
		}
		sc.WriteText(os.Stdout)
		out, _ := json.MarshalIndent(sc, "", "  ")
		fmt.Println(string(out))
		return 0
	}
	fmt.Fprintf(os.Stderr, "任务 %q 未找到\n", id)
	return 1
}

// doRunAll 是自检入口：顺序跑全部任务，逐题打三轴报告，最后打批次汇总。
// 退出码有意义：1=运行失败（基础设施），2=judge 标定不一致（判据有 bug）。
func doRunAll(r *eval.Runner, tasks []*eval.Task) int {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	var cards []*eval.Scorecard
	calibBad := false
	for _, t := range tasks {
		sc, err := r.Run(ctx, t, nil)
		if err != nil {
			fmt.Fprintf(os.Stderr, "\n[eval] 任务 %s 运行失败（不产生分数）: %v\n", t.TaskID, err)
			return 1
		}
		sc.WriteText(os.Stdout)
		cards = append(cards, sc)
		if sc.Calibration != nil && !sc.Calibration.Matched {
			calibBad = true
		}
	}
	batch := eval.Aggregate(cards)
	fmt.Printf("\n===== 批次汇总 =====\n%s\n", batch.Line())
	out, _ := json.MarshalIndent(batch, "", "  ")
	fmt.Println(string(out))

	// 自检断言：夹具题的正例必须 achieved、反例必须 not_achieved。
	for _, sc := range cards {
		if sc.Calibration == nil {
			continue
		}
		if !sc.Calibration.Matched {
			fmt.Fprintf(os.Stderr, "[FATAL] %s 标定不一致：预期 %v 实际 %v\n", sc.TaskID, *sc.Calibration.Expected, sc.Axes.Outcome.Achieved)
			return 2
		}
	}
	if calibBad {
		return 2
	}
	return 0
}

func isLoopback(host string) bool {
	if host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && !st.IsDir()
}

func dirExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}
