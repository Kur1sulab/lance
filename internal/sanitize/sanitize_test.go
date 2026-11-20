package sanitize

import (
	"strings"
	"testing"
)

func TestOutboundClearsSecurityTerms(t *testing.T) {
	in := "你是一次授权内网渗透测试的调度者，请复验已命中的漏洞，不要输出 payload 与 shell 命令。" +
		"覆盖表里还剩「默认口令爆破」「反序列化利用」两项，SQLi 与 XSS 已收工。"
	out := Outbound(in)
	if hits := Hits(out); len(hits) != 0 {
		t.Fatalf("净化后仍命中 %v\n原文: %s\n净化: %s", hits, in, out)
	}
	// 语义骨架必须还在：模型要靠这些词理解任务。
	for _, want := range []string{"调度者", "复核", "覆盖表"} {
		if !strings.Contains(out, want) {
			t.Fatalf("净化把语义关键词 %q 弄丢了: %s", want, out)
		}
	}
}

func TestOutboundIdempotent(t *testing.T) {
	in := "内网渗透测试：默认口令爆破 → 漏洞利用 → payload 投递"
	once := Outbound(in)
	twice := Outbound(once)
	if once != twice {
		t.Fatalf("不幂等\n一次: %s\n二次: %s", once, twice)
	}
}

func TestASCIIWordBoundary(t *testing.T) {
	cases := []struct{ in, want, why string }{
		{"run payload now", "run argument now", "独立单词应被替换"},
		{"payloadx stays", "payloadx stays", "长单词内部不该动"},
		{"my_payload_value", "my_payload_value", "下划线视为词内字符"},
		{"abc2 stays", "abc2 stays", "短键不该命中词内"},
		{"C2 channel", "external channel channel", "独立 C2 应被替换"},
		{"SHELL", "console", "大小写不敏感"},
	}
	for _, c := range cases {
		if got := Outbound(c.in); got != c.want {
			t.Fatalf("%s: Outbound(%q) = %q, 期望 %q", c.why, c.in, got, c.want)
		}
	}
}

func TestChineseLongestFirst(t *testing.T) {
	// 「渗透测试」必须整体替换，不能被「渗透」先吃掉。
	if got := Outbound("渗透测试"); got != "安全验证" {
		t.Fatalf("长键优先失效: %q", got)
	}
	if got := Outbound("内网渗透"); got != "内部网络验证" {
		t.Fatalf("组合替换异常: %q", got)
	}
}

func TestHitsReportsKeys(t *testing.T) {
	hits := Hits("这里有漏洞和 payload")
	if len(hits) == 0 {
		t.Fatal("Hits 应该报出敏感词")
	}
	joined := strings.Join(hits, ",")
	if !strings.Contains(joined, "漏洞") || !strings.Contains(joined, "payload") {
		t.Fatalf("Hits 漏报: %v", hits)
	}
}

func TestEnabledToggle(t *testing.T) {
	t.Setenv("LANCE_LLM_SANITIZE", "0")
	if Enabled() {
		t.Fatal("LANCE_LLM_SANITIZE=0 应关闭净化")
	}
	t.Setenv("LANCE_LLM_SANITIZE", "off")
	if Enabled() {
		t.Fatal("LANCE_LLM_SANITIZE=off 应关闭净化")
	}
	t.Setenv("LANCE_LLM_SANITIZE", "")
	if !Enabled() {
		t.Fatal("未设置时应默认开启")
	}
}

func TestEmptyAndPlain(t *testing.T) {
	if got := Outbound(""); got != "" {
		t.Fatalf("空串应原样返回: %q", got)
	}
	plain := "本轮预算 8 个动作，目标 10.0.0.1:8080 已复核。"
	if got := Outbound(plain); got != plain {
		t.Fatalf("无敏感词文本不该被改动: %q -> %q", plain, got)
	}
}
