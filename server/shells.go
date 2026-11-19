package server

// Webshell 管理：平台只负责记录、编排与留痕，真正的协议执行在
// internal/engine/webshell。所有操作都写进 shells 表，方便事后审计
// 「谁在什么时候对哪个 shell 执行了什么」。

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Kur1sulab/lance/internal/engine/webshell"
)

type shellRow struct {
	ID         int64             `json:"id"`
	URL        string            `json:"url"`
	Kind       string            `json:"kind"`
	Secret     string            `json:"secret"`
	Header     map[string]string `json:"header"`
	Body       string            `json:"body"`
	Insecure   bool              `json:"insecure"`
	Remark     string            `json:"remark"`
	Status     string            `json:"status"`
	LastCheck  string            `json:"last_check"`
	LastOutput string            `json:"last_output"`
	TaskID     string            `json:"task_id"`
	CreatedAt  string            `json:"created_at"`
}

func (s *Server) shellCfg(row shellRow) webshell.Config {
	return webshell.Config{
		URL:      row.URL,
		Kind:     row.Kind,
		Secret:   row.Secret,
		Header:   row.Header,
		Body:     row.Body,
		Insecure: row.Insecure,
	}
}

// GET /api/shells
func (s *Server) apiListShells(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	rows, err := pg.QueryContext(r.Context(), `
		SELECT id, url, kind, secret, header, body, insecure, remark, status,
		       coalesce(to_char(last_check,'YYYY-MM-DD HH24:MI:SS'),''), last_output, task_id,
		       to_char(created_at,'YYYY-MM-DD HH24:MI:SS')
		FROM shells ORDER BY id DESC LIMIT 500`)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	defer rows.Close()
	out := make([]shellRow, 0, 32)
	for rows.Next() {
		var x shellRow
		var hdr []byte
		if err := rows.Scan(&x.ID, &x.URL, &x.Kind, &x.Secret, &hdr, &x.Body, &x.Insecure, &x.Remark,
			&x.Status, &x.LastCheck, &x.LastOutput, &x.TaskID, &x.CreatedAt); err != nil {
			writeErr(w, 500, err.Error())
			return
		}
		x.Header = map[string]string{}
		_ = json.Unmarshal(hdr, &x.Header)
		out = append(out, x)
	}
	writeJSON(w, 200, map[string]any{"shells": out})
}

// POST /api/shells
func (s *Server) apiCreateShell(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	var in shellRow
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	if strings.TrimSpace(in.URL) == "" || strings.TrimSpace(in.Kind) == "" {
		writeErr(w, 400, "url 和 kind 必填")
		return
	}
	if in.Header == nil {
		in.Header = map[string]string{}
	}
	hdr, _ := json.Marshal(in.Header)
	var id int64
	err := pg.QueryRowContext(r.Context(), `
		INSERT INTO shells (url, kind, secret, header, body, insecure, remark, task_id)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`,
		in.URL, in.Kind, in.Secret, hdr, in.Body, in.Insecure, in.Remark, in.TaskID).Scan(&id)
	if err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"id": id})
}

// DELETE /api/shells/{id}
func (s *Server) apiDeleteShell(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if _, err := pg.ExecContext(r.Context(), `DELETE FROM shells WHERE id=$1`, id); err != nil {
		writeErr(w, 500, err.Error())
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true})
}

func (s *Server) loadShell(r *http.Request) (shellRow, bool, error) {
	pg := s.pg(&discardWriter{})
	if pg == nil {
		return shellRow{}, false, nil
	}
	id, _ := strconv.ParseInt(r.PathValue("id"), 10, 64)
	var x shellRow
	var hdr []byte
	err := pg.QueryRowContext(r.Context(), `
		SELECT id, url, kind, secret, header, body, insecure, remark, status,
		       coalesce(to_char(last_check,'YYYY-MM-DD HH24:MI:SS'),''), last_output, task_id,
		       to_char(created_at,'YYYY-MM-DD HH24:MI:SS')
		FROM shells WHERE id=$1`, id).
		Scan(&x.ID, &x.URL, &x.Kind, &x.Secret, &hdr, &x.Body, &x.Insecure, &x.Remark,
			&x.Status, &x.LastCheck, &x.LastOutput, &x.TaskID, &x.CreatedAt)
	if err != nil {
		return shellRow{}, false, err
	}
	x.Header = map[string]string{}
	_ = json.Unmarshal(hdr, &x.Header)
	return x, true, nil
}

type discardWriter struct{ h http.Header }

func (d *discardWriter) Header() http.Header {
	if d.h == nil {
		d.h = http.Header{}
	}
	return d.h
}
func (d *discardWriter) Write(b []byte) (int, error) { return len(b), nil }
func (d *discardWriter) WriteHeader(int)            {}

// POST /api/shells/{id}/op  body: {op: ping|exec|ls|read|write, cmd, path, content}
func (s *Server) apiShellOp(w http.ResponseWriter, r *http.Request) {
	pg := s.pg(w)
	if pg == nil {
		return
	}
	row, ok, err := s.loadShell(r)
	if err != nil || !ok {
		writeErr(w, 404, "shell 不存在")
		return
	}
	var in struct {
		Op      string `json:"op"`
		Cmd     string `json:"cmd"`
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := decode(r, &in); err != nil {
		writeErr(w, 400, "请求格式错误")
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	cfg := s.shellCfg(row)

	var out string
	switch in.Op {
	case "ping":
		out, err = cfg.Ping(ctx)
	case "exec":
		out, err = cfg.Exec(ctx, in.Cmd)
	case "ls":
		out, err = cfg.ListDir(ctx, in.Path)
	case "read":
		out, err = cfg.ReadFile(ctx, in.Path)
	case "write":
		out, err = cfg.WriteFile(ctx, in.Path, []byte(in.Content))
	default:
		writeErr(w, 400, "未知 op")
		return
	}

	status := "alive"
	if err != nil {
		status = "dead"
	}
	_, _ = pg.ExecContext(context.Background(),
		`UPDATE shells SET status=$2, last_check=now(), last_output=$3 WHERE id=$1`,
		row.ID, status, truncateStr(out, 4000))
	if err != nil {
		writeJSON(w, 200, map[string]any{"ok": false, "error": err.Error(), "status": status})
		return
	}
	writeJSON(w, 200, map[string]any{"ok": true, "output": out, "status": status})
}

func truncateStr(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
