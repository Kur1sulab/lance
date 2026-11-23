package eval

// 独立评测服务：与主平台解耦。
//
// 边界是刻意的：
//   - 本包不 import server/、不写 findings 表、不碰 6.2 验证闸门的任何状态。
//     评测结论是「模型能力」的度量，不是「某个发现是否成立」的判定，两者混在一张表里
//     会互相污染：一次评测失败会被当成漏洞不存在，一次验证通过会被当成能力提升。
//   - 主平台只做两件事：POST 任务 id（+ 可选动作清单），读回 scorecard。
//   - 服务不暴露「任意命令执行」：只有任务定义里经代码评审的动作可以 exec，
//     提交方现写的 exec 默认被 Runner 拒绝。
//
// 安全默认值：只监听回环。这个服务会起靶进程，暴露到网络上等于把评测机交出去；
// 真要远程用，必须显式 -allow-remote + token 双重开启。

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"sync"
	"time"
)

// ServerOptions 是评测服务的运行参数。
type ServerOptions struct {
	// MaxConcurrent 限制同时跑的任务数：每道题都要占端口和进程，放开跑会互相踩。
	MaxConcurrent int
	// Token 非空时，所有 /api 请求必须带 Authorization: Bearer *** 或 X-Eval-Token。
	Token string
	// RunTimeout 是单题总超时，防止靶卡死把服务拖住。
	RunTimeout time.Duration
}

// Server 是评测服务的 HTTP 入口。
type Server struct {
	runner *Runner
	tasks  map[string]*Task
	order  []string
	opts   ServerOptions
	sem    chan struct{}

	mu    sync.Mutex
	runs  []*Scorecard
	byRun map[string]*Scorecard

	started time.Time
}

// NewServer 构造评测服务。任务在启动时全部加载+校验，宁可起不来也不要在跑的时候才发现题坏了。
func NewServer(r *Runner, tasks []*Task, opts ServerOptions) (*Server, error) {
	if opts.MaxConcurrent <= 0 {
		opts.MaxConcurrent = 2
	}
	if opts.RunTimeout <= 0 {
		opts.RunTimeout = 10 * time.Minute
	}
	s := &Server{
		runner:  r,
		tasks:   map[string]*Task{},
		opts:    opts,
		sem:     make(chan struct{}, opts.MaxConcurrent),
		byRun:   map[string]*Scorecard{},
		started: time.Now(),
	}
	for _, t := range tasks {
		if _, dup := s.tasks[t.TaskID]; dup {
			return nil, fmt.Errorf("任务 id 重复: %s", t.TaskID)
		}
		s.tasks[t.TaskID] = t
		s.order = append(s.order, t.TaskID)
	}
	sort.Strings(s.order)
	return s, nil
}

// Handler 返回带路由与鉴权的 http.Handler。
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("GET /api/eval/tasks", s.handleListTasks)
	mux.HandleFunc("GET /api/eval/tasks/{id}", s.handleGetTask)
	mux.HandleFunc("POST /api/eval/runs", s.handleCreateRun)
	mux.HandleFunc("GET /api/eval/runs/{run_id}", s.handleGetRun)
	mux.HandleFunc("GET /api/eval/scorecards", s.handleListRuns)
	mux.HandleFunc("GET /api/eval/summary", s.handleSummary)
	return s.auth(mux)
}

// auth 用常数时间比较 token，避免按字节比较泄漏长度/前缀。
func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.opts.Token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := r.Header.Get("X-Eval-Token")
		if got == "" {
			const prefix = "Bearer "
			if h := r.Header.Get("Authorization"); len(h) > len(prefix) && h[:len(prefix)] == prefix {
				got = h[len(prefix):]
			}
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(s.opts.Token)) != 1 {
			writeErr(w, http.StatusUnauthorized, "token 校验失败")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	n := len(s.runs)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ok",
		"tasks":    len(s.order),
		"runs":     n,
		"uptime_s": int(time.Since(s.started).Seconds()),
	})
}

// TaskInfo 是任务列表项，只暴露调度需要的信息。
type TaskInfo struct {
	TaskID      string `json:"task_id"`
	Name        string `json:"name"`
	Layer       int    `json:"layer"`
	Scoring     string `json:"scoring"`
	PointsMode  string `json:"points_mode,omitempty"`
	MaxPoints   int    `json:"max_points,omitempty"`
	Defense     string `json:"defense"`
	FreezeKind  string `json:"freeze_kind"`
	FreezeRef   string `json:"freeze_ref"`
	Milestones  int    `json:"milestones"`
	Calibration *bool  `json:"calibration_expect_achieved,omitempty"`
}

func (s *Server) handleListTasks(w http.ResponseWriter, r *http.Request) {
	out := make([]TaskInfo, 0, len(s.order))
	for _, id := range s.order {
		t := s.tasks[id]
		info := TaskInfo{
			TaskID: id, Name: t.Name, Layer: t.Layer, Scoring: t.Scoring.Mode,
			PointsMode: t.Scoring.PointsMode, MaxPoints: t.MaxPoints(),
			Defense: t.DefenseKind(), FreezeKind: t.Freeze.Kind, FreezeRef: t.Freeze.Ref,
			Milestones: len(t.Milestones),
		}
		if t.Calibration != nil {
			info.Calibration = t.Calibration.ExpectAchieved
		}
		out = append(out, info)
	}
	writeJSON(w, http.StatusOK, map[string]any{"tasks": out})
}

func (s *Server) handleGetTask(w http.ResponseWriter, r *http.Request) {
	t, ok := s.tasks[r.PathValue("id")]
	if !ok {
		writeErr(w, http.StatusNotFound, "任务不存在")
		return
	}
	writeJSON(w, http.StatusOK, t)
}

func (s *Server) handleCreateRun(w http.ResponseWriter, r *http.Request) {
	// 1MiB 上限：提交的是任务 id + 动作清单，不该有大 body。
	r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
	var sub Submission
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&sub); err != nil {
		writeErr(w, http.StatusBadRequest, "解析请求失败: "+err.Error())
		return
	}
	sc, err := s.RunOnce(r.Context(), sub)
	if err != nil {
		var nf errNotFound
		if errors.As(err, &nf) {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) handleGetRun(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	sc := s.byRun[r.PathValue("run_id")]
	s.mu.Unlock()
	if sc == nil {
		writeErr(w, http.StatusNotFound, "run 不存在")
		return
	}
	writeJSON(w, http.StatusOK, sc)
}

func (s *Server) handleListRuns(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	out := make([]*Scorecard, len(s.runs))
	copy(out, s.runs)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, map[string]any{"scorecards": out, "count": len(out)})
}

func (s *Server) handleSummary(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	cards := make([]*Scorecard, len(s.runs))
	copy(cards, s.runs)
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, Aggregate(cards))
}

// errNotFound 让 handler 能把「任务不存在」映射成 404 而不是 500。
type errNotFound struct{ what string }

func (e errNotFound) Error() string { return e.what }

// RunOnce 跑一道题并登记结果。并发受 sem 限制：每道题都占端口和进程。
func (s *Server) RunOnce(ctx context.Context, sub Submission) (*Scorecard, error) {
	if sub.TaskID == "" {
		return nil, fmt.Errorf("task_id 必填")
	}
	task, ok := s.tasks[sub.TaskID]
	if !ok {
		return nil, errNotFound{what: fmt.Sprintf("任务 %q 未注册", sub.TaskID)}
	}
	select {
	case s.sem <- struct{}{}:
		defer func() { <-s.sem }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	cctx, cancel := context.WithTimeout(ctx, s.opts.RunTimeout)
	defer cancel()

	sc, err := s.runner.Run(cctx, task, &sub)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.runs = append(s.runs, sc)
	s.byRun[sc.RunID] = sc
	s.mu.Unlock()
	return sc, nil
}

// Cards 返回已登记的 scorecard 快照。
func (s *Server) Cards() []*Scorecard {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*Scorecard, len(s.runs))
	copy(out, s.runs)
	return out
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}
