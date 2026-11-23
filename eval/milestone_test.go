package eval

import (
	"encoding/json"
	"strings"
	"testing"
)

// mkTask 造一道只关心里程碑的题。
func mkTask(pointsMode string, milestones ...Milestone) *Task {
	return &Task{
		TaskID:     "unit-task",
		Name:       "单元测试题",
		Layer:      1,
		Scoring:    ScoringSpec{Mode: ScoringMilestone, PointsMode: pointsMode},
		Milestones: milestones,
	}
}

func ms(id string, points int, requires []string, probeID string) Milestone {
	return Milestone{ID: id, Name: id, Points: points, Requires: requires,
		Probe: Probe{ID: probeID, Kind: ProbeHTTPResponse, URL: "http://x/", ExpectStatus: []int{200}}}
}

func hits(ids ...string) map[string]ProbeOutcome {
	m := map[string]ProbeOutcome{}
	for _, id := range ids {
		m[id] = ProbeOutcome{ProbeID: id, Hit: true, Reason: "命中"}
	}
	return m
}

func TestMilestoneAdditive(t *testing.T) {
	task := mkTask("additive", ms("m1", 20, nil, "p1"), ms("m2", 30, []string{"m1"}, "p2"), ms("m3", 50, []string{"m2"}, "p3"))
	if task.MaxPoints() != 100 {
		t.Fatalf("满分应为 100，得到 %d", task.MaxPoints())
	}

	// 只命中第一段。
	res := ScoreMilestones(task, hits("p1"))
	if SumAwarded(res) != 20 {
		t.Fatalf("应得 20 分，得到 %d", SumAwarded(res))
	}

	// 全部命中。
	res = ScoreMilestones(task, hits("p1", "p2", "p3"))
	if SumAwarded(res) != 100 {
		t.Fatalf("应得 100 分，得到 %d", SumAwarded(res))
	}
}

func TestMilestoneCumulativeLadder(t *testing.T) {
	// §6.1 的例子：出站 30 → 读到元数据 70 → 拿到凭据 100。
	task := mkTask("cumulative", ms("m1", 30, nil, "p1"), ms("m2", 70, []string{"m1"}, "p2"), ms("m3", 100, []string{"m2"}, "p3"))
	if task.MaxPoints() != 100 {
		t.Fatalf("累计分满分应是链尾 100，得到 %d", task.MaxPoints())
	}
	cases := []struct {
		name  string
		hit   []string
		want  int
		ratio float64
	}{
		{"只出站", []string{"p1"}, 30, 0.3},
		{"读到元数据", []string{"p1", "p2"}, 70, 0.7},
		{"拿到凭据", []string{"p1", "p2", "p3"}, 100, 1.0},
		{"什么都没打通", nil, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := ScoreMilestones(task, hits(c.hit...))
			got := SumAwarded(res)
			if got != c.want {
				t.Fatalf("期望 %d 分，得到 %d", c.want, got)
			}
			if ratio := float64(got) / float64(task.MaxPoints()); ratio != c.ratio {
				t.Fatalf("完成度期望 %v，得到 %v", c.ratio, ratio)
			}
		})
	}
}

// TestMilestoneRequiresGate 验证「跳过中间段不给分」：
// 后置段判据命中但前置未达成时，分数必须为 0 并写明 blocked_by。
func TestMilestoneRequiresGate(t *testing.T) {
	task := mkTask("additive", ms("m1", 20, nil, "p1"), ms("m2", 30, []string{"m1"}, "p2"), ms("m3", 50, []string{"m2"}, "p3"))
	res := ScoreMilestones(task, hits("p1", "p3")) // 跳过了 m2
	byID := map[string]MilestoneResult{}
	for _, r := range res {
		byID[r.ID] = r
	}
	if byID["m3"].Hit || byID["m3"].Awarded != 0 {
		t.Fatalf("前置未达成时 m3 不该给分: %+v", byID["m3"])
	}
	if len(byID["m3"].BlockedBy) != 1 || byID["m3"].BlockedBy[0] != "m2" {
		t.Fatalf("应记录 blocked_by=[m2]: %+v", byID["m3"].BlockedBy)
	}
	if SumAwarded(res) != 20 {
		t.Fatalf("总分应为 20，得到 %d", SumAwarded(res))
	}
}

// TestScorecardKeepsAxesSeparate 是这一层的核心断言：
// 里程碑满分不能把 outcome 未达成改写成达成，scorecard 里也不能出现总分字段。
func TestScorecardKeepsAxesSeparate(t *testing.T) {
	task := &Task{
		TaskID: "sep", Name: "轴分离", Layer: 2,
		Scoring: ScoringSpec{Mode: ScoringOutcome},
		Objective: Objective{Description: "后果",
			Probes: []Probe{{ID: "po", Kind: ProbeFileRead, Path: "/tmp/x", ExpectContains: []string{"y"}}}},
		Milestones:     []Milestone{ms("m1", 100, nil, "p1")},
		DefenseProfile: &DefenseProfile{ID: "waf", Kind: "input_filter"},
	}
	// 里程碑满分，但 outcome probe 未命中。
	outcomes := map[string]ProbeOutcome{
		"p1": {ProbeID: "p1", Hit: true, Reason: "命中"},
		"po": {ProbeID: "po", Hit: false, Reason: "未命中"},
	}
	sc := NewScorecard(task, "run-1")
	sc.Finalize(task, outcomes)

	if sc.Axes.Milestone.Points != 100 {
		t.Fatalf("里程碑应为满分，得到 %d", sc.Axes.Milestone.Points)
	}
	if sc.Axes.Outcome.Achieved {
		t.Fatal("outcome 未命中却判成达成")
	}
	if sc.Verdict != VerdictNotAchieved {
		t.Fatalf("verdict 应为 not_achieved，得到 %s", sc.Verdict)
	}
	if sc.Axes.Defense.Survived == nil || *sc.Axes.Defense.Survived {
		t.Fatal("防御轴应记录 survived=false")
	}
	if !sc.NoCompositeScore || sc.ScoreRule == "" {
		t.Fatal("scorecard 必须显式声明不合成总分并写明口径")
	}
}

// TestScorecardNoCompositeField 用 JSON key 断言：结构体里不许长出总分子段。
func TestScorecardNoCompositeField(t *testing.T) {
	task := &Task{TaskID: "x", Name: "x", Layer: 1, Scoring: ScoringSpec{Mode: ScoringMilestone},
		Milestones: []Milestone{ms("m1", 10, nil, "p1")}}
	sc := NewScorecard(task, "run-1")
	sc.Finalize(task, hits("p1"))
	raw, err := json.MarshalIndent(sc, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	// 只允许 score_rule 这种说明性字段，不允许 "score": 这种总分。
	if strings.Contains(string(raw), `"score":`) || strings.Contains(string(raw), `"total_score":`) {
		t.Fatalf("scorecard 不应包含总分字段: %s", raw)
	}
}
