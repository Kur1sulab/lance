package report

import (
	"strings"
	"testing"
	"time"

	"github.com/Kur1sulab/lance/db"
)

// TestVerificationMetaHeaderRendersMetrics 保证「两个指标 + 判定来源」真的出现在报告头部：
// 报告一旦脱库，收件人必须能看见质量口径，而不是只看到一堆结论。
func TestVerificationMetaHeaderRendersMetrics(t *testing.T) {
	meta := &VerificationMeta{
		Metrics:          db.ComputeVerificationMetrics(db.VerificationMetrics{Verified: 4, Replayable: 3, Refuted: 1, Unverified: 2}),
		ExportCount:      4,
		ExportReplayable: 3,
	}
	f := &db.DBFinding{ID: 7, VulnClass: "sqli", Severity: db.SeverityHigh, VerificationStatus: db.VerificationVerified}
	md := FindingsMarkdown([]*db.DBFinding{f}, time.Date(2026, 11, 21, 1, 2, 3, 0, time.UTC), meta)

	for _, want := range []string{
		"可重放率 **75.00%**（3/4", "误报率 **20.00%**（1/5", "环境 ground-truth", "LLM 或 Agent 自报不作为判定依据",
		"verified（环境 ground-truth 判定）",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("报告头部缺 %q:\n%s", want, md)
		}
	}
	// 空样本时不能出现 NaN。
	empty := &VerificationMeta{Metrics: db.ComputeVerificationMetrics(db.VerificationMetrics{})}
	if h := empty.Header(); strings.Contains(h, "NaN") {
		t.Errorf("空样本不得出现 NaN: %s", h)
	}
}

// TestSingleFindingMarkdownFlagsUnverified 保证「不该出现在报告里的未验证条目」
// 不会假装成已验证：万一有人绕过闸门，报告自己会把问题暴露出来。
func TestSingleFindingMarkdownFlagsUnverified(t *testing.T) {
	f := &db.DBFinding{ID: 9, VulnClass: "xss", Severity: db.SeverityLow, VerificationStatus: db.VerificationUnverified}
	md := SingleFindingMarkdown(f, time.Now(), nil)
	if !strings.Contains(md, "未过验证闸门") {
		t.Errorf("未验证条目应显式标注:\n%s", md)
	}
}
