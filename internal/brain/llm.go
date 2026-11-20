package brain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"lance/internal/action"
	"lance/internal/sanitize"
)

// LLM 走 OpenAI 兼容的 /chat/completions，只用标准库。
//
// 两条铁律：
//  1. 模型的输出当**不可信输入**处理 —— 解析成 action 后必须过 Validate，
//     任何字段越界整批拒掉，绝不"尽量执行能执行的部分"。
//  2. 两次都拿不到合法 JSON 就报错退出，由调用方回落到 Rules。
//     宁可这轮不编排，也不能让一个抽风的决策驱动扫描。
type LLM struct {
	BaseURL string
	APIKey  string
	Model   string
	HTTP    *http.Client
}

func (l LLM) Name() string { return "llm:" + l.Model }

// userAgent 是出站请求的固定标识。网关的边缘策略会看它，见 chat() 里的注释。
const userAgent = "lance-orchestrator/1.3"

const schemaDoc = `你要输出的 JSON 形如：
{"actions":[{"kind":"replay|run_tpl|stop","target":"<可选>","args":{...},"why":"<必填，一句话理由>","conf":0.0~1.0}]}

动作白名单（不在其中的一律不许输出）：
- replay  : args={"id":"<已有发现的 ID>"}          —— 复核一条已有发现
- run_tpl : args={"target":"<host:port>","tpl":"<模板目录>"} —— 用模板检查新目标
- stop    : 无参数                                  —— 收工

禁止事项：不要输出命令、控制台语句、URL 路径、参数、任何执行细节。
你只负责选路和排序，具体怎么执行由执行层决定。`

func (l LLM) Decide(ctx context.Context, in Input) ([]action.Action, error) {
	prompt := l.buildPrompt(in)
	var lastErr error
	for attempt := 1; attempt <= 2; attempt++ {
		raw, err := l.chat(ctx, prompt)
		if err != nil {
			return nil, err
		}
		acts, err := parseActions(raw)
		if err == nil {
			return acts, nil
		}
		lastErr = err
		prompt = l.buildPrompt(in) + "\n\n你上一次的输出不合规：" + err.Error() +
			"\n只输出一个 JSON 对象，不要 markdown 代码块，不要任何解释文字。"
	}
	return nil, fmt.Errorf("两次尝试都没拿到合法动作: %w", lastErr)
}

func (l LLM) buildPrompt(in Input) string {
	on := sanitize.Enabled()
	clean := func(s string) string {
		if on {
			return sanitize.Outbound(s)
		}
		return s
	}
	var b strings.Builder
	b.WriteString("你是一次授权内部安全评估的调度者。你的唯一职责是决定下一步做什么，")
	b.WriteString("不是判断缺陷真假（那由执行层的匹配器负责，你看到的 severity 已经是结论）。\n\n")
	b.WriteString(schemaDoc)
	b.WriteString("\n\n当前可用信息：\n")
	b.WriteString(fmt.Sprintf("预算：本轮最多 %d 个动作\n", in.Budget))
	b.WriteString("已有发现：\n")
	for _, f := range in.Findings {
		b.WriteString(fmt.Sprintf("  %s | %s | severity=%s level=%s conf=%s | %s\n",
			f.ID, clean(f.Check), f.Severity, f.Level, f.Conf, f.Target))
	}
	b.WriteString("覆盖表：\n")
	for _, c := range in.Coverage {
		b.WriteString(fmt.Sprintf("  %s | %s | %s\n", clean(c.Item), c.State, clean(c.Reason)))
	}
	b.WriteString("已经执行过的动作（不要重复）：\n")
	for _, h := range in.History {
		b.WriteString("  " + clean(h) + "\n")
	}
	b.WriteString("\n只输出 JSON。")
	p := b.String()
	if !on {
		return p
	}
	// 逐字段净化之后再过一遍整段：字段拼接可能把两个中性词拼出敏感组合，
	// 也可能是调用方自己塞进来的自由文本。整段兜底是最后一道闸。
	return sanitize.Outbound(p)
}

// RenderPrompt 用零配置渲染一次出站 prompt，供 `lance prompt` 人工核查。
// 它不读环境变量、不发网络请求，纯函数，可以放心在测试里断言。
func RenderPrompt(in Input) string { return LLM{}.buildPrompt(in) }

// SensitiveHits 透出词表自检能力，让命令行能对出站内容做断言。
func SensitiveHits(s string) []string { return sanitize.Hits(s) }

type chatReq struct {
	Model    string    `json:"model"`
	Messages []chatMsg `json:"messages"`
	Temp     float64   `json:"temperature"`
}

type chatMsg struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatResp struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (l LLM) chat(ctx context.Context, prompt string) (string, error) {
	if l.BaseURL == "" || l.Model == "" {
		return "", errors.New("LLM brain 未配置（需要 base_url / model）")
	}
	body, _ := json.Marshal(chatReq{
		Model:    l.Model,
		Temp:     0.2,
		Messages: []chatMsg{{Role: "user", Content: prompt}},
	})
	url := strings.TrimRight(l.BaseURL, "/") + "/chat/completions"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	// 固定 UA：部分推理网关在前面挂 Cloudflare，按 UA 做边缘策略，
	// Go 默认的 "Go-http-client/1.1" 会被直接 403（返回一张 HTML 挑战页，
	// 看起来像鉴权失败，其实是没到应用层）。给一个明确的产品标识。
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	if l.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+l.APIKey)
	}
	cli := l.HTTP
	if cli == nil {
		cli = &http.Client{Timeout: 60 * time.Second}
	}
	resp, err := cli.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, truncate(string(b), 300))
	}
	var cr chatResp
	if err := json.Unmarshal(b, &cr); err != nil {
		return "", err
	}
	if cr.Error != nil {
		return "", errors.New(cr.Error.Message)
	}
	if len(cr.Choices) == 0 {
		return "", errors.New("模型没有返回任何 choice")
	}
	return cr.Choices[0].Message.Content, nil
}

// parseActions 容错解析：模型经常裹一层 ```json，剥掉再解。
func parseActions(raw string) ([]action.Action, error) {
	s := strings.TrimSpace(raw)
	if i := strings.Index(s, "```"); i >= 0 {
		s = s[i+3:]
		s = strings.TrimPrefix(s, "json")
		if j := strings.Index(s, "```"); j >= 0 {
			s = s[:j]
		}
	}
	if i, j := strings.Index(s, "{"), strings.LastIndex(s, "}"); i >= 0 && j > i {
		s = s[i : j+1]
	}
	var wrap struct {
		Actions []action.Action `json:"actions"`
	}
	if err := json.Unmarshal([]byte(s), &wrap); err != nil {
		return nil, fmt.Errorf("不是合法 JSON: %w", err)
	}
	if len(wrap.Actions) == 0 {
		return nil, errors.New("actions 为空")
	}
	if err := action.ValidateAll(wrap.Actions); err != nil {
		return nil, err
	}
	return wrap.Actions, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// envAny 按顺序取第一个非空的环境变量：新名优先，旧名兜底。
func envAny(keys ...string) string {
	for _, k := range keys {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return ""
}

// EnvLLM 从环境变量读配置；没配就返回 false，调用方回落到 Rules。
// 主名 LANCE_LLM_*，依次兼容改名前的 LAUGHING_LLM_* 与更早的 ORCH_LLM_*，
// 老脚本与已有 shell 配置不用跟着改 —— env 静默失效是最难查的那类 bug。
func EnvLLM() (LLM, bool) {
	key := envAny("LANCE_LLM_KEY", "LAUGHING_LLM_KEY", "ORCH_LLM_KEY")
	base := envAny("LANCE_LLM_BASE", "LAUGHING_LLM_BASE", "ORCH_LLM_BASE")
	model := envAny("LANCE_LLM_MODEL", "LAUGHING_LLM_MODEL", "ORCH_LLM_MODEL")
	if base == "" || model == "" {
		return LLM{}, false
	}
	return LLM{BaseURL: base, APIKey: key, Model: model}, true
}
