package report

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/Kur1sulab/lance/db"
)

// 漏洞页面「导出」用渲染：将多条 findings 表行汇总为 Markdown 摘要，
// 渲染为独立 Markdown 或 CSV。JSON 由 server 层直接序列化为 DTO，因此
// 此处不处理。

// sortFindingsForExport 按严重度降序、再按时间倒序排序。与摘要报告的分组一致。
func sortFindingsForExport(fs []*db.DBFinding) {
	sort.SliceStable(fs, func(i, j int) bool {
		ri, rj := sevRank[fs[i].Severity], sevRank[fs[j].Severity]
		if ri != rj {
			return ri < rj // sevRank 值越小越严重
		}
		return fs[i].CreatedAt.After(fs[j].CreatedAt)
	})
}

// VerificationMeta 是写进报告头部的验证质量元数据（6.2 一等指标）。
// 它跟着报告走，而不是只留在平台 API 里：报告一旦脱库，收件人必须能看见
// 「这批结论的可重放率/误报率是多少、判定依据是什么」。
type VerificationMeta struct {
	Metrics          db.VerificationMetrics
	ExportCount      int
	ExportReplayable int
}

// Header 渲染报告头部的验证质量块。口径与 db.ComputeVerificationMetrics 同源。
func (m *VerificationMeta) Header() string {
	if m == nil {
		return ""
	}
	var b strings.Builder
	b.WriteString("> **验证质量（6.2 闸门）**\n>\n")
	fmt.Fprintf(&b, "> - 可重放率 **%.2f%%**（%d/%d 条 verified 能凭证据重放成功）\n",
		m.Metrics.ReplayableRate*100, m.Metrics.Replayable, m.Metrics.ReplaySample)
	fmt.Fprintf(&b, "> - 误报率 **%.2f%%**（%d/%d，refuted ÷ (verified+refuted)）\n",
		m.Metrics.FalsePositiveRate*100, m.Metrics.Refuted, m.Metrics.FalsePositiveSample)
	fmt.Fprintf(&b, "> - 库内样本：verified %d · refuted %d · unreproducible %d · unverified %d（共 %d）\n",
		m.Metrics.Verified, m.Metrics.Refuted, m.Metrics.Unreproducible, m.Metrics.Unverified, m.Metrics.Total)
	fmt.Fprintf(&b, "> - 本报告 %d 条（全部为 verified），其中可重放 %d 条\n", m.ExportCount, m.ExportReplayable)
	b.WriteString("> - 判定依据：**环境 ground-truth**（按证据重放实测 / 独立复现 / 目标真实响应）。" +
		"LLM 或 Agent 自报不作为判定依据。\n>\n")
	b.WriteString("> 分母为 0 时比率记 0；样本数为 0 的指标不具统计意义，勿单独引用。\n\n")
	return b.String()
}

// verificationLine 是单条发现里的验证状态行（zip 单文件报告用）。
func verificationLine(f *db.DBFinding) string {
	st := nz(f.VerificationStatus, db.VerificationUnverified)
	if st != db.VerificationVerified {
		// 理论上进不了导出（导出路径已硬拦），出现就说明有人绕过了闸门，
		// 报告里必须显式暴露，而不是假装没事。
		return fmt.Sprintf("- **验证状态**：%s（未过验证闸门，不应出现在本报告中）\n", st)
	}
	return "- **验证状态**：verified（环境 ground-truth 判定）\n"
}

// findingTitle 选取漏洞的可读标题：名称 → 类型 →「未分类」。
func findingTitle(f *db.DBFinding) string {
	return nz(f.Name, nz(f.VulnClass, "未分类"))
}

// FindingsMarkdown 将多条 findings 整合为一份摘要报告(摘要 + 按严重程度
// 分组，每条包含类型/状态/所属任务/证据/详细报告）。
func FindingsMarkdown(fs []*db.DBFinding, generatedAt time.Time, meta *VerificationMeta) string {
	items := append([]*db.DBFinding(nil), fs...)
	sortFindingsForExport(items)

	var b strings.Builder
	b.WriteString("# 漏洞摘要报告\n\n")
	b.WriteString(meta.Header())
	fmt.Fprintf(&b, "- **创建时间**：%s\n", generatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- **漏洞总数**: %d 个\n\n", len(items))

	// 摘要：按严重度聚合。
	counts := map[string]int{}
	for _, f := range items {
		counts[f.Severity]++
	}
	b.WriteString("## 摘要\n\n")
	b.WriteString("| 严重程度 | 数量 |\n| --- | --- |\n")
	for _, s := range []struct{ key, label string }{
		{"critical", "严重"}, {"high", "高"}, {"medium", "中"}, {"low", "低"},
	} {
		fmt.Fprintf(&b, "| %s | %d |\n", s.label, counts[s.key])
	}
	b.WriteString("\n")

	if len(items) == 0 {
		b.WriteString("_没有匹配的漏洞。_\n")
		return b.String()
	}

	b.WriteString("## 漏洞详情\n\n")
	for i, f := range items {
		fmt.Fprintf(&b, "### %d. [%s] %s\n\n", i+1, strings.ToUpper(nz(f.Severity, "info")), findingTitle(f))
		if f.VulnClass != "" {
			fmt.Fprintf(&b, "- **类型**: %s\n", f.VulnClass)
		}
		fmt.Fprintf(&b, "- **状态**：%s\n", nz(f.Status, "pending"))
		b.WriteString(verificationLine(f))
		if desc := strings.TrimSpace(f.TaskDescription); desc != "" {
			fmt.Fprintf(&b, "- **所属任务**：%s\n", desc)
		}
		fmt.Fprintf(&b, "- **发现时间**：%s\n\n", f.CreatedAt.Format("2006-01-02 15:04:05"))
		if s := strings.TrimSpace(f.Summary); s != "" {
			fmt.Fprintf(&b, "%s\n\n", s)
		}
		if e := strings.TrimSpace(f.Evidence); e != "" {
			fmt.Fprintf(&b, "**证据：**\n\n```\n%s\n```\n\n", e)
		}
		if rep := strings.TrimSpace(f.Report); rep != "" {
			b.WriteString("**详细报告：**\n\n")
			b.WriteString(rep)
			b.WriteString("\n\n")
		}
		b.WriteString(findingTrafficMarkdown(f, false))
		b.WriteString("---\n\n")
	}
	return b.String()
}

// SingleFindingMarkdown 将单个漏洞渲染为独立的 Markdown 单条(用于「一个漏洞一个文件」的打包)。
func SingleFindingMarkdown(f *db.DBFinding, generatedAt time.Time, meta *VerificationMeta) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# [%s] %s\n\n", strings.ToUpper(nz(f.Severity, "info")), findingTitle(f))
	if f.VulnClass != "" {
		fmt.Fprintf(&b, "- **类型**: %s\n", f.VulnClass)
	}
	fmt.Fprintf(&b, "- **严重程度**: %s\n", nz(f.Severity, "info"))
	fmt.Fprintf(&b, "- **状态**：%s\n", nz(f.Status, "pending"))
	b.WriteString(verificationLine(f))
	if desc := strings.TrimSpace(f.TaskDescription); desc != "" {
		fmt.Fprintf(&b, "- **所属任务**：%s\n", desc)
	}
	fmt.Fprintf(&b, "- **发现时间**：%s\n", f.CreatedAt.Format("2006-01-02 15:04:05"))
	fmt.Fprintf(&b, "- **创建时间**：%s\n\n", generatedAt.Format("2006-01-02 15:04:05"))
	if s := strings.TrimSpace(f.Summary); s != "" {
		fmt.Fprintf(&b, "## 概述\n\n%s\n\n", s)
	}
	if e := strings.TrimSpace(f.Evidence); e != "" {
		fmt.Fprintf(&b, "## 证据\n\n```\n%s\n```\n\n", e)
	}
	if rep := strings.TrimSpace(f.Report); rep != "" {
		b.WriteString("## 详细报告\n\n")
		b.WriteString(rep)
		b.WriteString("\n")
	}
	b.WriteString(findingTrafficMarkdown(f, true))
	return b.String()
}

var unsafeFilenameChars = regexp.MustCompile(`[^\p{Han}\p{L}\p{N}._-]+`)

// FindingFilename 为「每个漏洞一个文件」生成安全的 .md 文件名。
// 例：`critical_SQL注入_#123.md`。移除路径分隔符与控制字符，防止 zip 内出现异常路径。
func FindingFilename(f *db.DBFinding) string {
	sev := nz(f.Severity, "info")
	title := findingTitle(f)
	name := fmt.Sprintf("%s_%s_#%d", sev, title, f.ID)
	name = unsafeFilenameChars.ReplaceAllString(name, "_")
	name = strings.Trim(name, "._")
	if name == "" {
		name = fmt.Sprintf("finding_%d", f.ID)
	}
	// 防御性处理：再剥离一次路径以阻断 zip slip。
	name = path.Base(name)
	if len(name) > 120 {
		name = name[:120]
	}
	return name + ".md"
}

// FindingsCSV 将多条 findings 渲染为 CSV(包含 UTF-8 BOM，Excel 对韩文
// 以便正确识别）。不放入长篇 report/evidence 全文，只放摘要性质的字段。
// 需要全文时导出为 Markdown/JSON。
func FindingsCSV(fs []*db.DBFinding, meta *VerificationMeta) []byte {
	items := append([]*db.DBFinding(nil), fs...)
	sortFindingsForExport(items)

	var buf bytes.Buffer
	buf.WriteString("\xEF\xBB\xBF") // UTF-8 BOM
	if meta != nil {
		// 指标以注释行写进 CSV 头部：Excel 会忽略 # 行，脚本 grep 得到。
		fmt.Fprintf(&buf, "# 验证质量: 可重放率 %.2f%% (%d/%d) | 误报率 %.2f%% (%d/%d) | 判定=环境ground-truth,不接受LLM自报\n",
			meta.Metrics.ReplayableRate*100, meta.Metrics.Replayable, meta.Metrics.ReplaySample,
			meta.Metrics.FalsePositiveRate*100, meta.Metrics.Refuted, meta.Metrics.FalsePositiveSample)
		fmt.Fprintf(&buf, "# 本报告 %d 条,均为 verified;其中可重放 %d 条\n", meta.ExportCount, meta.ExportReplayable)
	}
	w := csv.NewWriter(&buf)
	_ = w.Write([]string{"ID", "名称", "类型", "严重级别", "状态", "验证状态", "所属任务", "发现时间", "概要", "流量证据数量", "流量证据 ID"})
	for _, f := range items {
		_ = w.Write([]string{
			fmt.Sprintf("%d", f.ID),
			findingTitle(f),
			f.VulnClass,
			nz(f.Severity, "info"),
			nz(f.Status, "pending"),
			nz(f.VerificationStatus, db.VerificationUnverified),
			f.TaskDescription,
			f.CreatedAt.Format("2006-01-02 15:04:05"),
			strings.TrimSpace(f.Summary),
			fmt.Sprint(len(f.TrafficBindings)), findingTrafficIDs(f),
		})
	}
	w.Flush()
	return buf.Bytes()
}

func findingTrafficIDs(f *db.DBFinding) string {
	ids := make([]string, 0, len(f.TrafficBindings))
	for _, b := range f.TrafficBindings {
		ids = append(ids, fmt.Sprint(b.ID))
	}
	return strings.Join(ids, ",")
}

func findingTrafficMarkdown(f *db.DBFinding, attachments bool) string {
	stale := f.Report != "" && f.EvidenceVersion != f.ReportEvidenceVersion
	if len(f.TrafficBindings) == 0 && !stale {
		return ""
	}
	var out strings.Builder
	out.WriteString("\n## 相关流量证据\n\n")
	fmt.Fprintf(&out, "证据版本：%d，绑定数量：%d。\n\n", f.EvidenceVersion, len(f.TrafficBindings))
	if stale {
		out.WriteString("证据已变更，需要更新详细报告。\n\n")
	}
	for i, b := range f.TrafficBindings {
		fmt.Fprintf(&out, "%d. **证据 #%d · %s**: `%s %s`，状态码 %d\n", i+1, b.ID, b.Role, b.Snapshot.Method, strings.ReplaceAll(b.Snapshot.URL, "`", "%60"), b.Snapshot.Status)
		if b.Note != "" {
			fmt.Fprintf(&out, "   %s\n", strings.ReplaceAll(b.Note, "\n", "\n   "))
		}
		if attachments {
			fmt.Fprintf(&out, "   [请求消息](evidence/%d/%d/request.http) · [响应消息](evidence/%d/%d/response.http)\n", f.ID, b.ID, f.ID, b.ID)
		}
	}
	out.WriteString("\n")
	return out.String()
}
