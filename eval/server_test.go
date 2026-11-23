package eval

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

// newTestServer 起一个只加载 l2 夹具题的评测服务（走真实 Runner，不 mock 判定）。
func newTestServer(t *testing.T, token string) (*Server, *httptest.Server) {
	t.Helper()
	root := repoRoot(t)
	tasks, err := LoadTaskDir(filepath.Join(root, "eval", "tasks"))
	if err != nil {
		t.Fatal(err)
	}
	var picked []*Task
	for _, task := range tasks {
		if task.TaskID == "l2-pt-escape" {
			picked = append(picked, task)
		}
	}
	if len(picked) == 0 {
		t.Fatal("找不到 l2-pt-escape 夹具题")
	}
	r := NewRunner(root)
	r.EvalDir = t.TempDir()
	r.LockPath = filepath.Join(root, "eval", "freeze.lock")
	r.Log = tLogWriter{t}
	srv, err := NewServer(r, picked, ServerOptions{Token: token, MaxConcurrent: 1})
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return srv, ts
}

func TestServerRunOverHTTP(t *testing.T) {
	_, ts := newTestServer(t, "")

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("healthz 应为 200，得到 %d", resp.StatusCode)
	}

	body := strings.NewReader(`{"task_id":"l2-pt-escape","note":"自检"}`)
	resp, err = http.Post(ts.URL+"/api/eval/runs", "application/json", body)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		t.Fatalf("提交任务应 200，得到 %d: %s", resp.StatusCode, raw)
	}
	var sc Scorecard
	if err := json.Unmarshal(raw, &sc); err != nil {
		t.Fatalf("响应不是合法 scorecard: %v", err)
	}
	if sc.TaskID != "l2-pt-escape" || sc.Verdict != VerdictAchieved {
		t.Fatalf("通过 HTTP 跑出来的结论不对: task=%s verdict=%s", sc.TaskID, sc.Verdict)
	}
	if !sc.NoCompositeScore {
		t.Fatal("scorecard 必须声明不合成总分")
	}

	// 未知任务 → 404，而不是 500。
	resp2, err := http.Post(ts.URL+"/api/eval/runs", "application/json", strings.NewReader(`{"task_id":"nope"}`))
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusNotFound {
		t.Fatalf("未知任务应 404，得到 %d", resp2.StatusCode)
	}

	// summary 要有三轴，且没有总分子段。
	resp3, err := http.Get(ts.URL + "/api/eval/summary")
	if err != nil {
		t.Fatal(err)
	}
	defer resp3.Body.Close()
	raw3, _ := io.ReadAll(resp3.Body)
	var batch map[string]any
	if err := json.Unmarshal(raw3, &batch); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"milestone", "outcome", "defense"} {
		if _, ok := batch[key]; !ok {
			t.Fatalf("summary 缺少 %s 轴: %s", key, raw3)
		}
	}
	if _, ok := batch["score"]; ok {
		t.Fatalf("summary 不应有总分字段: %s", raw3)
	}
}

func TestServerTokenAuth(t *testing.T) {
	_, ts := newTestServer(t, "s3cret")

	resp, err := http.Get(ts.URL + "/api/eval/tasks")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("没带 token 应 401，得到 %d", resp.StatusCode)
	}

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/api/eval/tasks", nil)
	req.Header.Set("Authorization", "Bearer s3cret")
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != 200 {
		t.Fatalf("带 token 应 200，得到 %d", resp2.StatusCode)
	}
}
