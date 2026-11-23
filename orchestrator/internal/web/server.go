// Package web 是 Lance 的本地控制台。
//
// 设计边界和 CLI 一致：这里不新增任何决策能力，只是把 next / exec / 账本
// 三件事搬到浏览器里，让人能看清「下一步为什么这么选」再决定要不要按下去。
// 服务只绑 127.0.0.1 —— 它是自己的调度台，不是给客户的门面。
package web

import (
	"context"
	"embed"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	osexec "os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Kur1sulab/lance/orchestrator/internal/action"
	"github.com/Kur1sulab/lance/orchestrator/internal/brain"
	"github.com/Kur1sulab/lance/orchestrator/internal/exec"
	"github.com/Kur1sulab/lance/orchestrator/internal/state"
)

//go:embed ui/index.html
var uiFS embed.FS

// Config 是控制台里可改的三条路径。落盘到 <state>/serve.json，重启不丢。
type Config struct {
	Findings string `json:"findings"`
	Litmus   string `json:"litmus"`
	StateDir string `json:"state_dir"`
}

type Server struct {
	mu  sync.Mutex
	cfg Config
	ver string
}

func New(cfg Config, version string) *Server { return &Server{cfg: cfg, ver: version} }

// Run 起服务，阻塞直到进程退出。
func (s *Server) Run(ctx context.Context, addr string, open bool) error {
	url := "http://" + addr + "/"
	srv := &http.Server{Addr: addr, Handler: s.Handler(), ReadHeaderTimeout: 10 * time.Second}
	fmt.Printf("控制台：%s\n", url)
	if open {
		go func() {
			time.Sleep(300 * time.Millisecond)
			// macOS 用 open，Linux 用 xdg-open；失败就算了，不挡服务启动
			bin := "open"
			if _, err := osexec.LookPath("open"); err != nil {
				bin = "xdg-open"
			}
			_ = osexec.Command(bin, url).Start()
		}()
	}
	go func() {
		<-ctx.Done()
		sh, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(sh)
	}()
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		return err
	}
	return nil
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", s.hIndex)
	mux.HandleFunc("/api/state", s.hState)
	mux.HandleFunc("/api/config", s.hConfig)
	mux.HandleFunc("/api/next", s.hNext)
	mux.HandleFunc("/api/exec", s.hExec)
	return mux
}

func (s *Server) hIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	b, err := uiFS.ReadFile("ui/index.html")
	if err != nil {
		http.Error(w, "ui 未打包", 500)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(b)
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(v)
}

// ---- 数据视图 ----

type stateResp struct {
	Version   string           `json:"version"`
	Config    Config           `json:"config"`
	Findings  []brain.Finding  `json:"findings"`
	Coverage  []brain.Coverage `json:"coverage"`
	Ledger    []state.Record   `json:"ledger"`
	Round     int              `json:"round"`
	LoadError string           `json:"load_error"`
	Stats     stats            `json:"stats"`
}

type stats struct {
	Findings  int `json:"findings"`
	High      int `json:"high"`
	Verified  int `json:"verified"`
	Pending   int `json:"pending"`
	Actions   int `json:"actions"`
	Round     int `json:"round"`
}

func (s *Server) snapshot() stateResp {
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	resp := stateResp{Version: s.ver, Config: cfg}
	if cfg.Findings != "" {
		if b, err := os.ReadFile(cfg.Findings); err != nil {
			resp.LoadError = err.Error()
		} else if err := json.Unmarshal(b, &struct {
			Findings *[]brain.Finding  `json:"findings"`
			Coverage *[]brain.Coverage `json:"coverage"`
		}{&resp.Findings, &resp.Coverage}); err != nil {
			resp.LoadError = "findings.json 解析失败: " + err.Error()
		}
	}
	// 账本：直接读 JSONL，最新的排在前面
	if st, err := state.Open(cfg.StateDir); err == nil {
		resp.Ledger = readLedger(st.Path)
		resp.Round = st.Round()
	}
	// 已复验签名 → 每条的复验状态
	done := map[string]string{}
	for _, r := range resp.Ledger {
		if _, ok := done[r.Signature]; !ok {
			done[r.Signature] = r.Verdict
		}
	}
	for _, f := range resp.Findings {
		if _, ok := done["replay:"+f.ID]; ok {
			resp.Stats.Verified++
		} else {
			resp.Stats.Pending++
		}
		if strings.EqualFold(f.Severity, "high") || strings.EqualFold(f.Severity, "critical") {
			resp.Stats.High++
		}
	}
	resp.Stats.Findings = len(resp.Findings)
	resp.Stats.Actions = len(resp.Ledger)
	resp.Stats.Round = resp.Round
	return resp
}

func readLedger(path string) []state.Record {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var out []state.Record
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var r state.Record
		if json.Unmarshal([]byte(line), &r) == nil {
			out = append(out, r)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].TS > out[j].TS })
	if len(out) > 300 {
		out = out[:300]
	}
	return out
}

func (s *Server) hState(w http.ResponseWriter, r *http.Request) { writeJSON(w, s.snapshot()) }

func (s *Server) hConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var in Config
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	s.mu.Lock()
	if in.Findings != "" {
		in.Findings = expand(in.Findings)
		s.cfg.Findings = in.Findings
	}
	if in.Litmus != "" {
		s.cfg.Litmus = expand(in.Litmus)
	}
	cfg := s.cfg
	s.mu.Unlock()
	// 落盘，下次启动沿用（写在状态目录里，不污染项目根）
	if st, err := state.Open(cfg.StateDir); err == nil {
		if b, err := json.MarshalIndent(cfg, "", "  "); err == nil {
			_ = os.WriteFile(filepath.Join(filepath.Dir(st.Path), "serve.json"), b, 0o644)
		}
	}
	writeJSON(w, map[string]any{"ok": true, "config": cfg})
}

func expand(p string) string {
	if strings.HasPrefix(p, "~/") {
		if h, err := os.UserHomeDir(); err == nil {
			return filepath.Join(h, p[2:])
		}
	}
	return p
}

// ---- 决策 ----

type nextReq struct {
	Brain  string `json:"brain"`
	Budget int    `json:"budget"`
}

type planResp struct {
	Brain    string          `json:"brain"`
	Label    string          `json:"label"`
	Actions  []action.Action `json:"actions"`
	Findings int             `json:"findings"`
	History  int             `json:"history"`
}

func (s *Server) hNext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var in nextReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if in.Budget <= 0 || in.Budget > 32 {
		in.Budget = 8
	}
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	var br brain.Brain
	label := "rules"
	if in.Brain == "llm" {
		if l, ok := brain.EnvLLM(); ok {
			br, label = l, "llm"
		} else {
			br, label = brain.Rules{}, "未配置 LANCE_LLM_*，已回落 rules"
		}
	} else {
		br = brain.Rules{}
	}

	var ff struct {
		Findings []brain.Finding  `json:"findings"`
		Coverage []brain.Coverage `json:"coverage"`
	}
	if b, err := os.ReadFile(cfg.Findings); err != nil {
		http.Error(w, "读不到 findings.json: "+err.Error(), 400)
		return
	} else if err := json.Unmarshal(b, &ff); err != nil {
		http.Error(w, "findings.json 解析失败: "+err.Error(), 400)
		return
	}
	st, err := state.Open(cfg.StateDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	hist, _ := st.History()
	acts, err := br.Decide(r.Context(), brain.Input{
		Findings: ff.Findings, Coverage: ff.Coverage, History: hist, Budget: in.Budget,
	})
	if err != nil {
		http.Error(w, "决策失败: "+err.Error(), 500)
		return
	}
	if acts == nil {
		acts = []action.Action{}
	}
	writeJSON(w, planResp{Brain: in.Brain, Label: label, Actions: acts, Findings: len(ff.Findings), History: len(hist)})
}

// ---- 执行 ----

type execReq struct {
	Brain   string          `json:"brain"`
	Actions []action.Action `json:"actions"`
}

func (s *Server) hExec(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", 405)
		return
	}
	var in execReq
	if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
		http.Error(w, err.Error(), 400)
		return
	}
	if len(in.Actions) == 0 {
		writeJSON(w, map[string]any{"ok": false, "error": "没有可执行的动作"})
		return
	}
	// 整批原子性：只要有一条非法，整批拒绝 —— 与 CLI 的行为保持一致
	if err := action.ValidateAll(in.Actions); err != nil {
		writeJSON(w, map[string]any{"ok": false, "error": "动作校验失败，整批拒绝: " + err.Error()})
		return
	}
	s.mu.Lock()
	cfg := s.cfg
	s.mu.Unlock()

	st, err := state.Open(cfg.StateDir)
	if err != nil {
		http.Error(w, err.Error(), 500)
		return
	}
	round := st.Round()
	rn := exec.Runner{Findings: cfg.Findings, LitmusPath: cfg.Litmus, Timeout: 45 * time.Second}

	type item struct {
		Kind      string  `json:"kind"`
		Signature string  `json:"signature"`
		Verdict   string  `json:"verdict"`
		Detail    string  `json:"detail"`
		Why       string  `json:"why"`
		Seconds   float64 `json:"seconds"`
	}
	out := make([]item, 0, len(in.Actions))
	for _, a := range in.Actions {
		res := rn.Do(r.Context(), a)
		_ = st.Append(state.Record{
			Round: round, Brain: in.Brain, Kind: string(a.Kind),
			Signature: res.Signature, Why: a.Why,
			Verdict: string(res.Verdict), Detail: res.Detail,
		})
		out = append(out, item{
			Kind: string(a.Kind), Signature: res.Signature, Verdict: string(res.Verdict),
			Detail: res.Detail, Why: a.Why, Seconds: res.Duration.Seconds(),
		})
	}
	writeJSON(w, map[string]any{"ok": true, "round": round, "results": out, "ledger": st.Path})
}
