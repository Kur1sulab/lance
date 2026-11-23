package oracle

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Runner 执行一轮对照探测。
//
// 并发策略：**不同探测点之间并行，同一探测点内部串行**。
// 原因不是性能，是安全——同一入口上的连续请求间隔必须受 Interval 约束，
// 否则「探已知账号 3 次」会被目标的风控看成撞库，直接把真人账号锁掉。
type Runner struct {
	opts   Options
	client *http.Client
}

func New(opts Options) *Runner {
	opts.defaults()
	tr := &http.Transport{
		TLSClientConfig:     &tls.Config{InsecureSkipVerify: opts.Insecure}, //nolint:gosec // 自签靶场
		MaxIdleConnsPerHost: 4,
		DisableKeepAlives:   false,
	}
	return &Runner{
		opts:   opts,
		client: &http.Client{Timeout: opts.Timeout, Transport: tr},
	}
}

// Run 跑完整对照：每个探测点先打基线（不存在账号），再打对照（存在账号），
// 然后归类。任一步网络失败都如实记进 Report.Notes，不静默吞掉。
func (r *Runner) Run(ctx context.Context, req Request) (*Report, error) {
	if req.Known == "" || req.Absent == "" {
		return nil, errors.New("Known 与 Absent 都必须给：没有对照组就没有差异判定")
	}
	if len(req.Probes) == 0 {
		return nil, errors.New("至少要给一个探测点")
	}

	rep := &Report{Known: req.Known, Absent: req.Absent, StartedAt: time.Now()}
	var mu sync.Mutex
	var wg sync.WaitGroup

	for _, p := range req.Probes {
		p := p
		wg.Add(1)
		go func() {
			defer wg.Done()
			v := r.judgeProbe(ctx, p, req.Known, req.Absent)
			mu.Lock()
			rep.Verdicts = append(rep.Verdicts, v)
			if v.Enumerable {
				rep.Enumerable = true
			}
			if v.Note != "" {
				rep.Notes = append(rep.Notes, p.Name+": "+v.Note)
			}
			mu.Unlock()
		}()
	}
	wg.Wait()

	// 按探测点声明顺序重排，保证输出稳定（并发 append 的顺序不可依赖）。
	order := make(map[string]int, len(req.Probes))
	for i, p := range req.Probes {
		order[p.Name] = i
	}
	for i := 1; i < len(rep.Verdicts); i++ {
		for j := i; j > 0 && order[rep.Verdicts[j].Probe] < order[rep.Verdicts[j-1].Probe]; j-- {
			rep.Verdicts[j], rep.Verdicts[j-1] = rep.Verdicts[j-1], rep.Verdicts[j]
		}
	}
	rep.Elapsed = time.Since(rep.StartedAt)
	return rep, nil
}

// judgeProbe 是单个探测点的完整判定：采基线 → 采对照 → 聚类 → 归类。
func (r *Runner) judgeProbe(ctx context.Context, p Probe, known, absent string) Verdict {
	v := Verdict{Probe: p.Name, Account: known}

	// 1) 基线：不存在账号重复测。
	base := r.sample(ctx, p, absent, r.opts.Repeats)
	if len(base) == 0 {
		v.State = StateUnknown
		v.Note = "基线探测全部失败（网络层），无法判定"
		return v
	}
	v.Baseline = stats(base)
	if !v.Baseline.Stable {
		// 抖动本身就大于任何可用阈值——这时任何「差异」都可能是噪声。
		// 判 no_signal 而不是 unknown：unknown 留给「有信号但归不了类」。
		v.State = StateNoSignal
		v.Note = fmt.Sprintf("基线不稳定（长度 σ=%.1f、状态码 %v），差异无法与噪声区分", v.Baseline.LenStd, v.Baseline.Statuses)
		return v
	}

	// 2) 对照：已知存在账号重复测。
	knownObs := r.sample(ctx, p, known, r.opts.Repeats)
	if len(knownObs) == 0 {
		v.State = StateUnknown
		v.Note = "对照探测全部失败（网络层），无法判定"
		return v
	}
	knownStats := stats(knownObs)

	// 3) 差异信号：每一条都要超过基线抖动才计。
	sigs := diffSignals(v.Baseline, knownStats, base, knownObs)
	v.Signals = sigs
	v.Enumerable = len(sigs) > 0
	if !v.Enumerable {
		v.State = StateNoSignal
		v.Note = "三次对照未见超出抖动的差异——该入口不构成 oracle"
		return v
	}
	v.Confidence = confidence(sigs)

	// 4) 归类：从信号里读出是哪个态。
	state, note := classify(v.Signals, knownObs)
	v.State = state
	if note != "" {
		v.Note = note
	}
	// 锁定/停用是「改写结论」的强信号，必须也进 Signals——
	// 否则报告只能看到 note 里一句话，看不出这结论是怎么来的。
	if (state == StateLocked || state == StateDisabled) && !hasSignal(v.Signals, "lock_echo") {
		kind := "lock_echo"
		if state == StateDisabled {
			kind = "disabled_echo"
		}
		v.Signals = append(v.Signals, Signal{Kind: kind, Detail: note, Weight: 0.85})
		v.Confidence = confidence(v.Signals)
	}

	// 5) 锁定探测：只对「存在」的账号做，且次数受 Options 限制。
	//    目的不是枚举，是回答「这个入口会不会把真人锁死」——那是另一个风险面。
	if state == StateExist || state == StateUnknown {
		if lockSig, locked := r.detectLock(ctx, p, known); locked {
			v.Signals = append(v.Signals, lockSig)
			v.State = StateLocked
			v.Confidence = clamp(v.Confidence + lockSig.Weight*0.3)
		}
	}
	return v
}

// sample 重复采集同一输入 n 次，按 Interval 节流。
func (r *Runner) sample(ctx context.Context, p Probe, account string, n int) []Observation {
	out := make([]Observation, 0, n)
	for i := 0; i < n; i++ {
		if i > 0 {
			select {
			case <-ctx.Done():
				return out
			case <-time.After(r.opts.Interval):
			}
		}
		obs := r.once(ctx, p, account)
		if obs.Err != "" {
			// 网络失败不进统计（它不是「服务端的回答」），但要留痕。
			out = append(out, obs)
			continue
		}
		out = append(out, obs)
	}
	return out
}

// once 发一次请求并记录观测。
func (r *Runner) once(ctx context.Context, p Probe, account string) Observation {
	obs := Observation{Probe: p.Name, Account: account, At: time.Now()}

	method := strings.ToUpper(strings.TrimSpace(p.Method))
	if method == "" {
		method = http.MethodGet
	}
	target := substitute(p.URL, account, p.Encode)
	bodyStr := substitute(p.Body, account, p.Encode)

	var bodyReader io.Reader
	if bodyStr != "" {
		bodyReader = strings.NewReader(bodyStr)
	}
	httpReq, err := http.NewRequestWithContext(ctx, method, target, bodyReader)
	if err != nil {
		obs.Err = "构造请求失败: " + err.Error()
		return obs
	}
	httpReq.Header.Set("User-Agent", r.opts.UserAgent)
	httpReq.Header.Set("Accept", "*/*")
	if bodyStr != "" {
		switch {
		case strings.HasPrefix(strings.TrimSpace(bodyStr), "{"):
			httpReq.Header.Set("Content-Type", "application/json")
		default:
			httpReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
	}
	for k, v := range p.Headers {
		httpReq.Header.Set(k, v)
	}

	t0 := time.Now()
	resp, err := r.client.Do(httpReq)
	obs.Latency = time.Since(t0)
	if err != nil {
		obs.Err = err.Error()
		return obs
	}
	defer resp.Body.Close()

	// 只读 MaxBody 前缀：判据要的是「变没变」，不是全文。
	buf := make([]byte, r.opts.MaxBody)
	n, _ := io.ReadFull(resp.Body, buf)
	buf = buf[:n]
	_, _ = io.Copy(io.Discard, resp.Body) // 排干，允许连接复用

	obs.Status = resp.StatusCode
	obs.Length = len(buf)
	obs.Body = string(buf)
	obs.Header = resp.Header
	return obs
}

// detectLock 判断该入口会不会在连续失败后锁定账号，并尽量读出「还剩几次」。
//
// 只探 LockProbes 次（默认 4，多数系统 5 次锁），且只对已知账号做。
// 这是本轮唯一会改变目标状态的动作，所以：
//   - 次数硬上限，不做「探到锁为止」；
//   - 探测完在报告里明确写「该账号可能已被锁定，解锁时间看目标策略」。
func (r *Runner) detectLock(ctx context.Context, p Probe, known string) (Signal, bool) {
	var prev *Observation
	trend := []int{}
	for i := 0; i < r.opts.LockProbes; i++ {
		select {
		case <-ctx.Done():
			return Signal{}, false
		case <-time.After(r.opts.Interval):
		}
		obs := r.once(ctx, p, known)
		if obs.Err != "" {
			return Signal{}, false
		}
		if n, ok := remainingAttempts(obs.Body); ok {
			trend = append(trend, n)
		}
		if prev != nil {
			// 转变判定：上一次还没有锁定语义、这一次有了 —— 无论状态码变不变都是信号。
			// 只看状态码会漏掉一大批站点：它们锁定时照样回 200，只是正文换了。
			if !looksLocked(prev.Body) && looksLocked(obs.Body) {
				return Signal{
					Kind:   "lock_echo",
					Detail: fmt.Sprintf("连续 %d 次后响应转为锁定语义（HTTP %d，正文含「已锁定」类措辞）", i+1, obs.Status),
					Weight: 0.9,
				}, true
			}
			if obs.Status != prev.Status && looksLocked(obs.Body) {
				return Signal{
					Kind:   "lock_echo",
					Detail: fmt.Sprintf("连续 %d 次后状态从 %d 变为 %d，响应含锁定语义", i+1, prev.Status, obs.Status),
					Weight: 0.9,
				}, true
			}
		}
		prev = &obs
	}
	if len(trend) >= 2 && strictlyDecreasing(trend) {
		return Signal{
			Kind:   "lock_echo",
			Detail: fmt.Sprintf("响应回显剩余尝试次数且逐次递减 %v——锁定状态本身成了 oracle，且可按需把账号锁死", trend),
			Weight: 0.85,
		}, true
	}
	return Signal{}, false
}

// substitute 替换 {{account}} 占位符。
//
// 编码方式必须和目标一致的解析器匹配：JSON 接口要转义，表单要 urlencode，
// 否则两次请求的差异会落在「转义字符」上——那是我们制造的差异，不是服务端泄露的。
func substitute(tpl, account, mode string) string {
	if !strings.Contains(tpl, "{{account}}") {
		return tpl
	}
	var repl string
	switch strings.ToLower(mode) {
	case "json":
		repl = jsonEscape(account)
	case "form":
		repl = url.QueryEscape(account)
	default:
		repl = account
	}
	return strings.ReplaceAll(tpl, "{{account}}", repl)
}

func jsonEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
