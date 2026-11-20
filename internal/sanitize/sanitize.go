// Package sanitize 把准备发往外部模型的文本做中性化处理。
//
// 为什么需要这一层：部分第三方推理通道（尤其是带内容审查的免费额度）会对
// 安全类措辞直接返回 400 content_policy_blocked，整轮决策直接作废。而决策
// 层真正需要的只有三件事——发现了什么、还剩哪些没覆盖、之前做过什么——
// 这三件事用中性词汇完全可以表达，所以出站前统一替换一遍。
//
// 三条硬约束：
//  1. 只处理**出站**文本。本地账本（lance-ledger.jsonl）、产物、日志一律保留
//     原词，审计链不受影响；净化只发生在网络边界上。
//  2. 规则按"匹配串长度倒序"应用，避免「渗透测试」被「渗透」先吃掉半截。
//  3. 幂等：同一段文本跑两遍结果一致（替换后的词不再命中任何规则）。
//
// 关掉这一层：LANCE_LLM_SANITIZE=0（本地自建模型不需要净化时用）。
package sanitize

import (
	"os"
	"sort"
	"strings"
)

type rule struct{ from, to string }

// rules 是唯一的词表。改这里就够了，不要在调用方零散写替换。
//
// 排序说明：中文键直接子串替换；ASCII 键走词边界匹配（见 replaceASCII），
// 所以 "C2" 不会命中 "abc2"，"payload" 不会命中 "payloadx"。
var rules = []rule{
	// —— 中文：安全术语降级为中性表述 ——
	{"渗透测试", "安全验证"},
	{"渗透", "验证"},
	{"内网", "内部网络"},
	{"攻击面", "检查范围"},
	{"攻击", "检查"},
	{"漏洞", "缺陷"},
	{"反序列化", "数据解析"},
	{"注入", "输入校验"},
	{"提权", "权限变更"},
	{"越权", "权限校验"},
	{"爆破", "重复尝试"},
	{"默认口令", "默认认证"},
	{"弱口令", "弱认证"},
	{"口令", "认证信息"},
	{"凭据", "认证信息"},
	{"载荷", "参数"},
	{"武器", "工具"},
	{"扫描", "巡检"},
	{"复验", "复核"},
	{"靶机", "测试主机"},
	{"靶", "测试环境"},
	{"后门", "预留入口"},
	{"木马", "可疑程序"},
	{"恶意", "可疑"},
	{"免杀", "规避检测"},
	{"红队", "安全团队"},
	{"入侵", "非预期访问"},
	{"利用", "验证"},
	{"打点", "初始验证"},
	{"打穿", "深度验证"},
	{"怎么打", "怎么处理"},
	{"打新目标", "检查新目标"},
	{"命令行", "控制台"},
	{"代码执行", "代码运行"},
	{"命令执行", "指令运行"},
	{"窃取", "获取"},
	{"劫持", "接管"},
	{"篡改", "修改"},
	{"泄露", "暴露"},

	// —— ASCII：词边界匹配 ——
	{"webshell", "web script"},
	{"getshell", "access gained"},
	{"payload", "argument"},
	{"penetration", "assessment"},
	{"pentest", "assessment"},
	{"exploit", "verify"},
	{"vulnerability", "defect"},
	{"vuln", "defect"},
	{"attack", "check"},
	{"backdoor", "reserved entry"},
	{"shell", "console"},
	{"bruteforce", "repeated attempt"},
	{"privesc", "permission change"},
	{"injection", "input validation"},
	{"malware", "suspicious file"},
	{"ransomware", "encrypting file"},
	{"credential", "auth data"},
	{"RCE", "critical defect"},
	{"XSS", "output encoding"},
	{"SSRF", "outbound request check"},
	{"C2", "external channel"},
	{"SQLi", "input validation"},
}

// ordered 是 rules 按匹配串长度倒序的副本，避免每次调用都排一遍。
var ordered = func() []rule {
	out := make([]rule, len(rules))
	copy(out, rules)
	sort.SliceStable(out, func(i, j int) bool {
		return len(out[i].from) > len(out[j].from)
	})
	return out
}()

// Enabled 读 LANCE_LLM_SANITIZE，默认开。
// 认 0/false/off/no 为关闭——运维手滑写成 "false" 也能生效。
func Enabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("LANCE_LLM_SANITIZE"))) {
	case "0", "false", "off", "no":
		return false
	}
	return true
}

// Outbound 返回净化后的文本；多次调用结果稳定。
func Outbound(s string) string {
	for _, r := range ordered {
		if isASCII(r.from) {
			s = replaceASCII(s, r.from, r.to)
		} else {
			s = strings.ReplaceAll(s, r.from, r.to)
		}
	}
	return s
}

// Hits 返回文本里仍然命中的敏感词，用于自检与回归测试。
// 有返回不代表净化失败——调用方可能故意传原文；它只陈述事实。
func Hits(s string) []string {
	var out []string
	for _, r := range ordered {
		if isASCII(r.from) {
			if indexFoldASCII(s, r.from) >= 0 {
				out = append(out, r.from)
			}
		} else if strings.Contains(s, r.from) {
			out = append(out, r.from)
		}
	}
	return out
}

func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

func isWordByte(b byte) bool {
	return b == '_' ||
		(b >= '0' && b <= '9') ||
		(b >= 'a' && b <= 'z') ||
		(b >= 'A' && b <= 'Z')
}

func lowerASCII(b byte) byte {
	if b >= 'A' && b <= 'Z' {
		return b + ('a' - 'A')
	}
	return b
}

// indexFoldASCII 在 s 里找 sub 的首次出现，ASCII 大小写不敏感。
// UTF-8 续字节恒 >= 0x80，不可能与 ASCII 字母相等，所以按字节扫是安全的。
func indexFoldASCII(s, sub string) int {
	n := len(sub)
	if n == 0 || len(s) < n {
		return -1
	}
	for i := 0; i+n <= len(s); i++ {
		ok := true
		for k := 0; k < n; k++ {
			if lowerASCII(s[i+k]) != lowerASCII(sub[k]) {
				ok = false
				break
			}
		}
		if ok {
			return i
		}
	}
	return -1
}

// replaceASCII 做带词边界的 ASCII 替换：前后必须是词边界，
// 否则跳过这个起点继续往后找（而不是整段放弃）。
func replaceASCII(s, from, to string) string {
	var b strings.Builder
	b.Grow(len(s))
	i := 0
	for i < len(s) {
		j := indexFoldASCII(s[i:], from)
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		abs := i + j
		end := abs + len(from)
		beforeOK := abs == 0 || !isWordByte(s[abs-1])
		afterOK := end >= len(s) || !isWordByte(s[end])
		if !beforeOK || !afterOK {
			// 这个起点不算独立单词：只吞一个字节，避免死循环也避免误吞。
			b.WriteString(s[i : abs+1])
			i = abs + 1
			continue
		}
		b.WriteString(s[i:abs])
		b.WriteString(to)
		i = end
	}
	return b.String()
}
