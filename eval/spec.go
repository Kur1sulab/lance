package eval

// 私有冻结真实漏洞评测层（调研总纲 §6.1）的规格定义、加载与校验。
//
// 为什么要有这一层：平台自己的 6.2 闸门只回答「这次发现的证据站不站得住」，
// 回答不了「模型的攻击能力到底涨了没有」。要回答后者，靶必须不可被模型背题：
//   - XBOW 的 104 题已经饱和作废（分数顶满，区分度归零）；
//   - CyberGym 的教训更直接：同一漏洞，单文件裁剪版 66.7%，换成全仓库真实构建 0.7%。
// 所以评测层坚持三条：**靶冻结、判据是后果、题私有且可滚动更新**。
//
// 三层结构不是难度分级，是三种判据来源：
//   LayerSynthetic(1) 合成 in-vitro：靶我们自己写，测组件能力，用里程碑计分；
//   LayerFrozen(2)    真实历史 CVE 的冻结版本：判「攻击后果是否达成」，outcome 计分；
//   LayerDefense(3)   防御层：同一后果在 WAF / 统一响应陷阱下还成不成立，测对抗生存。
//
// 判「后果达成」而不是判 flag，是本包最重要的一条设计：
//   flag 是人为放进靶里的字符串，模型可以靠泄漏、猜路径、甚至读题面拿到，
//   拿到 flag 不等于打通了链路；而「后果」是靶的外部可观测状态——
//   敏感数据真的出现在响应里、目标进程真的打开了 webroot 外的文件、
//   目标真的替攻击者拨通了内网地址。这些状态只能由攻击行为造成，不能靠读题造成。
//   本包的 canary 全部在运行动态铸造，靶内不存在任何「题面里能查到的答案」。

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// 层号。见文件头注释。
const (
	LayerSynthetic = 1 // 合成 in-vitro
	LayerFrozen    = 2 // 真实历史 CVE 冻结版本
	LayerDefense   = 3 // 防御层（WAF / 统一响应陷阱）
)

// 计分模式。里程碑与 outcome 不是二选一的美化说法，分母不同：
//   - milestone：分母是任务声明的满分，测「推进到第几段」；
//   - outcome：分母是「后果达成 / 未达成」，测「有没有真的打穿」。
//
// 一个 outcome 任务照样可以带里程碑，用来解释失败停在哪一段，但 verdict 只由 outcome 决定。
const (
	ScoringMilestone = "milestone"
	ScoringOutcome   = "outcome"
)

// 判据（probe）类型。三种都只看环境事实，不看被测方自报。
const (
	ProbeHTTPResponse = "http_response" // 关键响应特征：状态码 + 必含/禁含片段
	ProbeFileRead     = "file_read"     // 宿主侧事实：能不能读到指定内容（含目标自己写的审计日志）
	ProbeTCPConnect   = "tcp_connect"   // 出站事实：评测方持有的监听器有没有真的收到连接
)

// 动作类型。attack 段是被测方提交（或任务内置 PoC）的动作清单，判定不读它，
// 只把它原样重放一遍留下真实响应作为审计证据。
const (
	ActionHTTP = "http"
	ActionExec = "exec"
)

// Task 是一个评测任务的完整定义。加载后必须过 Validate，再交 Runner 执行。
type Task struct {
	TaskID string `json:"task_id" yaml:"task_id"`
	Name   string `json:"name" yaml:"name"`
	Layer  int    `json:"layer" yaml:"layer"`
	// Notes 写清这道题在测什么、为什么这样判。报告与 scorecard 会带上，便于审计。
	Notes string `json:"notes,omitempty" yaml:"notes,omitempty"`

	Target TargetSpec `json:"target" yaml:"target"`
	// Freeze 声明本题靶的冻结物。Runner 会拿它和 eval/freeze.lock 逐项比对，
	// 不一致直接拒绝运行——这是防污染的唯一硬保证，不做「警告后继续」。
	Freeze FreezeSpec `json:"freeze" yaml:"freeze"`
	// Canaries 是运行动态铸造的哨兵。它们不在靶的源码里、不在题面里，
	// 只存在于本次运行的内存/临时目录，因此不可能被背题或从仓库里 grep 到。
	Canaries []CanarySpec `json:"canaries,omitempty" yaml:"canaries,omitempty"`
	// Attack 是被测方提交的动作清单（MVP 由任务内置 PoC 提供）。
	Attack []Action `json:"attack,omitempty" yaml:"attack,omitempty"`
	// Objective 是 outcome 判据。scoring.mode=outcome 时必填。
	Objective Objective `json:"objective,omitempty" yaml:"objective,omitempty"`
	// Milestones 是分段里程碑。layer=1 必填；layer=2/3 可选，用于解释失败停在哪一段。
	Milestones []Milestone `json:"milestones,omitempty" yaml:"milestones,omitempty"`
	Scoring    ScoringSpec `json:"scoring" yaml:"scoring"`
	// DefenseProfile 只在 layer=3 必填，声明本任务跑在哪种防御画像下。
	DefenseProfile *DefenseProfile `json:"defense_profile,omitempty" yaml:"defense_profile,omitempty"`
	// Calibration 只给自检/夹具题用：声明「本题预期结果」，用来验证 judge 两个方向都判得对。
	// 真实 CVE 题不写这一项——写了就等于把答案写进题面。
	Calibration *Calibration `json:"calibration,omitempty" yaml:"calibration,omitempty"`

	// SourcePath 是加载时填的（仓库相对路径），用于冻结校验与报告。
	SourcePath string `json:"-" yaml:"-"`
}

// TargetSpec 描述靶怎么起来。MVP 只支持本机进程（exec）与已在跑的服务（static），
// 容器形态留 kind=exec + cmd=["docker","run",...] 即可，不额外造一层抽象。
type TargetSpec struct {
	Kind string `json:"kind" yaml:"kind"` // exec | static
	// Build 可选：起靶前先编译/构建。把 go run 换成 go build 是为了拿到真实进程句柄——
	// go run 会多一层父进程，杀父留子会让下一道题抢端口、日志串台。
	Build         []string          `json:"build,omitempty" yaml:"build,omitempty"`
	Workdir       string            `json:"workdir,omitempty" yaml:"workdir,omitempty"`
	Cmd           []string          `json:"cmd,omitempty" yaml:"cmd,omitempty"`
	Env           map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Ready         ReadySpec         `json:"ready" yaml:"ready"`
	StopTimeoutMS int               `json:"stop_timeout_ms,omitempty" yaml:"stop_timeout_ms,omitempty"`
	// BaseURL 只在 kind=static 时使用：靶已经在跑，评测方只连过去。
	BaseURL string `json:"base_url,omitempty" yaml:"base_url,omitempty"`
}

// ReadySpec 是起靶后的就绪判据。就绪失败必须报错退出，绝不能当成「打不穿」，
// 否则基础设施故障会被统计成模型能力问题。
type ReadySpec struct {
	Kind      string `json:"kind" yaml:"kind"` // tcp_dial | http
	Addr      string `json:"addr,omitempty" yaml:"addr,omitempty"`
	URL       string `json:"url,omitempty" yaml:"url,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
}

// FreezeSpec 是任务声明的冻结信息。
//
//	Kind=manifest      逐文件 SHA256（本仓库夹具与源码形态用这个，无 git 依赖）；
//	Kind=image_digest  容器镜像 digest，Ref 即 sha256:...；
//	Kind=config_hash   配置哈希（统一响应陷阱、WAF 规则集等的指纹）。
//
// 三种都只是「同一个事实的不同载体」，校验逻辑统一：比对 freeze.lock 里登记的值。
type FreezeSpec struct {
	Kind      string           `json:"kind" yaml:"kind"`
	Ref       string           `json:"ref" yaml:"ref"`
	Artifacts []FreezeArtifact `json:"artifacts" yaml:"artifacts"`
}

// FreezeArtifact 是冻结清单的一项。Path 必须是仓库相对路径。
type FreezeArtifact struct {
	Path   string `json:"path" yaml:"path"`
	SHA256 string `json:"sha256" yaml:"sha256"`
}

// CanarySpec 定义一个运行动态铸造的哨兵。
//
//	Kind=file   在靶所在宿主上写一个随机串到 Path（Path 必须在靶的 webroot 之外，
//	            这样只有真的穿越出去才读得到）；
//	Kind=inline 只生成随机串，不落盘，给 tcp 监听器当 banner 用——
//	            这种哨兵物理上不可被读文件拿到，只能靠目标真的出站连过来。
type CanarySpec struct {
	ID     string `json:"id" yaml:"id"`
	Kind   string `json:"kind" yaml:"kind"` // file | inline
	Path   string `json:"path,omitempty" yaml:"path,omitempty"`
	Prefix string `json:"prefix,omitempty" yaml:"prefix,omitempty"`
}

// Action 是一条被测方动作。
type Action struct {
	ID        string            `json:"id" yaml:"id"`
	Kind      string            `json:"kind" yaml:"kind"` // http | exec
	Method    string            `json:"method,omitempty" yaml:"method,omitempty"`
	URL       string            `json:"url,omitempty" yaml:"url,omitempty"`
	Headers   map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body      string            `json:"body,omitempty" yaml:"body,omitempty"`
	Command   []string          `json:"command,omitempty" yaml:"command,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
	Note      string            `json:"note,omitempty" yaml:"note,omitempty"`
}

// Objective 是「攻击后果达成」的判据集合。Mode=all 时全部 probe 命中才算达成。
type Objective struct {
	Description string  `json:"description" yaml:"description"`
	Mode        string  `json:"mode,omitempty" yaml:"mode,omitempty"` // all（默认）| any
	Probes      []Probe `json:"probes" yaml:"probes"`
}

// Probe 是一条判据。所有字段都支持 {{var}} 占位符（见 vars.go），解析失败即报错，
// 不允许「占位符没替换成功就当成字符串匹配」——那会静默把判据变成永假。
type Probe struct {
	ID   string `json:"id" yaml:"id"`
	Kind string `json:"kind" yaml:"kind"`

	// http_response
	Method            string            `json:"method,omitempty" yaml:"method,omitempty"`
	URL               string            `json:"url,omitempty" yaml:"url,omitempty"`
	Headers           map[string]string `json:"headers,omitempty" yaml:"headers,omitempty"`
	Body              string            `json:"body,omitempty" yaml:"body,omitempty"`
	ExpectStatus      []int             `json:"expect_status,omitempty" yaml:"expect_status,omitempty"`
	ExpectContains    []string          `json:"expect_contains,omitempty" yaml:"expect_contains,omitempty"`
	ExpectNotContains []string          `json:"expect_not_contains,omitempty" yaml:"expect_not_contains,omitempty"`

	// file_read
	Path string `json:"path,omitempty" yaml:"path,omitempty"`

	// tcp_connect
	Listen string `json:"listen,omitempty" yaml:"listen,omitempty"`
	Banner string `json:"banner,omitempty" yaml:"banner,omitempty"`
	// CredentialBanner 只在客户端载荷里出现 CredentialOn 时追加。
	// 用途：同一个监听器先只给"元数据"banner，攻击方必须让目标发对请求才拿得到凭据——
	// 这样「读到元数据 70 → 拿到凭据 100」才是两个真实的深度，而不是同一个响应判两次。
	CredentialBanner            string   `json:"credential_banner,omitempty" yaml:"credential_banner,omitempty"`
	CredentialOn                string   `json:"credential_on,omitempty" yaml:"credential_on,omitempty"`
	ExpectClientPayloadContains []string `json:"expect_client_payload_contains,omitempty" yaml:"expect_client_payload_contains,omitempty"`

	TimeoutMS int `json:"timeout_ms,omitempty" yaml:"timeout_ms,omitempty"`
}

// Milestone 是一段里程碑。Requires 声明前置段：前置未达成时本段即使 probe 命中也不给分，
// 避免「跳过中间段直接拿满分」把链路理解误差算成能力。
type Milestone struct {
	ID       string   `json:"id" yaml:"id"`
	Name     string   `json:"name" yaml:"name"`
	Points   int      `json:"points" yaml:"points"`
	Requires []string `json:"requires,omitempty" yaml:"requires,omitempty"`
	Probe    Probe    `json:"probe" yaml:"probe"`
}

// ScoringSpec 声明计分模式与及格线。
//
// PointsMode 决定 milestones[].points 怎么读：
//
//	additive（默认）  每段 points 是「本段新增分」，满分 = 各段之和；
//	cumulative        每段 points 是「本段达成后的累计分」，满分 = 链尾分数。
//
// 后者对应 §6.1 例子里的 30 → 70 → 100 阶梯：读到元数据就是 70 分（含出站的 30），
// 不是再加 70。两种口径混用会让同一题在不同人手里算出不同分数，所以必须显式声明。
type ScoringSpec struct {
	Mode       string `json:"mode" yaml:"mode"`                                   // milestone | outcome
	PointsMode string `json:"points_mode,omitempty" yaml:"points_mode,omitempty"` // additive | cumulative
	PassPoints int    `json:"pass_points,omitempty" yaml:"pass_points,omitempty"`
}

// DefenseProfile 是防御画像。Kind=none 表示无防御（对照组）。
type DefenseProfile struct {
	ID          string `json:"id" yaml:"id"`
	Kind        string `json:"kind" yaml:"kind"` // none | input_filter | uniform_response | waf
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Calibration 是夹具题的自检预期，用来双向校验 judge。
type Calibration struct {
	ExpectAchieved *bool `json:"expect_achieved" yaml:"expect_achieved"`
}

var taskIDPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{2,63}$`)

// LoadTask 读单个任务文件，按扩展名选 JSON 或 YAML 解码器，并做严格校验。
// 严格（DisallowUnknownFields / KnownFields）是故意的：任务定义写错字段名时
// 必须炸在加载期，而不是静默忽略后跑到一半给出一个看似正常的分数。
func LoadTask(path string) (*Task, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("读取任务文件失败 %s: %w", path, err)
	}
	t := &Task{}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".json":
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(t); err != nil {
			return nil, fmt.Errorf("解析 JSON 任务失败 %s: %w", path, err)
		}
	case ".yaml", ".yml":
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		if err := dec.Decode(t); err != nil {
			return nil, fmt.Errorf("解析 YAML 任务失败 %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("不支持的任务文件扩展名 %s（只认 .json/.yaml/.yml）", path)
	}
	t.SourcePath = path
	if err := t.Validate(); err != nil {
		return nil, fmt.Errorf("任务 %s 校验失败: %w", path, err)
	}
	return t, nil
}

// LoadTaskDir 读取目录下全部任务文件（不递归），按 task_id 排序返回。
func LoadTaskDir(dir string) ([]*Task, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("读取任务目录失败 %s: %w", dir, err)
	}
	var tasks []*Task
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		switch strings.ToLower(filepath.Ext(e.Name())) {
		case ".json", ".yaml", ".yml":
		default:
			continue
		}
		t, err := LoadTask(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, err
		}
		tasks = append(tasks, t)
	}
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].TaskID < tasks[j].TaskID })
	return tasks, nil
}

// Validate 在加载期把能查的错全查掉。判据写错的代价是「分数看着正常但毫无意义」，
// 这种错必须在跑之前炸出来，而不是跑完之后靠人肉复核。
func (t *Task) Validate() error {
	if !taskIDPattern.MatchString(t.TaskID) {
		return fmt.Errorf("task_id %q 不合法（小写字母数字点横线，3-64 位）", t.TaskID)
	}
	if t.Name == "" {
		return fmt.Errorf("name 必填")
	}
	if t.Layer != LayerSynthetic && t.Layer != LayerFrozen && t.Layer != LayerDefense {
		return fmt.Errorf("layer 必须是 1/2/3，得到 %d", t.Layer)
	}
	if err := t.Target.validate(); err != nil {
		return err
	}
	if err := t.Freeze.validate(); err != nil {
		return err
	}

	seenCanary := map[string]bool{}
	for i, c := range t.Canaries {
		if c.ID == "" {
			return fmt.Errorf("canaries[%d].id 必填", i)
		}
		if seenCanary[c.ID] {
			return fmt.Errorf("canary id 重复: %s", c.ID)
		}
		seenCanary[c.ID] = true
		switch c.Kind {
		case "file":
			if c.Path == "" {
				return fmt.Errorf("canary %s 是 file 类型，必须给 path", c.ID)
			}
		case "inline":
			if c.Path != "" {
				return fmt.Errorf("canary %s 是 inline 类型，不该给 path（不落盘才不可被读文件拿到）", c.ID)
			}
		default:
			return fmt.Errorf("canary %s 的 kind 必须是 file/inline，得到 %q", c.ID, c.Kind)
		}
	}

	seenAction := map[string]bool{}
	for i, a := range t.Attack {
		if a.ID == "" {
			return fmt.Errorf("attack[%d].id 必填", i)
		}
		if seenAction[a.ID] {
			return fmt.Errorf("action id 重复: %s", a.ID)
		}
		seenAction[a.ID] = true
		switch a.Kind {
		case ActionHTTP:
			if a.URL == "" {
				return fmt.Errorf("action %s 是 http 类型，必须给 url", a.ID)
			}
		case ActionExec:
			if len(a.Command) == 0 {
				return fmt.Errorf("action %s 是 exec 类型，必须给 command", a.ID)
			}
		default:
			return fmt.Errorf("action %s 的 kind 必须是 http/exec，得到 %q", a.ID, a.Kind)
		}
	}

	if err := t.Scoring.validate(); err != nil {
		return err
	}

	// probe id 全局唯一：objective 与 milestones 共用一张表，重复会导致结果互相覆盖。
	probeIDs := map[string]bool{}
	checkProbe := func(where string, p Probe) error {
		if p.ID == "" {
			return fmt.Errorf("%s: probe.id 必填", where)
		}
		if probeIDs[p.ID] {
			return fmt.Errorf("%s: probe id 重复 %s（objective 与 milestones 共用命名空间）", where, p.ID)
		}
		probeIDs[p.ID] = true
		return p.validate(where)
	}

	if t.Scoring.Mode == ScoringOutcome {
		if len(t.Objective.Probes) == 0 {
			return fmt.Errorf("scoring.mode=outcome 必须给至少一条 objective.probes（否则 outcome 无法判定）")
		}
		if t.Objective.Description == "" {
			return fmt.Errorf("scoring.mode=outcome 必须写 objective.description，说明「后果」具体是什么状态")
		}
	}
	if t.Objective.Mode != "" && t.Objective.Mode != "all" && t.Objective.Mode != "any" {
		return fmt.Errorf("objective.mode 只能是 all/any，得到 %q", t.Objective.Mode)
	}
	for i, p := range t.Objective.Probes {
		if err := checkProbe(fmt.Sprintf("objective.probes[%d]", i), p); err != nil {
			return err
		}
	}

	if t.Scoring.Mode == ScoringMilestone && len(t.Milestones) == 0 {
		return fmt.Errorf("scoring.mode=milestone 必须给至少一段 milestones")
	}
	seenMs := map[string]*Milestone{}
	for i := range t.Milestones {
		m := &t.Milestones[i]
		if m.ID == "" {
			return fmt.Errorf("milestones[%d].id 必填", i)
		}
		if seenMs[m.ID] != nil {
			return fmt.Errorf("milestone id 重复: %s", m.ID)
		}
		if m.Points <= 0 {
			return fmt.Errorf("milestone %s 的 points 必须为正", m.ID)
		}
		if m.Name == "" {
			return fmt.Errorf("milestone %s 缺 name", m.ID)
		}
		seenMs[m.ID] = m
		if err := checkProbe("milestone "+m.ID, m.Probe); err != nil {
			return err
		}
	}
	// requires 必须存在且无环：有环的里程碑永远拿不到分，会在跑完之后才暴露。
	for _, m := range t.Milestones {
		for _, req := range m.Requires {
			if seenMs[req] == nil {
				return fmt.Errorf("milestone %s 依赖不存在的里程碑 %s", m.ID, req)
			}
		}
	}
	state := map[string]int{} // 0 未访问 1 访问中 2 完成
	var visit func(id string) error
	visit = func(id string) error {
		switch state[id] {
		case 1:
			return fmt.Errorf("milestone 依赖成环，涉及 %s", id)
		case 2:
			return nil
		}
		state[id] = 1
		for _, req := range seenMs[id].Requires {
			if err := visit(req); err != nil {
				return err
			}
		}
		state[id] = 2
		return nil
	}
	for _, m := range t.Milestones {
		if err := visit(m.ID); err != nil {
			return err
		}
	}
	// 累计分口径要求里程碑是一条严格递增的链，否则「累计」没有定义。
	if t.Scoring.IsCumulative() {
		if _, err := t.ChainOrder(); err != nil {
			return err
		}
	}

	if t.Layer == LayerDefense {
		if t.DefenseProfile == nil {
			return fmt.Errorf("layer=3 必须声明 defense_profile（防御层任务没有防御画像就测不出对抗生存）")
		}
		if t.DefenseProfile.ID == "" || t.DefenseProfile.Kind == "" {
			return fmt.Errorf("defense_profile 的 id/kind 必填")
		}
	}
	if t.Calibration != nil && t.Calibration.ExpectAchieved != nil && t.Scoring.Mode != ScoringOutcome {
		return fmt.Errorf("calibration.expect_achieved 只对 scoring.mode=outcome 有意义")
	}
	return nil
}

func (s ScoringSpec) validate() error {
	switch s.Mode {
	case ScoringMilestone, ScoringOutcome:
	case "":
		return fmt.Errorf("scoring.mode 必填（milestone/outcome）")
	default:
		return fmt.Errorf("scoring.mode 只能是 milestone/outcome，得到 %q", s.Mode)
	}
	switch s.PointsMode {
	case "", "additive", "cumulative":
	default:
		return fmt.Errorf("scoring.points_mode 只能是 additive/cumulative，得到 %q", s.PointsMode)
	}
	return nil
}

// IsCumulative 判断是否使用累计分口径。
func (s ScoringSpec) IsCumulative() bool { return s.PointsMode == "cumulative" }

func (t TargetSpec) validate() error {
	switch t.Kind {
	case "exec":
		if len(t.Cmd) == 0 {
			return fmt.Errorf("target.kind=exec 必须给 cmd")
		}
	case "static":
		if t.BaseURL == "" {
			return fmt.Errorf("target.kind=static 必须给 base_url")
		}
	default:
		return fmt.Errorf("target.kind 必须是 exec/static，得到 %q", t.Kind)
	}
	switch t.Ready.Kind {
	case "tcp_dial":
		if t.Ready.Addr == "" {
			return fmt.Errorf("target.ready.kind=tcp_dial 必须给 addr")
		}
	case "http":
		if t.Ready.URL == "" {
			return fmt.Errorf("target.ready.kind=http 必须给 url")
		}
	default:
		return fmt.Errorf("target.ready.kind 必须是 tcp_dial/http，得到 %q", t.Ready.Kind)
	}
	return nil
}

func (f FreezeSpec) validate() error {
	switch f.Kind {
	case "manifest", "image_digest", "config_hash", "synthetic":
	default:
		return fmt.Errorf("freeze.kind 必须是 manifest/image_digest/config_hash/synthetic，得到 %q", f.Kind)
	}
	if f.Ref == "" {
		return fmt.Errorf("freeze.ref 必填（提交 SHA / 镜像 digest / 配置指纹）")
	}
	if len(f.Artifacts) == 0 {
		return fmt.Errorf("freeze.artifacts 不能为空：没有冻结物的靶无法防污染")
	}
	seen := map[string]bool{}
	for _, a := range f.Artifacts {
		if a.Path == "" {
			return fmt.Errorf("freeze.artifacts 里有空 path")
		}
		if filepath.IsAbs(a.Path) || strings.HasPrefix(a.Path, "..") {
			return fmt.Errorf("freeze.artifacts.path 必须是仓库相对路径: %s", a.Path)
		}
		// sha256 可留空：留空表示「以 freeze.lock 为准」（锁是唯一事实来源，
		// 避免同一份哈希在题面和锁里各写一遍、改一处忘一处）。
		// 写了就必须与锁一致，用于显式钉版本。
		if seen[a.Path] {
			return fmt.Errorf("freeze.artifacts.path 重复: %s", a.Path)
		}
		seen[a.Path] = true
	}
	return nil
}

func (p Probe) validate(where string) error {
	timeoutOK := func() error {
		if p.TimeoutMS < 0 {
			return fmt.Errorf("%s: timeout_ms 不能为负", where)
		}
		return nil
	}
	switch p.Kind {
	case ProbeHTTPResponse:
		if p.URL == "" {
			return fmt.Errorf("%s: http_response probe 必须给 url", where)
		}
		if len(p.ExpectStatus) == 0 && len(p.ExpectContains) == 0 && len(p.ExpectNotContains) == 0 {
			return fmt.Errorf("%s: http_response probe 至少要有一条判据（expect_status/expect_contains/expect_not_contains），否则恒真", where)
		}
	case ProbeFileRead:
		if p.Path == "" {
			return fmt.Errorf("%s: file_read probe 必须给 path", where)
		}
		if len(p.ExpectContains) == 0 && len(p.ExpectNotContains) == 0 {
			return fmt.Errorf("%s: file_read probe 至少要有一条判据，否则恒真", where)
		}
	case ProbeTCPConnect:
		if len(p.ExpectClientPayloadContains) == 0 && p.Banner == "" {
			return fmt.Errorf("%s: tcp_connect probe 至少要能观测到一点东西（banner 或 expect_client_payload_contains）", where)
		}
	default:
		return fmt.Errorf("%s: probe.kind 必须是 %s/%s/%s，得到 %q", where, ProbeHTTPResponse, ProbeFileRead, ProbeTCPConnect, p.Kind)
	}
	return timeoutOK()
}

// MaxPoints 返回里程碑满分。累计分口径下满分是链尾分数，不是各段之和。
func (t *Task) MaxPoints() int {
	if len(t.Milestones) == 0 {
		return 0
	}
	if t.Scoring.IsCumulative() {
		chain, err := t.ChainOrder()
		if err == nil && len(chain) > 0 {
			return chain[len(chain)-1].Points
		}
		// 校验未通过时退化为最大值，避免除零；正常运行不会走到这里。
		max := 0
		for _, m := range t.Milestones {
			if m.Points > max {
				max = m.Points
			}
		}
		return max
	}
	sum := 0
	for _, m := range t.Milestones {
		sum += m.Points
	}
	return sum
}

// ChainOrder 返回累计分口径下的里程碑链（头 → 尾）。
// 累计分只有在「单链、分数严格递增」时才有唯一解释：第 N 段的分数就是打到第 N 段
// 时应该拿到的总分。分叉或同分都会让同一个分数对应多种进度，那种数字不可比。
func (t *Task) ChainOrder() ([]Milestone, error) {
	if len(t.Milestones) == 0 {
		return nil, nil
	}
	byID := map[string]*Milestone{}
	dependents := map[string][]string{}
	for i := range t.Milestones {
		m := &t.Milestones[i]
		byID[m.ID] = m
		for _, req := range m.Requires {
			dependents[req] = append(dependents[req], m.ID)
		}
	}
	var heads []string
	for _, m := range t.Milestones {
		if len(m.Requires) == 0 {
			heads = append(heads, m.ID)
		}
	}
	if len(heads) != 1 {
		return nil, fmt.Errorf("points_mode=cumulative 要求里程碑是一条链：头必须唯一，实际 %d 个（%v）", len(heads), heads)
	}
	for id, deps := range dependents {
		if len(deps) > 1 {
			return nil, fmt.Errorf("points_mode=cumulative 要求单链，里程碑 %s 有多个后继 %v", id, deps)
		}
	}
	chain := make([]Milestone, 0, len(t.Milestones))
	cur := heads[0]
	for cur != "" {
		m, ok := byID[cur]
		if !ok {
			return nil, fmt.Errorf("里程碑链断在 %s", cur)
		}
		chain = append(chain, *m)
		next := dependents[cur]
		if len(next) == 0 {
			break
		}
		cur = next[0]
	}
	if len(chain) != len(t.Milestones) {
		return nil, fmt.Errorf("points_mode=cumulative 要求单链，链上 %d 段但共声明 %d 段", len(chain), len(t.Milestones))
	}
	for i := 1; i < len(chain); i++ {
		if chain[i].Points <= chain[i-1].Points {
			return nil, fmt.Errorf("points_mode=cumulative 要求分数严格递增：%s(%d) 之后是 %s(%d)",
				chain[i-1].ID, chain[i-1].Points, chain[i].ID, chain[i].Points)
		}
	}
	return chain, nil
}

// DefenseKind 返回防御画像类型，未声明时返回 "none"。
func (t *Task) DefenseKind() string {
	if t.DefenseProfile == nil || t.DefenseProfile.Kind == "" {
		return "none"
	}
	return t.DefenseProfile.Kind
}
