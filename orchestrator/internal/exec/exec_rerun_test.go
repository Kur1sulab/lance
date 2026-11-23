package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"lance/internal/action"
)

// 这些用例守 rerun 的三条底线：
// 1) 目标只能来自显式清单或 findings.json，绝不凭空猜
// 2) 拼给 litmus 的必须是参数数组（每个目标一对 -t），不是一条字符串
// 3) 结论只认 litmus 自己吐的 JSON 摘要 / 统计行

func TestSplitTargets(t *testing.T) {
	got := splitTargets("10.0.0.1, 10.0.0.2；10.0.0.3\n10.0.0.1\thttp://a.test ")
	want := []string{"10.0.0.1", "10.0.0.2", "10.0.0.3", "http://a.test"}
	if len(got) != len(want) {
		t.Fatalf("去重/拆分结果 %v，期望 %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("第 %d 个目标 %q，期望 %q（顺序也要稳定）", i, got[i], want[i])
		}
	}
	if len(splitTargets("  ,;，\n ")) != 0 {
		t.Fatal("全是分隔符时应返回空")
	}
}

func TestTargetsFromFindingsBothShapes(t *testing.T) {
	wrapped := []byte(`{"findings":[{"target":"10.0.0.2"},{"target":"10.0.0.1"},{"target":"10.0.0.2"},{"target":""}]}`)
	got := targetsFromFindings(wrapped)
	if len(got) != 2 || got[0] != "10.0.0.1" || got[1] != "10.0.0.2" {
		t.Fatalf("包装形态解析成 %v，期望去重且升序 [10.0.0.1 10.0.0.2]", got)
	}

	bare := []byte(`[{"target":"b.test"},{"target":"a.test"}]`)
	got = targetsFromFindings(bare)
	if len(got) != 2 || got[0] != "a.test" || got[1] != "b.test" {
		t.Fatalf("裸数组形态解析成 %v，期望 [a.test b.test]", got)
	}

	if targetsFromFindings([]byte(`not json`)) != nil {
		t.Fatal("坏 JSON 应返回 nil 而不是半个清单")
	}
}

func TestRerunTargetsPrefersArgsThenFindings(t *testing.T) {
	dir := t.TempDir()
	fp := filepath.Join(dir, "findings.json")
	if err := os.WriteFile(fp, []byte(`{"findings":[{"target":"10.9.9.9"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	r := Runner{Findings: fp}

	got, err := r.rerunTargets(action.Action{Kind: action.KindRerun,
		Args: map[string]string{"targets": "10.1.1.1,10.1.1.2", "intensity": "high"}})
	if err != nil {
		t.Fatalf("显式清单不该报错: %v", err)
	}
	if len(got) != 2 || got[0] != "10.1.1.1" {
		t.Fatalf("显式清单优先，实际 %v", got)
	}

	got, err = r.rerunTargets(action.Action{Kind: action.KindRerun,
		Args: map[string]string{"intensity": "high"}})
	if err != nil {
		t.Fatalf("退回 findings 不该报错: %v", err)
	}
	if len(got) != 1 || got[0] != "10.9.9.9" {
		t.Fatalf("findings 兜底取到 %v，期望 [10.9.9.9]", got)
	}

	// 既没清单也没 findings：必须报错，不许默认去打什么
	if _, err := (Runner{}).rerunTargets(action.Action{Kind: action.KindRerun,
		Args: map[string]string{"intensity": "high"}}); err == nil {
		t.Fatal("没有目标来源时必须报错")
	}

	// 上限：超过 64 个直接拒绝，而不是悄悄截断
	var many []string
	for i := 0; i < 70; i++ {
		many = append(many, "10.2.0."+itoa(i))
	}
	if _, err := r.rerunTargets(action.Action{Kind: action.KindRerun,
		Args: map[string]string{"targets": strings.Join(many, ",")}}); err == nil {
		t.Fatal("超过上限应报错")
	}
}

func TestRerunVerdict(t *testing.T) {
	out := `发现 2 条  高危=1  中危=1
{
  "run_id": "r1",
  "findings": [{"id":"L-1","target":"10.0.0.1"},{"id":"L-2","target":"10.0.0.2"}],
  "out": "/tmp/run1"
}`
	v, detail := rerunVerdict(out)
	if v != VerdictReproduced || !strings.Contains(detail, "2 条") || !strings.Contains(detail, "/tmp/run1") {
		t.Fatalf("有命中时应判 reproduced 且带条数与产物目录，得到 %s / %s", v, detail)
	}

	v, _ = rerunVerdict("发现 0 条\n{\"findings\":[],\"out\":\"/tmp/run2\"}")
	if v != VerdictUnverified {
		t.Fatalf("零命中应判 unverified（没东西可复现），得到 %s", v)
	}

	// 没有 JSON 时退回统计行
	v, detail = rerunVerdict("========\n发现 3 条  高危=3\n========")
	if v != VerdictReproduced || !strings.Contains(detail, "3 条") {
		t.Fatalf("统计行兜底失败: %s / %s", v, detail)
	}

	if v, _ := rerunVerdict("Segmentation fault"); v != VerdictError {
		t.Fatalf("既无摘要也无统计行时应判 error，得到 %s", v)
	}
}

func TestRerunDoBuildsArgArray(t *testing.T) {
	dir := t.TempDir()
	fake := filepath.Join(dir, "litmus")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$0.args\"\n" +
		"cat <<'EOF'\n发现 1 条  高危=1\n{\"findings\":[{\"id\":\"L-9\",\"target\":\"10.5.5.5\"}],\"out\":\"/tmp/rr\"}\nEOF\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	r := Runner{LitmusPath: fake, Findings: filepath.Join(dir, "findings.json"),
		OutDir: filepath.Join(dir, "out"), Timeout: 10 * time.Second,
		Templates: "/tmp/tpl"}
	res := r.Do(context.Background(), action.Action{
		Kind: action.KindRerun,
		Args: map[string]string{"targets": "10.5.5.5,10.5.5.6", "intensity": "high"},
		Why:  "覆盖项补测", Conf: 0.6,
	})
	if res.Verdict != VerdictReproduced {
		t.Fatalf("判据应来自 JSON 摘要，得到 %s / %s / %s", res.Verdict, res.Detail, res.Raw)
	}

	raw, err := os.ReadFile(fake + ".args")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Fields(strings.TrimSpace(string(raw)))
	want := []string{"run", "-t", "10.5.5.5", "-t", "10.5.5.6", "--intensity", "high", "--json",
		"--templates", "/tmp/tpl", "--out"}
	for i, w := range want {
		if i >= len(lines) || lines[i] != w {
			t.Fatalf("第 %d 个参数 %q，期望 %q（完整命令行 %v）", i, lines[i], w, lines)
		}
	}
	if !strings.HasPrefix(lines[len(lines)-1], filepath.Join(dir, "out", "rerun-")) {
		t.Fatalf("rerun 必须落到独立子目录，实际 %q", lines[len(lines)-1])
	}
}

func TestRerunDoRejectsBadIntensity(t *testing.T) {
	res := Runner{}.Do(context.Background(), action.Action{
		Kind: action.KindRerun,
		Args: map[string]string{"targets": "10.0.0.1", "intensity": "ultra"},
		Why:  "手滑写了不存在的档位", Conf: 0.5,
	})
	if res.Verdict != VerdictError || !strings.Contains(res.Detail, "不合法") {
		t.Fatalf("非法强度应被 action.Validate 拦下，得到 %s / %s", res.Verdict, res.Detail)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
