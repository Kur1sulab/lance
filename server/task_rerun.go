package server

import (
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/Kur1sulab/lance/db"
)

// 任务重跑(rerun)错误文案。中文原文即 errCatalog 键,经 trMsg 按输出语言翻译;
// 界面只允许看到中文(或用户所选语言),不泄漏底层英文枚举。
const (
	errRerunTaskNotFound  = "任务不存在或已归档"
	errRerunTaskRunning   = "任务还在运行，请先停止再重跑"
	errRerunTaskPaused    = "任务已暂停，请先恢复任务，待任务结束再重跑"
	errRerunTaskQueued    = "任务还在排队等待启动，请等待任务结束再重跑"
	errRerunTaskDeleting  = "任务正在删除，无法重跑"
	errRerunReadRules     = "重跑失败：读取原任务拦截规则出错"
	errRerunCreateFailed  = "重跑失败：创建新任务出错"
	errRerunCategoryGone  = "重跑失败：原任务的任务分类已失效"
	errRerunCompanyGone   = "重跑失败：原任务关联的公司已失效"
	errRerunSourceGone    = "重跑失败：原任务的继承任务已失效"
	errRerunProfileGone   = "重跑失败：原任务的 LLM 配置已失效"
	errRerunLineageFailed = "重跑失败：记录重跑来源出错"
)

// rerunTask 一键重跑终态任务(done/failed/timeout):读取原任务的全部创建期
// 元数据(描述/目标/名称/分类/继承任务/关联公司/LLM 链/超时/心跳/覆盖度开关/
// 任务级拦截规则),用 CreateTaskWithOptions 建一个全新任务并走 launchTask
// 共享启动序列。原任务保持原样(历史证据/发现/时间线不丢),新任务通过现有
// parent_ref 列记录「跑自哪个任务」(不动 schema)。
//
// 与意图级 rerun(/intents/{iid}/rerun,复活原任务内的单条意图)不同:这里是
// 任务级克隆重跑,原任务不被复活、不被修改。
func (s *Server) rerunTask(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.PathValue("id"))
	idNum, err := strconv.ParseInt(id, 10, 64)
	if err != nil || idNum <= 0 {
		writeErr(w, 404, trMsg(errRerunTaskNotFound))
		return
	}

	// 终态门禁与准入/控制路径共用 concMu:读取 live 快照时不会被调度器并发
	// 改写(排队/暂停/终态复活都在这把锁下提交)。只允许终态任务重跑。
	s.concMu.Lock()
	orig, ok := s.m.Task(id)
	if !ok || orig == nil {
		s.concMu.Unlock()
		writeErr(w, 404, trMsg(errRerunTaskNotFound))
		return
	}
	if s.engine.IsDeleting(id) {
		s.concMu.Unlock()
		writeErr(w, 409, trMsg(errRerunTaskDeleting))
		return
	}
	lifecycle := orig.lifecycleSnapshot()
	switch {
	case lifecycle.Queued:
		s.concMu.Unlock()
		writeErr(w, 409, trMsg(errRerunTaskQueued))
		return
	case lifecycle.Paused || s.engine.IsPaused(id) || lifecycle.Status == "paused":
		s.concMu.Unlock()
		writeErr(w, 409, trMsg(errRerunTaskPaused))
		return
	case !isTerminalStatus(lifecycle.Status):
		s.concMu.Unlock()
		writeErr(w, 409, trMsg(errRerunTaskRunning))
		return
	}
	s.concMu.Unlock()

	// 元数据以库为准(GetTask 已 hydrate 继承任务/公司/LLM 链,且过滤已归档)。
	pt, err := s.m.PG().GetTask(idNum)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	if pt == nil {
		writeErr(w, 404, trMsg(errRerunTaskNotFound))
		return
	}

	// 任务级拦截/允许规则随克隆复制(含 enabled/action/kind/pattern/note)。
	var ruleInputs []db.TaskInterceptRuleInput
	if as := s.m.Assets(); as != nil {
		rules, err := as.ListTaskInterceptRules(idNum)
		if err != nil {
			writeErr(w, 500, trMsg(errRerunReadRules))
			return
		}
		for _, ru := range rules {
			ruleInputs = append(ruleInputs, db.TaskInterceptRuleInput{
				Enabled: ru.Enabled, Action: ru.Action, Kind: ru.Kind,
				Pattern: ru.Pattern, Note: ru.Note,
			})
		}
	}

	coverage := pt.CoverageEnabled
	nt, err := s.m.CreateTaskWithOptions(pt.Description, pt.Goal, db.TaskCreateOptions{
		Name:                 pt.Name,
		CategoryID:           pt.CategoryID,
		SourceTaskIDs:        pt.SourceTaskIDs,
		CompanyIDs:           pt.CompanyIDs,
		LLMProfileIDs:        pt.LLMProfileIDs,
		TimeoutSeconds:       pt.TimeoutSeconds,
		PlanHeartbeatSeconds: pt.PlanHeartbeatSeconds,
		CoverageEnabled:      &coverage,
		InterceptRules:       ruleInputs,
	})
	if err != nil {
		writeErr(w, 400, trMsg(rerunCreateErrMessage(err)))
		return
	}

	// 血缘:复用现有 parent_ref 列(编排 spawn 也用它记录父任务),新任务在库里
	// 可直接看出跑自哪个任务。写失败则补偿删除刚建的任务,避免留下无来源的孤儿。
	newID, _ := strconv.ParseInt(nt.ID, 10, 64)
	if err := s.m.PG().SetParentRef(newID, id); err != nil {
		_, _ = s.m.DeleteTask(nt.ID, DeleteTaskOptions{})
		writeErr(w, 500, trMsg(errRerunLineageFailed))
		return
	}
	nt.ParentRef = id

	log.Printf("[task] rerun #%s -> new task #%s %q", id, nt.ID, nt.Name)
	// 共享建后启动序列(seed 根资产 + 后台目标分解 + engine.Run),与 createTask /
	// spawn_task 同一条路径;不走它任务永远不会跑。
	s.launchTask(nt, pt.Description+" "+pt.Goal, false)
	writeJSON(w, 201, taskDTO(nt, s.resolvedTaskStatus(nt)))
}

// rerunCreateErrMessage 把 CreateTaskWithOptions 的底层错误映射为中文文案,
// 避免把英文枚举/SQL 细节直接吐给界面。
func rerunCreateErrMessage(err error) string {
	switch {
	case errors.Is(err, db.ErrTaskCategoryInvalid), errors.Is(err, db.ErrTaskCategoryNotFound):
		return errRerunCategoryGone
	case errors.Is(err, db.ErrTaskCompanyIDsInvalid), errors.Is(err, db.ErrTaskCompanyNotFound):
		return errRerunCompanyGone
	case strings.Contains(err.Error(), "source task"):
		return errRerunSourceGone
	case strings.Contains(err.Error(), "LLM profile"), strings.Contains(err.Error(), "task_llm_profiles"):
		return errRerunProfileGone
	default:
		return errRerunCreateFailed
	}
}
