package eval

// Runner：把一道题从「冻结校验」跑到「scorecard 落盘」。
//
// 执行顺序是刻意的，每一步失败都必须停在原地而不是继续给分：
//   1. 校验 freeze.lock            —— 靶或题面变了就不许跑（防污染）
//   2. 分配端口 / 建运行目录        —— 每次运行独立，互不串台
//   3. 铸造 canary                 —— 判据用的哨兵现场生成，靶内没有静态答案
//   4. 打开回调监听器               —— 必须在攻击之前，攻击动作要往里连
//   5. 构建 + 起靶 + 等就绪         —— 起不来是基础设施错误，不是「打不穿」
//   6. 重放被测动作                 —— 留下真实响应作为审计证据
//   7. 跑 judge probe              —— 只看环境事实
//   8. 停靶 + 定 verdict + 落盘     —— 三轴分开记
//
// 第 5 步与第 7 步的区别是这一层的可信度来源：起不来 → 返回 error，不产生分数；
// 打不穿 → 正常产出 achieved=false 的 scorecard。两者混在一起，评测结论就废了。

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// 运行期默认值。
const (
	defaultReadyTimeout  = 30 * time.Second
	defaultStopTimeout   = 5 * time.Second
	defaultActionTimeout = 30 * time.Second
	maxActionOutput      = 8 << 10
)

// Runner 执行评测任务。零值不可用，用 NewRunner 构造。
type Runner struct {
	RepoRoot string
	EvalDir  string
	LockPath string
	Log      io.Writer

	// AllowSubmittedExec 控制「提交上来的动作」能否执行任意命令。
	// 默认 false：动作清单来自被测 agent 时，允许 exec 等价于把评测机交给对方。
	// 真实 CVE 题里确实需要跑 PoC 二进制，那就由任务内置的动作清单（经代码评审）提供，
	// 而不是由提交方在请求体里现写。
	AllowSubmittedExec bool

	// AllowedReadRoots 是 file_read 允许读取的根目录，默认 [运行目录, 仓库根]。
	AllowedReadRoots []string
}

// NewRunner 构造 Runner，路径全部按仓库根推导。
func NewRunner(repoRoot string) *Runner {
	evalDir := filepath.Join(repoRoot, "eval")
	return &Runner{
		RepoRoot:         repoRoot,
		EvalDir:          evalDir,
		LockPath:         filepath.Join(evalDir, "freeze.lock"),
		Log:              os.Stderr,
		AllowedReadRoots: []string{evalDir, repoRoot},
	}
}

// Submission 是一次评测提交。MVP 里 Actions 为空时用任务内置 PoC；
// 接上真实 agent 后，这里放它实际执行过的动作，由评测方重放。
type Submission struct {
	TaskID  string   `json:"task_id"`
	Actions []Action `json:"actions,omitempty"`
	Note    string   `json:"note,omitempty"`
}

// Run 执行一道题。返回 error 表示「没能完成评测」（冻结失败、靶起不来、构建失败），
// 此时不产生分数；打不穿不是 error，是 verdict=not_achieved 的合法结果。
func (r *Runner) Run(ctx context.Context, task *Task, sub *Submission) (*Scorecard, error) {
	if r.Log == nil {
		r.Log = io.Discard
	}
	runID := newRunID(task.TaskID)
	runDir := filepath.Join(r.EvalDir, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		return nil, fmt.Errorf("创建运行目录失败: %w", err)
	}

	// 1. 冻结校验：不一致直接拒绝，绝不允许「带 drift 跑」。
	lock, lockRaw, err := LoadFreezeLock(r.LockPath)
	if err != nil {
		return nil, err
	}
	freezeStatus, err := VerifyFreeze(r.RepoRoot, task, lock, lockRaw)
	if err != nil {
		return nil, err
	}
	r.logf("run=%s task=%s layer=%d 冻结校验通过 kind=%s ref=%s lock=%s artifacts=%d",
		runID, task.TaskID, task.Layer, freezeStatus.Kind, freezeStatus.Ref, short(freezeStatus.LockSHA256), freezeStatus.Artifacts)

	sc := NewScorecard(task, runID)
	sc.Freeze = *freezeStatus
	sc.RunDir = runDir
	sc.Target.Kind = task.Target.Kind

	v := newVars()
	v.set("run_id", runID)
	v.set("run_dir", runDir)
	v.set("repo_root", r.RepoRoot)
	v.set("eval_dir", r.EvalDir)
	targetData := filepath.Join(runDir, "data")
	v.set("target_data", targetData)

	// 2. 端口与运行目录。
	port, err := freePort()
	if err != nil {
		return nil, fmt.Errorf("分配靶端口失败: %w", err)
	}
	base := fmt.Sprintf("http://127.0.0.1:%d", port)
	v.set("port", fmt.Sprintf("%d", port))
	v.set("target_base", base)
	v.set("bin", filepath.Join(runDir, "target-bin"))
	sc.Target.BaseURL = base

	// 3. 铸造 canary（必须在任何 probe/attack 解析之前，占位符要能取到值）。
	if err := r.mintCanaries(task, v, runDir); err != nil {
		return nil, err
	}

	// 4. 打开回调监听器（必须在攻击之前）。
	hub := newCallbackHub()
	defer hub.closeAll()
	if err := r.openCallbacks(task, v, hub); err != nil {
		return nil, err
	}

	// 5. 构建 + 起靶。
	var proc *targetProc
	if task.Target.Kind == "exec" {
		if err := r.buildTarget(ctx, task, v, runDir); err != nil {
			return nil, err
		}
		proc, err = r.startTarget(ctx, task, v, runDir)
		if err != nil {
			return nil, err
		}
		// 无论后面怎么退出，都要保证靶进程被收尸。
		defer proc.stop(defaultStopTimeout)
		sc.Target.Cmd = proc.cmdline
		sc.Target.LogPath = proc.logPath
	}
	readyStart := time.Now()
	if err := r.waitReady(ctx, task, v, proc, runDir); err != nil {
		return nil, err
	}
	sc.Target.ReadyMS = time.Since(readyStart).Milliseconds()
	r.logf("run=%s 靶就绪 base=%s ready_in=%dms", runID, base, sc.Target.ReadyMS)

	// 6. 重放被测动作。
	actions := task.Attack
	if sub != nil && len(sub.Actions) > 0 {
		actions = sub.Actions
	}
	for _, a := range actions {
		if a.Kind == ActionExec && sub != nil && len(sub.Actions) > 0 && !r.AllowSubmittedExec {
			return nil, fmt.Errorf("拒绝执行提交方的 exec 动作 %s：评测机不允许运行未评审命令（需 -allow-submitted-exec 显式开启）", a.ID)
		}
		res := r.runAction(ctx, a, v)
		sc.Attack = append(sc.Attack, res)
		if res.Error != "" {
			r.logf("run=%s action=%s %s -> 错误: %s", runID, res.ID, res.Request, res.Error)
		} else {
			r.logf("run=%s action=%s %s -> %d (%dB)", runID, res.ID, res.Request, res.Status, res.RespBytes)
		}
	}

	// 7. 判据：objective + milestone 一起跑，同 id 只跑一次。
	// 运行目录总是可读根：canary、靶日志、审计日志都在里面。
	readRoots := append([]string{runDir}, r.AllowedReadRoots...)
	je := &judgeEnv{vars: v, http: newEvalHTTPClient(), callbacks: hub, readRoots: readRoots}
	probeOutcomes := map[string]ProbeOutcome{}
	for _, p := range collectProbes(task) {
		out := je.evaluate(ctx, p)
		probeOutcomes[p.ID] = out
		verdict := "miss"
		if out.Hit {
			verdict = "hit"
		}
		if out.Error != "" {
			verdict = "error"
		}
		r.logf("run=%s probe=%s kind=%s %s: %s", runID, p.ID, p.Kind, verdict, out.Reason)
	}

	// 8. 停靶前先看它是否还活着：靶中途退出会让「没打穿」变得不可信。
	if proc != nil {
		if exited, exitErr := proc.poll(); exited {
			code := proc.exitCode()
			sc.Target.ExitCode = code
			sc.Target.AliveAfterAttack = false
			r.logf("run=%s 靶在评测过程中退出 code=%v err=%v", runID, code, exitErr)
		} else {
			sc.Target.AliveAfterAttack = true
		}
	} else {
		sc.Target.AliveAfterAttack = r.dialReady(ctx, task, v) == nil
	}

	sc.Finalize(task, probeOutcomes)
	// 靶死了又没打穿：不能记成 not_achieved，那是把基础设施故障算成能力不足。
	if !sc.Target.AliveAfterAttack && sc.Verdict == VerdictNotAchieved {
		sc.Verdict = VerdictUndetermined
		sc.Notes = append(sc.Notes, "靶在评测过程中退出且后果未达成：无法区分「打不穿」与「靶崩了」，本次不计入能力统计")
	}

	// 9. 落盘。scorecard 与证据都留在运行目录，便于人工复核。
	scorePath, evidencePath, err := sc.WriteFiles()
	if err != nil {
		return nil, fmt.Errorf("写 scorecard 失败: %w", err)
	}
	r.logf("run=%s %s", runID, sc.Line())
	r.logf("run=%s scorecard=%s evidence=%s target_log=%s", runID, scorePath, evidencePath, sc.Target.LogPath)
	return sc, nil
}

// collectProbes 汇总 objective 与 milestones 的 probe（Validate 已保证 id 不重复）。
func collectProbes(t *Task) []Probe {
	seen := map[string]bool{}
	var out []Probe
	add := func(p Probe) {
		if seen[p.ID] {
			return
		}
		seen[p.ID] = true
		out = append(out, p)
	}
	for _, p := range t.Objective.Probes {
		add(p)
	}
	for _, m := range t.Milestones {
		add(m.Probe)
	}
	return out
}

// openCallbacks 为每个 tcp_connect probe 开一个监听器，并把地址写进变量表。
func (r *Runner) openCallbacks(task *Task, v *vars, hub *callbackHub) error {
	for _, p := range collectProbes(task) {
		if p.Kind != ProbeTCPConnect {
			continue
		}
		banner, err := v.resolve(p.Banner)
		if err != nil {
			return fmt.Errorf("probe %s 的 banner 解析失败: %w", p.ID, err)
		}
		credBanner, err := v.resolve(p.CredentialBanner)
		if err != nil {
			return fmt.Errorf("probe %s 的 credential_banner 解析失败: %w", p.ID, err)
		}
		credOn, err := v.resolve(p.CredentialOn)
		if err != nil {
			return fmt.Errorf("probe %s 的 credential_on 解析失败: %w", p.ID, err)
		}
		listen, err := v.resolve(p.Listen)
		if err != nil {
			return fmt.Errorf("probe %s 的 listen 解析失败: %w", p.ID, err)
		}
		l, err := hub.open(p.ID, listen, banner, credBanner, credOn)
		if err != nil {
			return fmt.Errorf("probe %s: %w", p.ID, err)
		}
		v.set("callback:"+p.ID, l.addr)
		v.set("callback_url:"+p.ID, "http://"+l.addr)
		r.logf("回调监听器 probe=%s 监听 %s（banner %d 字节）", p.ID, l.addr, len(banner))
	}
	return nil
}

// mintCanaries 现场铸造哨兵。file 型必须落在本次运行目录内：
// 评测不该往仓库里写东西，也不该让「上一个靶的哨兵」留到下一个靶。
func (r *Runner) mintCanaries(task *Task, v *vars, runDir string) error {
	for _, c := range task.Canaries {
		val := c.Prefix + randomToken(16)
		v.set("canary:"+c.ID, val)
		if c.Kind != "file" {
			r.logf("canary=%s 已铸造（inline，不落盘，只能靠目标出站拿到）", c.ID)
			continue
		}
		p, err := v.resolve(c.Path)
		if err != nil {
			return fmt.Errorf("canary %s 的 path 解析失败: %w", c.ID, err)
		}
		abs, err := ensureInside(runDir, p)
		if err != nil {
			return fmt.Errorf("canary %s 的 path 必须在本次运行目录 %s 内: %w", c.ID, runDir, err)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return fmt.Errorf("canary %s 建目录失败: %w", c.ID, err)
		}
		if err := os.WriteFile(abs, []byte(val+"\n"), 0o644); err != nil {
			return fmt.Errorf("canary %s 落盘失败: %w", c.ID, err)
		}
		v.set("canary_path:"+c.ID, abs)
		r.logf("canary=%s 已铸造于 %s（file，webroot 之外，只有真穿越才读得到）", c.ID, abs)
	}
	return nil
}

// buildTarget 执行可选的构建步骤（把 go run 换成 go build，便于精确收尸）。
func (r *Runner) buildTarget(ctx context.Context, task *Task, v *vars, runDir string) error {
	if len(task.Target.Build) == 0 {
		return nil
	}
	cmdline, err := v.resolveAll(task.Target.Build)
	if err != nil {
		return fmt.Errorf("target.build 占位符解析失败: %w", err)
	}
	workdir, err := r.resolveWorkdir(task, v)
	if err != nil {
		return err
	}
	cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	c := exec.CommandContext(cctx, cmdline[0], cmdline[1:]...)
	c.Dir = workdir
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	r.logf("构建靶: %s（cwd=%s）", strings.Join(cmdline, " "), workdir)
	if err := c.Run(); err != nil {
		return fmt.Errorf("构建靶失败: %w\n%s", err, tail(buf.String(), 2000))
	}
	return nil
}

// startTarget 起靶进程并接管日志。进程组隔离在 proc_*.go 里按平台实现。
func (r *Runner) startTarget(ctx context.Context, task *Task, v *vars, runDir string) (*targetProc, error) {
	cmdline, err := v.resolveAll(task.Target.Cmd)
	if err != nil {
		return nil, fmt.Errorf("target.cmd 占位符解析失败: %w", err)
	}
	workdir, err := r.resolveWorkdir(task, v)
	if err != nil {
		return nil, err
	}
	env := map[string]string{"LANCE_EVAL_RUN_ID": v.m["run_id"]}
	for k, val := range task.Target.Env {
		rv, err := v.resolve(val)
		if err != nil {
			return nil, fmt.Errorf("target.env[%s] 占位符解析失败: %w", k, err)
		}
		env[k] = rv
	}
	logPath := filepath.Join(runDir, "target.log")
	proc, err := startProcess(ctx, cmdline, workdir, env, logPath)
	if err != nil {
		return nil, fmt.Errorf("起靶失败: %w", err)
	}
	r.logf("靶进程已启动 pid=%d cmd=%s log=%s", proc.pid(), strings.Join(cmdline, " "), logPath)
	return proc, nil
}

func (r *Runner) resolveWorkdir(task *Task, v *vars) (string, error) {
	if task.Target.Workdir == "" {
		return r.RepoRoot, nil
	}
	wd, err := v.resolve(task.Target.Workdir)
	if err != nil {
		return "", fmt.Errorf("target.workdir 占位符解析失败: %w", err)
	}
	if !filepath.IsAbs(wd) {
		wd = filepath.Join(r.RepoRoot, wd)
	}
	return wd, nil
}

// waitReady 等到靶可用。失败一律返回 error：起不来不是「打不穿」。
func (r *Runner) waitReady(ctx context.Context, task *Task, v *vars, proc *targetProc, runDir string) error {
	timeout := defaultReadyTimeout
	if task.Target.Ready.TimeoutMS > 0 {
		timeout = time.Duration(task.Target.Ready.TimeoutMS) * time.Millisecond
	}
	deadline := time.Now().Add(timeout)
	for {
		if err := r.dialReady(ctx, task, v); err == nil {
			return nil
		} else if time.Now().After(deadline) {
			detail := ""
			if proc != nil {
				detail = "\n靶日志尾部:\n" + tail(readFileString(proc.logPath), 2000)
			}
			return fmt.Errorf("靶在 %v 内未就绪: %w%s", timeout, err, detail)
		}
		if proc != nil {
			if exited, err := proc.poll(); exited {
				return fmt.Errorf("靶进程在就绪前退出（%v）\n靶日志尾部:\n%s", err, tail(readFileString(proc.logPath), 2000))
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// dialReady 做一次就绪探测。
func (r *Runner) dialReady(ctx context.Context, task *Task, v *vars) error {
	switch task.Target.Ready.Kind {
	case "tcp_dial":
		addr, err := v.resolve(task.Target.Ready.Addr)
		if err != nil {
			return err
		}
		c, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err != nil {
			return err
		}
		return c.Close()
	case "http":
		url, err := v.resolve(task.Target.Ready.URL)
		if err != nil {
			return err
		}
		cctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(cctx, http.MethodGet, url, nil)
		if err != nil {
			return err
		}
		resp, err := newEvalHTTPClient().Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
		// 5xx 视为未就绪：服务起来了但依赖没初始化完，这时候判分没有意义。
		if resp.StatusCode >= 500 {
			return fmt.Errorf("就绪探测返回 %d", resp.StatusCode)
		}
		return nil
	default:
		return fmt.Errorf("未知就绪探测类型 %q", task.Target.Ready.Kind)
	}
}

// runAction 重放一条被测动作，留下真实响应。
func (r *Runner) runAction(ctx context.Context, a Action, v *vars) ActionResult {
	start := time.Now()
	res := ActionResult{ID: a.ID, Kind: a.Kind}
	defer func() { res.ElapsedMS = time.Since(start).Milliseconds() }()

	switch a.Kind {
	case ActionHTTP:
		url, err := v.resolve(a.URL)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		method := a.Method
		if method == "" {
			method = http.MethodGet
		}
		body, err := v.resolve(a.Body)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		res.Request = method + " " + url
		timeout := defaultActionTimeout
		if a.TimeoutMS > 0 {
			timeout = time.Duration(a.TimeoutMS) * time.Millisecond
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		var rdr io.Reader
		if body != "" {
			rdr = strings.NewReader(body)
		}
		req, err := http.NewRequestWithContext(cctx, method, url, rdr)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		for k, val := range a.Headers {
			rv, err := v.resolve(val)
			if err != nil {
				res.Error = err.Error()
				return res
			}
			req.Header.Set(k, rv)
		}
		resp, err := newEvalHTTPClient().Do(req)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, MaxProbeBodyRead))
		res.Status = resp.StatusCode
		res.RespBytes = len(raw)
		res.RespSHA256 = sha256Hex(raw)
		res.Preview = preview(raw)
		return res
	case ActionExec:
		cmdline, err := v.resolveAll(a.Command)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		res.Request = strings.Join(cmdline, " ")
		timeout := defaultActionTimeout
		if a.TimeoutMS > 0 {
			timeout = time.Duration(a.TimeoutMS) * time.Millisecond
		}
		cctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		c := exec.CommandContext(cctx, cmdline[0], cmdline[1:]...)
		c.Dir = r.RepoRoot
		var buf limitedBuffer
		buf.limit = maxActionOutput
		c.Stdout = &buf
		c.Stderr = &buf
		err = c.Run()
		res.Output = buf.String()
		if err != nil {
			res.Error = err.Error()
			if ee, ok := err.(*exec.ExitError); ok {
				res.Status = ee.ExitCode()
			}
		}
		return res
	default:
		res.Error = fmt.Sprintf("未知动作类型 %q", a.Kind)
		return res
	}
}

func (r *Runner) logf(format string, args ...any) {
	if r.Log == nil {
		return
	}
	fmt.Fprintf(r.Log, "[eval] "+format+"\n", args...)
}

// ---- 进程管理 ----

// targetProc 是靶进程的运行期句柄。
type targetProc struct {
	cmd     *exec.Cmd
	cmdline []string
	logFile *os.File
	logPath string

	exited  chan error
	mu      sync.Mutex
	done    bool
	waitErr error
	stopped bool
}

func startProcess(ctx context.Context, cmdline []string, workdir string, env map[string]string, logPath string) (*targetProc, error) {
	f, err := os.Create(logPath)
	if err != nil {
		return nil, fmt.Errorf("创建靶日志失败: %w", err)
	}
	c := exec.Command(cmdline[0], cmdline[1:]...)
	c.Dir = workdir
	c.Stdout = f
	c.Stderr = f
	c.Env = os.Environ()
	for k, v := range env {
		c.Env = append(c.Env, k+"="+v)
	}
	// 独立进程组：停靶时能连子进程一起收掉，不留孤儿抢下一道题的端口。
	setProcAttr(c)
	if err := c.Start(); err != nil {
		f.Close()
		return nil, err
	}
	p := &targetProc{cmd: c, cmdline: cmdline, logFile: f, logPath: logPath, exited: make(chan error, 1)}
	go func() { p.exited <- c.Wait() }()
	// 上层 ctx 取消时补一刀，避免评测中断后靶还挂着。
	go func() {
		<-ctx.Done()
		p.kill()
	}()
	return p, nil
}

func (p *targetProc) pid() int {
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// poll 非阻塞地收尸。
func (p *targetProc) poll() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.done {
		return true, p.waitErr
	}
	select {
	case err := <-p.exited:
		p.done = true
		p.waitErr = err
		return true, err
	default:
		return false, nil
	}
}

// waitExit 在超时内等进程退出。
func (p *targetProc) waitExit(d time.Duration) bool {
	deadline := time.Now().Add(d)
	for {
		if exited, _ := p.poll(); exited {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (p *targetProc) exitCode() *int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.done || p.cmd.ProcessState == nil {
		return nil
	}
	code := p.cmd.ProcessState.ExitCode()
	return &code
}

// stop 先 SIGTERM（给靶刷日志的机会），超时再 SIGKILL 整个进程组。
func (p *targetProc) stop(timeout time.Duration) {
	p.mu.Lock()
	if p.stopped {
		p.mu.Unlock()
		return
	}
	p.stopped = true
	p.mu.Unlock()

	if exited, _ := p.poll(); exited {
		p.closeLog()
		return
	}
	_ = signalTerm(p.cmd)
	if !p.waitExit(timeout) {
		p.kill()
		p.waitExit(2 * time.Second)
	}
	p.closeLog()
}

func (p *targetProc) kill() {
	if exited, _ := p.poll(); exited {
		return
	}
	_ = signalKill(p.cmd)
}

func (p *targetProc) closeLog() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.logFile != nil {
		_ = p.logFile.Close()
		p.logFile = nil
	}
}

// ---- 小工具 ----

// limitedBuffer 是带上限的写入缓冲，防止 exec 动作把评测机内存打满。
type limitedBuffer struct {
	buf   bytes.Buffer
	limit int
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remain := b.limit - b.buf.Len()
	if remain > 0 {
		if len(p) > remain {
			b.buf.Write(p[:remain])
		} else {
			b.buf.Write(p)
		}
	}
	return len(p), nil
}

func (b *limitedBuffer) String() string { return b.buf.String() }

func newRunID(taskID string) string {
	return time.Now().Format("20060102T150405") + "-" + taskID + "-" + randomToken(3)
}

func randomToken(n int) string {
	buf := make([]byte, n)
	if _, err := rand.Read(buf); err != nil {
		// crypto/rand 失败说明系统熵源坏了，此时任何评测结论都不可信。
		panic("crypto/rand 不可用: " + err.Error())
	}
	return hex.EncodeToString(buf)
}

func sha256Hex(raw []byte) string {
	return hexDigest(raw)
}

func freePort() (int, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port, nil
}

func readFileString(path string) string {
	if path == "" {
		return ""
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(raw)
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return "…" + s[len(s)-n:]
}
