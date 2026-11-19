package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Kur1sulab/lance/db"
)

// 任务级重跑(rerun)测试。修复前 server 包没有 rerunTask/路由,本文件无法编译;
// 路由级「修复前 404 → 修复后 201」由端到端实测(基线二进制 vs 新二进制)覆盖。
//
// 约定与 task_admission_test.go 一致:连真实 PG(无 DSN 则 skip),测试数据
// 带唯一前缀并在结束时删除;并发上限临时改为确定值后恢复。测试 Server 不接
// LLM(权威解析器恒返回 nil),重跑出的新任务只会进排队分支,不会真跑模型。

func rerunTestUniqueName(prefix string) string {
	return fmt.Sprintf("RERUN-TEST-%s-%d", prefix, time.Now().UnixNano())
}

// callRerun 直接打 handler(与 chat_mentions_test 等一致用 SetPathValue 注入路径参数)。
func callRerun(s *Server, id string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/api/tasks/"+id+"/rerun", nil)
	req.SetPathValue("id", id)
	rec := httptest.NewRecorder()
	s.rerunTask(rec, req)
	return rec
}

func decodeRerunTaskDTO(t *testing.T, rec *httptest.ResponseRecorder) TaskDTO {
	t.Helper()
	var dto TaskDTO
	if err := json.Unmarshal(rec.Body.Bytes(), &dto); err != nil {
		t.Fatalf("decode rerun response: %v (body=%s)", err, rec.Body.String())
	}
	return dto
}

// TestRerunClonesTerminalTaskAndLaunches 是核心回归:终态任务重跑后,新任务
// 的全部创建期字段与原任务一致、parent_ref 指回原任务、拦截/允许规则被复制,
// 且新任务确实走了 launchTask 启动序列(无 LLM + 并发上限开启 → 确定进入排队)。
func TestRerunClonesTerminalTaskAndLaunches(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer m.Close()
	defer restoreConcurrencySetting(t, m)()
	// 并发上限开启 + 测试解析器恒不就绪 → 重跑的新任务必然走排队分支,
	// 排队状态在 launchTask→admitTask 内同步提交,handler 返回后即可断言。
	if err := m.SetConcurrency(true, 1); err != nil {
		t.Fatal(err)
	}

	// --- 夹具:分类 / 公司 / 继承源任务 / LLM 配置 ---
	cat, err := m.pg.CreateTaskCategory(rerunTestUniqueName("cat"))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.pg.DeleteTaskCategory(cat.ID) }()
	companyID, _, err := m.pg.Companies().UpsertCompany(rerunTestUniqueName("corp"), "")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = m.pg.Exec(`DELETE FROM company_scope WHERE company_id = $1`, companyID)
		_, _ = m.pg.Exec(`DELETE FROM companies WHERE id = $1`, companyID)
	}()
	profileID, err := m.pg.SaveProfile(&db.LLMProfile{
		Name: rerunTestUniqueName("llm"), Format: "openai",
		BaseURL: "http://127.0.0.1:1/v1", Model: "rerun-test-model", APIKey: "rerun-test-key",
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = m.pg.DeleteProfile(profileID) }()
	source, err := m.CreateTask(rerunTestUniqueName("source"), "继承源任务", nil, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.DeleteTask(source.ID, DeleteTaskOptions{}) }()
	sourceID, _ := strconv.ParseInt(source.ID, 10, 64)

	// --- 原任务:带全量创建期元数据 ---
	coverage := false
	orig, err := m.CreateTaskWithOptions("http://127.0.0.1:9 重跑测试目标", "验证重跑字段一致性", db.TaskCreateOptions{
		Name:                 rerunTestUniqueName("orig"),
		CategoryID:           &cat.ID,
		SourceTaskIDs:        []int64{sourceID},
		CompanyIDs:           []int64{companyID},
		LLMProfileIDs:        []int64{profileID},
		TimeoutSeconds:       1234,
		PlanHeartbeatSeconds: 900,
		CoverageEnabled:      &coverage,
		InterceptRules: []db.TaskInterceptRuleInput{
			{Enabled: true, Action: "block", Kind: "exact_url", Pattern: "http://127.0.0.1:9/admin", Note: "禁止打管理口"},
			{Enabled: false, Action: "allow", Kind: "fuzzy_ip", Pattern: "127.0.0.1", Note: "白名单(停用)"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = m.DeleteTask(orig.ID, DeleteTaskOptions{}) }()

	// 弄成终态(failed),并记录原任务规则快照用于比对。
	if err := m.SetTaskStatus(orig.ID, "failed"); err != nil {
		t.Fatal(err)
	}
	origIDNum, _ := strconv.ParseInt(orig.ID, 10, 64)
	origRules, err := m.Assets().ListTaskInterceptRules(origIDNum)
	if err != nil {
		t.Fatal(err)
	}

	s := newAdmissionTestServer(m, func(*Task) bool { return false })
	rec := callRerun(s, orig.ID)
	if rec.Code != http.StatusCreated {
		t.Fatalf("rerun terminal task: status=%d want 201, body=%s", rec.Code, rec.Body.String())
	}
	dto := decodeRerunTaskDTO(t, rec)
	if dto.ID == orig.ID {
		t.Fatalf("rerun must create a NEW task, got same id %s", dto.ID)
	}
	newID, _ := strconv.ParseInt(dto.ID, 10, 64)
	defer func() { _, _ = m.DeleteTask(dto.ID, DeleteTaskOptions{}) }()

	// --- 断言 1:DTO 字段与原任务一致(修复前:路由不存在,根本拿不到 201) ---
	origDTO := taskDTO(orig, s.resolvedTaskStatus(orig))
	if dto.Name != origDTO.Name || dto.Description != origDTO.Description || dto.Goal != origDTO.Goal {
		t.Fatalf("name/description/goal not cloned: new=%+v orig=%+v", dto, origDTO)
	}
	if dto.CategoryID == nil || *dto.CategoryID != cat.ID {
		t.Fatalf("category not cloned: %v want %d", dto.CategoryID, cat.ID)
	}
	if len(dto.SourceTaskIDs) != 1 || dto.SourceTaskIDs[0] != source.ID {
		t.Fatalf("source tasks not cloned: %v want [%s]", dto.SourceTaskIDs, source.ID)
	}
	if len(dto.CompanyIDs) != 1 || dto.CompanyIDs[0] != companyID {
		t.Fatalf("companies not cloned: %v want [%d]", dto.CompanyIDs, companyID)
	}
	if len(dto.LLMProfileIDs) != 1 || dto.LLMProfileIDs[0] != profileID {
		t.Fatalf("llm profiles not cloned: %v want [%d]", dto.LLMProfileIDs, profileID)
	}
	if dto.TimeoutSeconds != 1234 || dto.PlanHeartbeatSeconds != 900 {
		t.Fatalf("timeout/heartbeat not cloned: %d/%d want 1234/900", dto.TimeoutSeconds, dto.PlanHeartbeatSeconds)
	}
	if dto.CoverageEnabled != false {
		t.Fatalf("coverage switch not cloned: %v want false", dto.CoverageEnabled)
	}

	// --- 断言 2:血缘 parent_ref 指回原任务(DTO + 库里都要能看出) ---
	if dto.ParentRef != orig.ID {
		t.Fatalf("parent_ref=%q want %q", dto.ParentRef, orig.ID)
	}
	stored, err := m.pg.GetTask(newID)
	if err != nil || stored == nil {
		t.Fatalf("GetTask(new): %v", err)
	}
	if stored.ParentRef != orig.ID {
		t.Fatalf("db parent_ref=%q want %q", stored.ParentRef, orig.ID)
	}
	if stored.TimeoutSeconds != 1234 || stored.PlanHeartbeatSeconds != 900 || stored.CoverageEnabled {
		t.Fatalf("db row not cloned: timeout=%d heartbeat=%d coverage=%v", stored.TimeoutSeconds, stored.PlanHeartbeatSeconds, stored.CoverageEnabled)
	}

	// --- 断言 3:拦截/允许规则逐字段复制 ---
	newRules, err := m.Assets().ListTaskInterceptRules(newID)
	if err != nil {
		t.Fatal(err)
	}
	if len(newRules) != len(origRules) {
		t.Fatalf("rule count=%d want %d", len(newRules), len(origRules))
	}
	ruleKey := func(r db.AssetInterceptRule) string {
		return fmt.Sprintf("%v|%s|%s|%s|%s", r.Enabled, r.Action, r.Kind, r.Pattern, r.Note)
	}
	want := map[string]bool{}
	for _, r := range origRules {
		want[ruleKey(r)] = true
	}
	for _, r := range newRules {
		if !want[ruleKey(r)] {
			t.Fatalf("rule not cloned faithfully: %+v", r)
		}
	}

	// --- 断言 4:新任务确实被 launchTask 启动(排队也是启动序列的提交结果;
	// 只建不启动的任务会停在 created 且 queued=false) ---
	nt, ok := m.Task(dto.ID)
	if !ok {
		t.Fatalf("new task %s not in manager", dto.ID)
	}
	lc := nt.lifecycleSnapshot()
	if !lc.Queued && lc.Status != "running" && !s.engine.Started(dto.ID) {
		t.Fatalf("new task was created but never launched: %+v", lc)
	}
	// 原任务不被复活、不被修改。
	origLC := orig.lifecycleSnapshot()
	if origLC.Status != "failed" || origLC.Queued || origLC.Paused {
		t.Fatalf("rerun mutated the original task: %+v", origLC)
	}
}

// TestRerunRejectsNonTerminalTasks 验证终态门禁:运行中/排队中/暂停中一律
// 409 + 中文文案,且不产生新任务。修复前这些请求要么 404(无路由)要么被放行。
func TestRerunRejectsNonTerminalTasks(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer m.Close()

	cases := []struct {
		name    string
		prepare func(t *testing.T, m *Manager, task *Task)
		wantMsg string
	}{
		{
			name: "running",
			prepare: func(t *testing.T, m *Manager, task *Task) {
				if err := m.SetTaskStatus(task.ID, "running"); err != nil {
					t.Fatal(err)
				}
			},
			wantMsg: "任务还在运行",
		},
		{
			name: "queued",
			prepare: func(t *testing.T, m *Manager, task *Task) {
				if err := m.ApplyTaskAdmission(task.ID, "created", "running", true, "bootstrap", false); err != nil {
					t.Fatal(err)
				}
			},
			wantMsg: "排队",
		},
		{
			name: "paused",
			prepare: func(t *testing.T, m *Manager, task *Task) {
				if err := m.ApplyTaskPause(task.ID); err != nil {
					t.Fatal(err)
				}
			},
			wantMsg: "暂停",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			task, err := m.CreateTask(rerunTestUniqueName(tc.name), "非终态不得重跑", nil, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) }()
			tc.prepare(t, m, task)

			s := newAdmissionTestServer(m, func(*Task) bool { return false })
			before := len(m.List())
			rec := callRerun(s, task.ID)
			if rec.Code != http.StatusConflict {
				t.Fatalf("rerun %s task: status=%d want 409, body=%s", tc.name, rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.wantMsg) {
				t.Fatalf("error message must be Chinese and mention %q, got %s", tc.wantMsg, rec.Body.String())
			}
			if after := len(m.List()); after != before {
				t.Fatalf("rejected rerun still created a task: before=%d after=%d", before, after)
			}
		})
	}
}

// TestRerunAcceptsAllTerminalStatuses 验证 done/failed/timeout 三种终态都可重跑。
func TestRerunAcceptsAllTerminalStatuses(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer m.Close()
	defer restoreConcurrencySetting(t, m)()
	if err := m.SetConcurrency(true, 1); err != nil {
		t.Fatal(err)
	}
	for _, status := range []string{"done", "failed", "timeout"} {
		t.Run(status, func(t *testing.T) {
			task, err := m.CreateTask(rerunTestUniqueName(status), "终态可重跑", nil, 0, 0)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _, _ = m.DeleteTask(task.ID, DeleteTaskOptions{}) }()
			if err := m.SetTaskStatus(task.ID, status); err != nil {
				t.Fatal(err)
			}
			s := newAdmissionTestServer(m, func(*Task) bool { return false })
			rec := callRerun(s, task.ID)
			if rec.Code != http.StatusCreated {
				t.Fatalf("rerun %s task: status=%d want 201, body=%s", status, rec.Code, rec.Body.String())
			}
			dto := decodeRerunTaskDTO(t, rec)
			defer func() { _, _ = m.DeleteTask(dto.ID, DeleteTaskOptions{}) }()
			if dto.ParentRef != task.ID {
				t.Fatalf("parent_ref=%q want %q", dto.ParentRef, task.ID)
			}
		})
	}
}

// TestRerunUnknownTaskReturns404 验证不存在/非法 id 返回 404 中文文案。
func TestRerunUnknownTaskReturns404(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) - skipping", err)
	}
	defer m.Close()
	s := newAdmissionTestServer(m, nil)
	for _, id := range []string{"999999999", "abc", "-1"} {
		rec := callRerun(s, id)
		if rec.Code != http.StatusNotFound {
			t.Fatalf("rerun id=%q: status=%d want 404, body=%s", id, rec.Code, rec.Body.String())
		}
		if !strings.Contains(rec.Body.String(), "任务不存在") {
			t.Fatalf("rerun id=%q: want Chinese not-found message, got %s", id, rec.Body.String())
		}
	}
}
