package oracle

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// 差异判定的三个常量。它们不是拍脑袋的阈值，而是「容错倍数」：
// 抖动的 k 倍以内一律不算信号。取 3 是因为长度抖动通常只有 ±3 字节
// （时间戳、CSRF token、随机 id），3σ 已覆盖绝大多数正常波动；
// 而真实的状态差异（「不存在」多一句提示、跳转页少一个 script）普遍在 20 字节以上。
const (
	lenK      = 3.0
	lenFloor  = 8.0 // 绝对下限：抖动为 0 时，差异小于 8 字节仍不算信号
	latK      = 3.0
	latFloor  = 80 * time.Millisecond // 绝对下限：网络抖动天然在几十毫秒
	minSignal = 1                     // 至少一条信号才判「该入口可判别」
)

func clamp(v float64) float64 {
	if v > 1 {
		return 1
	}
	if v < 0 {
		return 0
	}
	return v
}

// stats 汇总一组观测。只有「成功的观测」进统计——网络失败不是服务端的回答。
func stats(obs []Observation) Stats {
	s := Stats{}
	oks := make([]Observation, 0, len(obs))
	for _, o := range obs {
		if o.Err == "" {
			oks = append(oks, o)
		}
	}
	s.N = len(oks)
	if s.N == 0 {
		return s
	}
	statusSet := map[int]struct{}{}
	var lenSum, latSum float64
	for _, o := range oks {
		lenSum += float64(o.Length)
		latSum += float64(o.Latency)
		statusSet[o.Status] = struct{}{}
	}
	s.LenMean = lenSum / float64(s.N)
	s.LatMean = time.Duration(latSum / float64(s.N))

	var lenVar, latVar float64
	for _, o := range oks {
		d := float64(o.Length) - s.LenMean
		lenVar += d * d
		l := float64(o.Latency) - latSum/float64(s.N)
		latVar += l * l
	}
	s.LenStd = math.Sqrt(lenVar / float64(s.N))
	s.LatStd = time.Duration(math.Sqrt(latVar / float64(s.N)))

	for st := range statusSet {
		s.Statuses = append(s.Statuses, st)
	}
	sort.Ints(s.Statuses)
	// 稳定 = 状态码唯一 且 长度抖动 < 1 字节。
	// 两者都满足时，任何超阈差异都不可能是噪声——这是判据能成立的前提。
	s.Stable = len(s.Statuses) == 1 && s.LenStd < 1.0
	return s
}

// diffSignals 比较基线与对照，产出超阈信号。
func diffSignals(base, known Stats, baseObs, knownObs []Observation) []Signal {
	var sigs []Signal

	// 1) 状态码差异：最硬的一条。200 vs 401、302 vs 200，几乎只能是状态不同。
	if len(base.Statuses) == 1 && len(known.Statuses) == 1 && base.Statuses[0] != known.Statuses[0] {
		sigs = append(sigs, Signal{
			Kind:   "status_diff",
			Detail: "状态码不同：不存在账号 " + itoa(base.Statuses[0]) + " / 存在账号 " + itoa(known.Statuses[0]),
			Weight: 0.9,
		})
	}

	// 2) 长度差异：必须同时超过 3σ 与 8 字节下限，否则是抖动。
	lenDelta := math.Abs(known.LenMean - base.LenMean)
	lenThresh := math.Max(lenK*base.LenStd, lenFloor)
	if lenDelta > lenThresh {
		sigs = append(sigs, Signal{
			Kind: "len_diff",
			Detail: "响应长度差 " + ftoa(lenDelta) + " 字节（基线均 " + ftoa(base.LenMean) +
				"，对照均 " + ftoa(known.LenMean) + "，阈值 " + ftoa(lenThresh) + "）",
			Weight: clamp(lenDelta / (lenThresh * 4)),
		})
	}

	// 3) 耗时差异：比长度更容易被 CDN / 缓存掩盖，权重低一档。
	latDelta := absDur(known.LatMean - base.LatMean)
	latThresh := durMax(time.Duration(latK*float64(base.LatStd)), latFloor)
	if latDelta > latThresh {
		sigs = append(sigs, Signal{
			Kind: "latency_diff",
			Detail: "响应耗时差 " + latDelta.String() + "（基线均 " + base.LatMean.String() +
				"，对照均 " + known.LatMean.String() + "）",
			Weight: clamp(float64(latDelta) / float64(latThresh*4)),
		})
	}

	// 4) 文案差异：只在基线稳定时才用。取两条响应的公共前后缀，中间那段就是分歧。
	if base.Stable && known.Stable && len(baseObs) > 0 && len(knownObs) > 0 {
		if frag, ok := textFragment(baseObs[0].Body, knownObs[0].Body); ok {
			sigs = append(sigs, Signal{
				Kind:   "text_diff",
				Detail: "响应文案分歧：" + frag,
				Weight: 0.6,
			})
		}
	}

	// 5) Set-Cookie 差异：有的系统「不存在」也下发会话 cookie，有的不。
	if a, b := cookieNames(baseObs), cookieNames(knownObs); a != b {
		sigs = append(sigs, Signal{
			Kind:   "set_cookie_diff",
			Detail: "Set-Cookie 不同：基线 [" + a + "] / 对照 [" + b + "]",
			Weight: 0.4,
		})
	}
	return sigs
}

// confidence 把信号累计成置信度。多条弱信号可以叠加，但单条强信号也有下限。
//
// 口径：0.9 以上是「可以写进报告」；0.5–0.9 是「值得人工复看」；
// 0.5 以下只当线索。不把 0.6 说成「确认」，是这套东西能被交付的前提。
func confidence(sigs []Signal) float64 {
	if len(sigs) == 0 {
		return 0
	}
	// 用「至少两条独立信号」作为 high 的门槛：单条信号可能是措辞差异，
	// 两条以上不同维度（状态码 + 长度、长度 + 文案）同时成立，才难用噪声解释。
	prod := 1.0
	for _, s := range sigs {
		prod *= (1 - clamp(s.Weight))
	}
	c := 1 - prod
	if len(sigs) >= 2 {
		c = math.Max(c, 0.7) // 两条独立信号的下限
	}
	if len(sigs) >= 3 {
		c = math.Max(c, 0.85)
	}
	return clamp(c)
}

var (
	lockRe    = regexp.MustCompile(`(?i)(locked|lock(ed)? now|too many (failed )?attempts|account is locked|已锁定|账号已锁|账户已锁|尝试次数过多|登录失败次数过多)`)
	disableRe = regexp.MustCompile(`(?i)(disabled|deactivated|expired|停用|禁用|已过期|账户已失效|账号已失效)`)
	remainRe  = regexp.MustCompile(`(?i)(?:will be locked after|remaining|还剩|剩余|还有|还可以尝试|可尝试|还能尝试|可以尝试)\s*(\d{1,2})\s*(?:times|attempts|次)`)
	remainRe2 = regexp.MustCompile(`(\d{1,2})\s*(?:times|attempts)\s*(?:remaining|left)`)
	successRe = regexp.MustCompile(`(?i)("success"\s*:\s*true|success|ok|登录成功|操作成功)`)
)

// classify 从信号 + 对照响应里读出状态。
//
// 判定顺序有讲究：「剩余次数递减」最先判——它不需要真把账号打锁就已经暴露了锁定机制；
// 然后才是「锁定 / 停用」这类静态语义（它们改写结论）；最后才落到存在性。
func classify(sigs []Signal, knownObs []Observation) (State, string) {
	// 剩余次数递减 = 锁定回显。这条要最先判：对照组的三次响应本身就是递减的，
	// 等到 detectLock 把账号真打锁才判定，等于为了拿结论先去制造一次事故。
	if trend := attemptTrend(knownObs); strictlyDecreasing(trend) {
		return StateLocked, "对照响应回显剩余尝试次数且逐次递减 " + fmt.Sprint(trend) +
			"——锁定状态本身成了 oracle，且可按需把任意账号锁死"
	}

	body := joinBodies(knownObs)
	if lockRe.MatchString(body) {
		return StateLocked, "对照响应含锁定语义——该账号当前被锁，或该入口主动告知锁定状态"
	}
	if disableRe.MatchString(body) {
		return StateDisabled, "对照响应含停用/失效语义，账号存在但不可用"
	}
	if len(sigs) >= minSignal {
		if successRe.MatchString(body) {
			return StateExist, "对照响应为成功态，基线响应不同——可区分存在性"
		}
		return StateExist, "与基线存在稳定差异，按「存在」归档（响应未出现成功语义，需人工复核归类）"
	}
	return StateNoSignal, ""
}

// attemptTrend 抽取一组响应里的「剩余尝试次数」序列。
func attemptTrend(obs []Observation) []int {
	var out []int
	for _, o := range obs {
		if o.Err != "" {
			continue
		}
		if n, ok := remainingAttempts(o.Body); ok {
			out = append(out, n)
		}
	}
	return out
}

func joinBodies(obs []Observation) string {
	var b strings.Builder
	for _, o := range obs {
		if o.Err == "" {
			b.WriteString(o.Body)
			b.WriteString("\n")
		}
	}
	return b.String()
}

// remainingAttempts 从响应里读「还剩几次」。
// 这是最值钱的一种 oracle：它不只告诉你账号存在，还把锁定状态暴露成可观测值。
func remainingAttempts(body string) (int, bool) {
	if m := remainRe.FindStringSubmatch(body); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 0 && n <= 20 {
			return n, true
		}
	}
	if m := remainRe2.FindStringSubmatch(body); len(m) == 2 {
		if n, err := strconv.Atoi(m[1]); err == nil && n >= 0 && n <= 20 {
			return n, true
		}
	}
	return 0, false
}

func looksLocked(body string) bool { return lockRe.MatchString(body) }

func strictlyDecreasing(xs []int) bool {
	if len(xs) < 2 {
		return false
	}
	for i := 1; i < len(xs); i++ {
		if xs[i] >= xs[i-1] {
			return false
		}
	}
	return true
}

func cookieNames(obs []Observation) string {
	if len(obs) == 0 || obs[0].Header == nil {
		return ""
	}
	var names []string
	for _, c := range obs[0].Header.Values("Set-Cookie") {
		if i := strings.IndexByte(c, '='); i > 0 {
			names = append(names, strings.TrimSpace(c[:i]))
		}
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}

// textFragment 取两条响应的公共前后缀，返回中间分歧片段（截断到 160 字符）。
// 重叠度过高（分歧 < 8 字节）视为「没有语义差异」，不产信号。
func textFragment(a, b string) (string, bool) {
	if a == b {
		return "", false
	}
	// 公共前缀
	i := 0
	for i < len(a) && i < len(b) && a[i] == b[i] {
		i++
	}
	// 公共后缀（不与前缀重叠）
	j := 0
	for j < len(a)-i && j < len(b)-i && a[len(a)-1-j] == b[len(b)-1-j] {
		j++
	}
	fa := strings.TrimSpace(a[i : len(a)-j])
	fb := strings.TrimSpace(b[i : len(b)-j])
	if len(fa) < 8 && len(fb) < 8 {
		return "", false
	}
	frag := "基线《" + truncate(fa, 80) + "》 vs 对照《" + truncate(fb, 80) + "》"
	return frag, true
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

func itoa(i int) string { return strconv.Itoa(i) }
func ftoa(f float64) string {
	return strconv.FormatFloat(f, 'f', 1, 64)
}
func absDur(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
func durMax(a, b time.Duration) time.Duration {
	if a > b {
		return a
	}
	return b
}

// hasSignal 判断信号列表里是否已含某类。
func hasSignal(sigs []Signal, kind string) bool {
	for _, s := range sigs {
		if s.Kind == kind {
			return true
		}
	}
	return false
}
