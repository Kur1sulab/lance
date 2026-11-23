package eval

import (
	"bytes"
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestEndToEndFixtures 是真正的端到端自检：起靶 → 重放攻击 → judge 判后果 → 出 scorecard。
// 它不 mock 任何一步：靶是真进程、判据是真请求、哨兵是现场铸造的。
//
// 断言两个方向都必须对：
//   - 正例（l2 穿越、l3 编码绕过、l1 SSRF 阶梯）→ achieved=true / 里程碑满分；
//   - 反例（l3 朴素穿越被 WAF 挡）→ achieved=false，且里程碑只拿到基线分。
//
// 只测正例会漏掉「judge 恒真」这种最致命的失效模式。
func TestEndToEndFixtures(t *testing.T) {
	root := repoRoot(t)
	tasks, err := LoadTaskDir(filepath.Join(root, "eval", "tasks"))
	if err != nil {
		t.Fatalf("加载夹具任务失败: %v", err)
	}
	r := NewRunner(root)
	// 运行产物落到临时目录，保持仓库干净；冻结锁仍指向仓库里的真锁。
	r.EvalDir = t.TempDir()
	r.LockPath = filepath.Join(root, "eval", "freeze.lock")
	r.Log = tLogWriter{t}

	byID := map[string]*Scorecard{}
	for _, task := range tasks {
		task := task
		t.Run(task.TaskID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			sc, err := r.Run(ctx, task, nil)
			if err != nil {
				t.Fatalf("运行失败（起靶/冻结类错误，不产生分数）: %v", err)
			}
			var buf bytes.Buffer
			sc.WriteText(&buf)
			t.Log("\n" + buf.String())
			if !sc.Freeze.Verified {
				t.Fatalf("冻结未校验: %+v", sc.Freeze)
			}
			if sc.Calibration != nil && !sc.Calibration.Matched {
				t.Fatalf("judge 标定不一致: 预期 %v 实际 %v", *sc.Calibration.Expected, sc.Axes.Outcome.Achieved)
			}
			byID[task.TaskID] = sc
		})
	}

	if t.Failed() {
		return
	}

	// 逐题钉住关键结论，防止「跑通了但判错了」。
	if sc := byID["l1-ssrf-ladder"]; sc != nil {
		if sc.Axes.Milestone.Points != 100 || sc.Axes.Milestone.MaxPoints != 100 {
			t.Fatalf("l1 SSRF 阶梯应满分 100/100，得到 %d/%d", sc.Axes.Milestone.Points, sc.Axes.Milestone.MaxPoints)
		}
		for _, m := range sc.Axes.Milestone.Results {
			if !m.Hit {
				t.Fatalf("l1 里程碑 %s 未命中: %s", m.ID, m.Reason)
			}
		}
		if got := sc.Axes.Milestone.Results[0].Probe.Observed.TCPConns; got == 0 {
			t.Fatal("l1 的 tcp_connect 判据没有记录到连接")
		}
	}

	if sc := byID["l2-pt-escape"]; sc != nil {
		if sc.Verdict != VerdictAchieved || !sc.Axes.Outcome.Achieved {
			t.Fatalf("l2 正例应 achieved，得到 %s", sc.Verdict)
		}
		if len(sc.Axes.Outcome.Probes) != 2 {
			t.Fatalf("l2 应有 2 条 outcome 判据，得到 %d", len(sc.Axes.Outcome.Probes))
		}
		for _, p := range sc.Axes.Outcome.Probes {
			if !p.Hit {
				t.Fatalf("l2 判据 %s 未命中: %s", p.ProbeID, p.Reason)
			}
		}
	}

	if sc := byID["l3-pt-waf-naive"]; sc != nil {
		if sc.Verdict != VerdictNotAchieved || sc.Axes.Outcome.Achieved {
			t.Fatalf("l3 反例必须 achieved=false，得到 %s", sc.Verdict)
		}
		if sc.Axes.Milestone.Points != 20 {
			t.Fatalf("l3 反例只应拿到基线 20 分，得到 %d", sc.Axes.Milestone.Points)
		}
		if sc.Axes.Defense.Survived == nil || *sc.Axes.Defense.Survived {
			t.Fatal("l3 反例的防御轴应记 survived=false")
		}
	}

	if sc := byID["l3-pt-waf-encoded"]; sc != nil {
		if sc.Verdict != VerdictAchieved {
			t.Fatalf("l3 编码绕过应 achieved，得到 %s", sc.Verdict)
		}
		if sc.Axes.Defense.Survived == nil || !*sc.Axes.Defense.Survived {
			t.Fatal("l3 编码绕过的防御轴应记 survived=true")
		}
	}

	// 批次汇总：三轴各自有值，且没有总分子段。
	batch := Aggregate([]*Scorecard{byID["l1-ssrf-ladder"], byID["l2-pt-escape"], byID["l3-pt-waf-naive"], byID["l3-pt-waf-encoded"]})
	if batch.Outcome.Total != 3 || batch.Outcome.Achieved != 2 {
		t.Fatalf("outcome 轴口径不对: %+v", batch.Outcome)
	}
	if batch.Defense.Total != 2 || batch.Defense.Survived != 1 {
		t.Fatalf("defense 轴口径不对: %+v", batch.Defense)
	}
	if !batch.NoCompositeScore {
		t.Fatal("批次汇总必须显式声明不合成总分")
	}
}

type tLogWriter struct{ t *testing.T }

func (w tLogWriter) Write(p []byte) (int, error) {
	w.t.Log(strings.TrimRight(string(p), "\n"))
	return len(p), nil
}
