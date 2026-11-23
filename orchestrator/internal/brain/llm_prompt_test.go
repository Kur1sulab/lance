package brain

import (
	"strings"
	"testing"
)

// 这组用例守的是「出站不出事」这条线：prompt 拼出来的每一个字节都会经过
// 第三方通道的内容审查，任何一个敏感词都会让整轮决策返回 400。
//
// 断言方式是黑盒的——不看实现怎么拼，只看最终字符串有没有命中词表。
func TestRenderPromptIsClean(t *testing.T) {
	t.Setenv("LANCE_LLM_SANITIZE", "1")
	in := Input{
		Budget: 8,
		Findings: []Finding{
			{ID: "LS-001", Check: "Apache Shiro 默认密钥（反序列化利用）", Severity: "high", Level: "L2", Conf: "0.92", Target: "10.0.0.11:8080"},
			{ID: "LS-002", Check: "Nacos 默认口令登录", Severity: "critical", Level: "L3", Conf: "0.98", Target: "10.0.0.12:8848"},
			{ID: "LS-003", Check: "Struts2 远程代码执行", Severity: "critical", Level: "L3", Conf: "0.71", Target: "10.0.0.12:8080"},
		},
		Coverage: []Coverage{
			{Item: "内网横向渗透", State: "uncovered", Reason: "未开始"},
			{Item: "口令爆破", State: "covered", Reason: "已复验 3 条"},
		},
		History: []string{"replay::LS-001 复验成功", "run_tpl::10.0.0.13:22 payload 已就位"},
	}
	p := RenderPrompt(in)
	if hits := SensitiveHits(p); len(hits) > 0 {
		t.Fatalf("出站 prompt 命中敏感词 %v\n--- prompt ---\n%s", hits, p)
	}
}

// 净化只动措辞，不能动决策所需的字段。
func TestRenderPromptKeepsDecisionFields(t *testing.T) {
	t.Setenv("LANCE_LLM_SANITIZE", "1")
	in := Input{
		Budget: 5,
		Findings: []Finding{
			{ID: "LS-007", Check: "默认口令", Severity: "high", Level: "L2", Conf: "0.9", Target: "10.1.2.3:6379"},
		},
	}
	p := RenderPrompt(in)
	for _, want := range []string{"LS-007", "high", "L2", "0.9", "10.1.2.3:6379", "预算：本轮最多 5 个动作"} {
		if !strings.Contains(p, want) {
			t.Fatalf("净化弄丢了决策字段 %q\n%s", want, p)
		}
	}
}

// 关掉净化时必须是原文，方便对接本地自建模型做 A/B 对比。
func TestRenderPromptRespectsToggle(t *testing.T) {
	t.Setenv("LANCE_LLM_SANITIZE", "0")
	in := Input{Findings: []Finding{{ID: "LS-001", Check: "漏洞", Severity: "high", Target: "x"}}}
	p := RenderPrompt(in)
	if !strings.Contains(p, "漏洞") {
		t.Fatalf("LANCE_LLM_SANITIZE=0 时不该替换，prompt:\n%s", p)
	}
}

// schemaDoc 自身不能带敏感词——它是每个 prompt 的固定前缀。
func TestSchemaDocIsClean(t *testing.T) {
	if hits := SensitiveHits(schemaDoc); len(hits) > 0 {
		t.Fatalf("schemaDoc 命中敏感词 %v", hits)
	}
}

// 提示词必须仍能让模型理解任务：净化不能把关键动词洗没。
func TestRenderPromptStillInstructs(t *testing.T) {
	t.Setenv("LANCE_LLM_SANITIZE", "1")
	p := RenderPrompt(Input{Budget: 3})
	for _, want := range []string{"调度者", "动作白名单", "stop", "只输出 JSON"} {
		if !strings.Contains(p, want) {
			t.Fatalf("prompt 缺少必要指令 %q", want)
		}
	}
}
