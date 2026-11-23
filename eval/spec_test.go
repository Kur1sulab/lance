package eval

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// repoRoot 从测试文件位置推仓库根（测试的 cwd 是包目录 eval/）。
func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("拿不到测试文件路径")
	}
	return filepath.Dir(filepath.Dir(file))
}

func TestLoadFixtures(t *testing.T) {
	root := repoRoot(t)
	tasks, err := LoadTaskDir(filepath.Join(root, "eval", "tasks"))
	if err != nil {
		t.Fatalf("加载夹具任务失败: %v", err)
	}
	if len(tasks) != 4 {
		t.Fatalf("期望 4 道夹具题，实际 %d", len(tasks))
	}
	want := map[int]string{1: "l1-ssrf-ladder", 2: "l2-pt-escape", 3: "l3-pt-waf-naive"}
	got := map[int]string{}
	for _, task := range tasks {
		if _, dup := got[task.Layer]; dup && task.Layer != 3 {
			t.Fatalf("layer %d 出现多道题: %s / %s", task.Layer, got[task.Layer], task.TaskID)
		}
		got[task.Layer] = task.TaskID
	}
	for layer, id := range want {
		if got[layer] != id {
			t.Fatalf("layer %d 期望 %s，得到 %s", layer, id, got[layer])
		}
	}
}

func TestLoadTaskRejectsUnknownField(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.yaml")
	content := `
task_id: bad-1
name: 坏题
layer: 1
bogus_field: 1
target:
  kind: exec
  cmd: ["true"]
  ready: {kind: tcp_dial, addr: "127.0.0.1:1"}
freeze: {kind: synthetic, ref: v1, artifacts: [{path: eval/fixtures/pathtraversal/main.go}]}
scoring: {mode: milestone}
milestones:
  - id: m1
    name: m1
    points: 10
    probe: {id: p1, kind: http_response, url: "http://x/", expect_status: [200]}
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := LoadTask(path)
	if err == nil {
		t.Fatal("未知字段应该被拒绝（严格解码），却通过了")
	}
	if !strings.Contains(err.Error(), "bogus_field") {
		t.Fatalf("错误信息应点名字段，实际: %v", err)
	}
}

func TestValidateRejectsBadTasks(t *testing.T) {
	base := func() *Task {
		return &Task{
			TaskID: "ok-task",
			Name:   "示例",
			Layer:  1,
			Target: TargetSpec{Kind: "exec", Cmd: []string{"true"},
				Ready: ReadySpec{Kind: "tcp_dial", Addr: "127.0.0.1:1"}},
			Freeze:  FreezeSpec{Kind: "synthetic", Ref: "v1", Artifacts: []FreezeArtifact{{Path: "eval/fixtures/pathtraversal/main.go"}}},
			Scoring: ScoringSpec{Mode: ScoringMilestone},
		}
	}
	cases := []struct {
		name   string
		mutate func(*Task)
		want   string
	}{
		{"layer 越界", func(tk *Task) { tk.Layer = 4 }, "layer"},
		{"缺 target.cmd", func(tk *Task) { tk.Target.Cmd = nil }, "cmd"},
		{"缺冻结物", func(tk *Task) { tk.Freeze.Artifacts = nil }, "artifacts"},
		{"outcome 缺 probe", func(tk *Task) {
			tk.Scoring.Mode = ScoringOutcome
			tk.Milestones = nil
		}, "objective.probes"},
		{"probe 恒真", func(tk *Task) {
			tk.Milestones = []Milestone{{ID: "m1", Name: "m1", Points: 10,
				Probe: Probe{ID: "p1", Kind: ProbeHTTPResponse, URL: "http://x/"}}}
		}, "恒真"},
		{"里程碑成环", func(tk *Task) {
			tk.Milestones = []Milestone{
				{ID: "m1", Name: "m1", Points: 10, Requires: []string{"m2"},
					Probe: Probe{ID: "p1", Kind: ProbeHTTPResponse, URL: "http://x/", ExpectStatus: []int{200}}},
				{ID: "m2", Name: "m2", Points: 20, Requires: []string{"m1"},
					Probe: Probe{ID: "p2", Kind: ProbeHTTPResponse, URL: "http://x/", ExpectStatus: []int{200}}},
			}
		}, "成环"},
		{"累计分不递增", func(tk *Task) {
			tk.Scoring.PointsMode = "cumulative"
			tk.Milestones = []Milestone{
				{ID: "m1", Name: "m1", Points: 50,
					Probe: Probe{ID: "p1", Kind: ProbeHTTPResponse, URL: "http://x/", ExpectStatus: []int{200}}},
				{ID: "m2", Name: "m2", Points: 50, Requires: []string{"m1"},
					Probe: Probe{ID: "p2", Kind: ProbeHTTPResponse, URL: "http://x/", ExpectStatus: []int{200}}},
			}
		}, "严格递增"},
		{"layer3 缺防御画像", func(tk *Task) {
			tk.Layer = 3
			tk.Milestones = []Milestone{{ID: "m1", Name: "m1", Points: 10,
				Probe: Probe{ID: "p1", Kind: ProbeHTTPResponse, URL: "http://x/", ExpectStatus: []int{200}}}}
		}, "defense_profile"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			tk := base()
			c.mutate(tk)
			err := tk.Validate()
			if err == nil {
				t.Fatalf("期望校验失败，却通过了")
			}
			if !strings.Contains(err.Error(), c.want) {
				t.Fatalf("错误信息应包含 %q，实际: %v", c.want, err)
			}
		})
	}
}

func TestLoadTaskJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "task.json")
	content := `{
  "task_id": "json-task",
  "name": "JSON 题",
  "layer": 1,
  "target": {"kind": "exec", "cmd": ["true"], "ready": {"kind": "tcp_dial", "addr": "127.0.0.1:1"}},
  "freeze": {"kind": "synthetic", "ref": "v1", "artifacts": [{"path": "eval/fixtures/pathtraversal/main.go"}]},
  "scoring": {"mode": "milestone"},
  "milestones": [
    {"id": "m1", "name": "m1", "points": 10,
     "probe": {"id": "p1", "kind": "http_response", "url": "http://x/", "expect_status": [200]}}
  ]
}`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	task, err := LoadTask(path)
	if err != nil {
		t.Fatalf("JSON 任务应能加载: %v", err)
	}
	if task.TaskID != "json-task" || task.MaxPoints() != 10 {
		t.Fatalf("解析结果不对: %+v", task)
	}
}
