package server

// C2 是一层薄适配：Lance 不实现监听器、会话与载荷，全部交给 Metasploit RPC。
// 这里只做三件事：存引擎连接信息、把 RPC 结果翻成前端能渲染的结构、
// 把每次下发命令的原文落到会话输出里供事后复核。

import (
	"context"
	"net/http"
	"strconv"
	"time"

	"github.com/Kur1sulab/lance/internal/engine/msf"
)

type c2EngineRow struct {
	ID       int64  `json:"id"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
	BaseURL  string `json:"base_url"`
	Username string `json:"username"`
	HasPass  bool   `json:"has_password"`
	Enabled  bool   `json:"enabled"`
}

// GET /api/c2/engines
func (s *Server) apiListC2Engines(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	rows, err := pg.QueryContext(r.Context(), `
		SELECT id, kind, name, base_url, username, password <> '', enabled
		FROM c2_engines ORDER BY id`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := make([]c2EngineRow, 0, 4)
	for rows.Next() {
		var x c2EngineRow
		if err := rows.Scan(&x.ID, &x.Kind, &x.Name, &x.BaseURL, &x.Username, &x.HasPass, &x.Enabled); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		out = append(out, x)
	}
	writeJSON(w, 200, map[string]any{"engines": out})
}

// POST /api/c2/engines
func (s *Server) apiCreateC2Engine(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var in struct {
		Kind     string `json:"kind"`
		Name     string `json:"name"`
		BaseURL  string `json:"base_url"`
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if in.Kind == "" {
		in.Kind = "msf-rpc"
	}
	if in.Username == "" {
		in.Username = "msf"
	}
	if in.Name == "" {
		in.Name = "Metasploit RPC"
	}
	var id int64
	err := pg.QueryRowContext(r.Context(), `
		INSERT INTO c2_engines (kind, name, base_url, username, password)
		VALUES ($1,$2,$3,$4,$5) RETURNING id`,
		in.Kind, in.Name, in.BaseURL, in.Username, in.Password).Scan(&id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

func (s *Server) apiDeleteC2Engine(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	_, _ = pg.ExecContext(r.Context(), `DELETE FROM c2_engines WHERE id=$1`, id)
	writeJSON(w, 200, map[string]any{"ok": true})
}

// engineClient 按 id 建一个 msf client。找不到就返回 nil。
func (s *Server) engineClient(r *http.Request, id int64) (*msf.Client, bool) {
	pg := s.pg(&discardWriter{})
	if pg == nil {
		return nil, false
	}
	var kind, base, user, pass string
	err := pg.QueryRowContext(r.Context(),
		`SELECT kind, base_url, username, password FROM c2_engines WHERE id=$1`, id).
		Scan(&kind, &base, &user, &pass)
	if err != nil {
		return nil, false
	}
	if kind != "msf-rpc" {
		return nil, false
	}
	return msf.New(base, user, pass), true
}

func engineID(r *http.Request) int64 {
	q := r.URL.Query().Get("engine")
	if q == "" {
		q = r.PathValue("id")
	}
	id, _ := strconv.ParseInt(q, 10, 64)
	return id
}

// POST /api/c2/test?engine=N
func (s *Server) apiC2Test(w http.ResponseWriter, r *http.Request) {
	c, ok := s.engineClient(r, engineID(r))
	if !ok {
		writeErr(w, 404, "引擎不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := c.Login(ctx); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	sess, err := c.Sessions(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "sessions": len(sess)})
}

// GET /api/c2/sessions?engine=N
func (s *Server) apiC2Sessions(w http.ResponseWriter, r *http.Request) {
	c, ok := s.engineClient(r, engineID(r))
	if !ok {
		writeErr(w, 404, "引擎不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	sess, err := c.Sessions(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error(), "sessions": []any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "sessions": sess})
}

// POST /api/c2/exec?engine=N  body: {session, cmd, meterpreter}
func (s *Server) apiC2Exec(w http.ResponseWriter, r *http.Request) {
	c, ok := s.engineClient(r, engineID(r))
	if !ok {
		writeErr(w, 404, "引擎不存在")
		return
	}
	var in struct {
		Session     string `json:"session"`
		Cmd         string `json:"cmd"`
		Meterpreter bool   `json:"meterpreter"`
	}
	if err := decode(r, &in); err != nil || in.Session == "" || in.Cmd == "" {
		writeErr(w, 400, "session 和 cmd 必填")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	var err error
	if in.Meterpreter {
		err = c.MeterpreterWrite(ctx, in.Session, in.Cmd)
	} else {
		err = c.ShellWrite(ctx, in.Session, in.Cmd)
	}
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	// 等一拍再读，多数 shell 需要点时间吐输出
	time.Sleep(1200 * time.Millisecond)
	var out string
	if in.Meterpreter {
		out, _ = c.MeterpreterRead(ctx, in.Session)
	} else {
		out, _ = c.ShellRead(ctx, in.Session)
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out})
}

// GET /api/c2/jobs?engine=N
func (s *Server) apiC2Jobs(w http.ResponseWriter, r *http.Request) {
	c, ok := s.engineClient(r, engineID(r))
	if !ok {
		writeErr(w, 404, "引擎不存在")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	jobs, err := c.Jobs(ctx)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error(), "jobs": map[string]any{}})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "jobs": jobs})
}

// POST /api/c2/jobs?engine=N  body: {payload, lhost, lport}
func (s *Server) apiC2StartJob(w http.ResponseWriter, r *http.Request) {
	c, ok := s.engineClient(r, engineID(r))
	if !ok {
		writeErr(w, 404, "引擎不存在")
		return
	}
	var in struct {
		Payload string `json:"payload"`
		LHost   string `json:"lhost"`
		LPort   int    `json:"lport"`
	}
	if err := decode(r, &in); err != nil || in.Payload == "" || in.LHost == "" || in.LPort == 0 {
		writeErr(w, 400, "payload / lhost / lport 必填")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	job, err := c.StartHandler(ctx, in.Payload, in.LHost, in.LPort)
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "job": job})
}

// DELETE /api/c2/jobs/{id}?engine=N
func (s *Server) apiC2StopJob(w http.ResponseWriter, r *http.Request) {
	c, ok := s.engineClient(r, engineID(r))
	if !ok {
		writeErr(w, 404, "引擎不存在")
		return
	}
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	if err := c.StopJob(ctx, id); err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error()})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}
