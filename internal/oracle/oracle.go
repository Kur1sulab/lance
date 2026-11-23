// Package oracle 把「登录 / 找回密码 / 注册」这三类入口的响应差异，收敛成一个可判定的
// 账号状态 oracle。
//
// 为什么需要它：账号枚举本身不是漏洞，**能稳定区分「存在 / 不存在 / 锁定 / 停用」才是**。
// 人工判断时踩过的坑几乎都一样——
//
//  1. 拿一段文案当判据（「用户不存在」）。厂商换个措辞、加个 i18n、套个统一错误页，判据就废了；
//     而真实的 oracle 往往是**长度差 3 个字节**或**耗时差 80ms**，肉眼看不出来。
//  2. 不测抖动。同一份请求连打三次，长度本来就在变（时间戳、CSRF、随机 id），
//     把抖动当信号，等于给每个站点都判一个「存在枚举」。
//  3. 只测一条链路。三态 oracle 常常只在找回密码那条路上，登录口是严格对齐的；
//     反过来也有：登录口回显剩余尝试次数，找回口却要求先过验证码。
//
// 所以这里的设计是「先量抖动，再谈差异」：
//
//	基线 ——用「已知不存在」的账号重复探测，得到长度/耗时的自然抖动
//	对照 ——用「已知存在」的账号做同样的重复探测
//	判据 ——差异必须超过 k 倍抖动才计信号；信号要两条以上才算 high
//
// 它只发探测请求、只读响应，不做任何写入、不尝试登录，也不爆破。
package oracle

import (
	"net/http"
	"time"
)

// State 是单个账号在某个入口上被判定的状态。
type State string

const (
	StateExist    State = "exist"     // 该账号存在
	StateNotExist State = "not_exist" // 该账号不存在
	StateLocked   State = "locked"    // 存在但被锁定
	StateDisabled State = "disabled"  // 存在但停用/过期
	StateUnknown  State = "unknown"   // 有信号但归不进上面四类
	StateNoSignal State = "no_signal" // 与基线无显著差异，判不出
)

// Probe 是一个探测点：一个具体的请求模板。
//
// Account 会被替换进 URL 与 Body 里的 {{account}} 占位符。之所以用占位符而不是
// 让调用方自己拼，是为了让「同一个探测点打两个不同账号」成为框架能力——
// 差异判定必须建立在同模板、同头、同方法的两次请求之上，少一样就不是对照实验。
type Probe struct {
	Name    string            `json:"name"`              // login / forgot / register
	Method  string            `json:"method"`            // GET / POST
	URL     string            `json:"url"`               // 可含 {{account}}
	Body    string            `json:"body,omitempty"`    // 可含 {{account}}
	Headers map[string]string `json:"headers,omitempty"` // 额外请求头
	// Encode 决定 {{account}} 在 Body 里的替换方式：
	// "raw"（默认，直接替换）、"json"（做 JSON 字符串转义）、"form"（urlencode）。
	// 判错编码会让两次请求的差异跑到「转义」上，而不是账号状态上。
	Encode string `json:"encode,omitempty"`
}

// Observation 是一次探测的原始观测。它刻意保留原始字节数而不是解析后的值：
// 判据要的是「服务端回的东西变没变」，不是「它说了什么」。
type Observation struct {
	Probe   string        `json:"probe"`
	Account string        `json:"account"`
	Status  int           `json:"status"`
	Length  int           `json:"length"`
	Latency time.Duration `json:"latency"`
	Body    string        `json:"body"`          // 截断到 Options.MaxBody
	Header  http.Header   `json:"-"`             // 不落盘：可能带 Cookie/Set-Cookie
	Err     string        `json:"err,omitempty"` // 网络层失败（超时/连接拒绝）
	At      time.Time     `json:"at"`
}

// Stats 是一组观测的统计量，用来代表「抖动」。
type Stats struct {
	N        int           `json:"n"`
	LenMean  float64       `json:"len_mean"`
	LenStd   float64       `json:"len_std"`
	LatMean  time.Duration `json:"lat_mean"`
	LatStd   time.Duration `json:"lat_std"`
	Statuses []int         `json:"statuses"`
	// Stable 为 true 表示这组观测的状态码全同、长度抖动小于 1 字节，
	// 也就是「同一输入得到同一输出」——只有这种基线才配用来做差异判定。
	Stable bool `json:"stable"`
}

// Signal 是一条判据证据。判定结果必须能说清「凭什么」。
type Signal struct {
	Kind   string  `json:"kind"`   // status_diff / len_diff / latency_diff / text_diff / lock_echo / set_cookie_diff
	Detail string  `json:"detail"` // 人可读的具体差异（含数值）
	Weight float64 `json:"weight"` // 0–1，用于累计置信度
}

// Verdict 是一个账号在一个探测点上的结论。
type Verdict struct {
	Probe      string   `json:"probe"`
	Account    string   `json:"account"`
	State      State    `json:"state"`
	Confidence float64  `json:"confidence"` // 0–1
	Signals    []Signal `json:"signals"`
	// Enumerable 表示「该入口能区分存在与不存在」——这是要不要写进报告的关键结论。
	// 它和 State 不同：State 是这次探测到的是什么，Enumerable 是这个入口本身有没有 oracle。
	Enumerable bool   `json:"enumerable"`
	Baseline   Stats  `json:"baseline"`
	Note       string `json:"note,omitempty"` // 判不出时写清原因，不许留空
}

// Options 是探测参数。默认值偏保守：慢一点没关系，把真人账号锁了才是事故。
type Options struct {
	// Timeout 单次请求超时，默认 10s。
	Timeout time.Duration
	// Interval 两次请求之间的最小间隔，默认 1s。锁定策略是按账号计数的，
	// 探「存在」的账号时打太快会把它锁掉——那等于自己制造事故。
	Interval time.Duration
	// Repeats 每组基线重复次数，默认 3。低于 2 无法算抖动。
	Repeats int
	// MaxBody 保留响应体前缀字节数，默认 4096。
	MaxBody int
	// UserAgent 默认标注自身身份，便于目标方审计。
	UserAgent string
	// LockProbes 锁定探测次数，默认 4（多数系统 5 次锁定，留一次余量）。
	LockProbes int
	// Insecure 跳过 TLS 校验（自签靶场用）。
	Insecure bool
}

func (o *Options) defaults() {
	if o.Timeout <= 0 {
		o.Timeout = 10 * time.Second
	}
	if o.Interval <= 0 {
		o.Interval = time.Second
	}
	if o.Repeats < 2 {
		o.Repeats = 3
	}
	if o.MaxBody <= 0 {
		o.MaxBody = 4096
	}
	if o.UserAgent == "" {
		o.UserAgent = "Lance-OracleProbe/1.0 (+authorized-testing)"
	}
	if o.LockProbes <= 0 {
		o.LockProbes = 4
	}
}

// Request 描述一次「拿两个已知账号做对照」的完整动作。
type Request struct {
	// Probes 探测点列表（登录 / 找回 / 注册…）。
	Probes []Probe
	// Known 已知存在的账号（调用方自己注册或已确认的，例如本次测试用的测试账号）。
	Known string
	// Absent 确认不存在的账号（随机串，但要保证格式合法——
	// 格式非法会被前置校验拦掉，那样的差异是格式差异不是状态差异）。
	Absent string
}

// Report 是一次完整判定的输出。
type Report struct {
	Known      string        `json:"known"`
	Absent     string        `json:"absent"`
	Verdicts   []Verdict     `json:"verdicts"`
	Enumerable bool          `json:"enumerable"` // 任一入口可判别即为 true
	StartedAt  time.Time     `json:"started_at"`
	Elapsed    time.Duration `json:"elapsed"`
	Notes      []string      `json:"notes,omitempty"`
}
