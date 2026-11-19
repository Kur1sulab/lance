// Package msf 是 Metasploit RPC（msfrpcd）的瘦客户端。
//
// Lance 不自己实现 C2：监听器、会话、meterpreter 全由 MSF 提供，这里只做
// 「把 JSON-RPC 翻译成平台能展示的结构」。任何一步失败都原样返回服务端错误，
// 不静默降级 —— UI 上要能看见「引擎没连上」而不是一片空白。
package msf

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Client struct {
	BaseURL string // http://127.0.0.1:55553
	User    string
	Pass    string

	mu    sync.Mutex
	token string
	http  *http.Client
}

func New(baseURL, user, pass string) *Client {
	if baseURL == "" {
		baseURL = "http://127.0.0.1:55553"
	}
	if user == "" {
		user = "msf"
	}
	return &Client{BaseURL: baseURL, User: user, Pass: pass, http: &http.Client{Timeout: 30 * time.Second}}
}

var errNoToken = errors.New("msf: 未登录或 token 已失效")

// call 走 Metasploit 的 binary/message-pack 协议：
// 请求体是 msgpack 数组 ["method", token, ...args]，响应是 map。
func (c *Client) call(ctx context.Context, method string, params ...any) (map[string]any, error) {
	c.mu.Lock()
	tok := c.token
	c.mu.Unlock()

	msg := []any{method}
	if method != "auth.login" && tok != "" {
		msg = append(msg, tok)
	}
	msg = append(msg, params...)

	body := Encode(msg)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/api/", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "binary/message-pack")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("msf: 连不上 %s（引擎没起？）: %w", c.BaseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, err
	}
	decoded, err := Decode(raw)
	if err != nil {
		return nil, fmt.Errorf("msf: 响应解析失败（HTTP %d）: %w", resp.StatusCode, err)
	}
	out, ok := decoded.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("msf: 响应不是 map（HTTP %d）", resp.StatusCode)
	}
	if e, ok := out["error"].(bool); ok && e {
		msg, _ := out["error_string"].(string)
		if msg == "" {
			msg = fmt.Sprint(out)
		}
		return nil, fmt.Errorf("msf %s: %s", method, msg)
	}
	if r, ok := out["result"].(string); ok && r == "failure" {
		msg, _ := out["error_string"].(string)
		if msg == "" {
			msg = fmt.Sprint(out)
		}
		return nil, fmt.Errorf("msf %s: %s", method, msg)
	}
	if method == "auth.login" {
		if tk, ok := out["token"].(string); ok {
			c.mu.Lock()
			c.token = tk
			c.mu.Unlock()
		}
	}
	return out, nil
}

// Login 建立会话；token 失效时调用方重新调用即可。
func (c *Client) Login(ctx context.Context) error {
	_, err := c.call(ctx, "auth.login", c.User, c.Pass)
	return err
}

func (c *Client) ensure(ctx context.Context) error {
	c.mu.Lock()
	tok := c.token
	c.mu.Unlock()
	if tok != "" {
		return nil
	}
	return c.Login(ctx)
}

// Session 是 session.list 里平台关心的字段。
type Session struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	TunnelPeer string `json:"tunnel_peer"`
	ViaExploit string `json:"via_exploit"`
	TargetHost string `json:"target_host"`
	Username  string `json:"username"`
	Platform  string `json:"platform"`
	Arch      string `json:"arch"`
	Info      string `json:"info"`
	OpenedAt  string `json:"opened_at"`
}

func (c *Client) Sessions(ctx context.Context) ([]Session, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "session.list")
	if err != nil {
		return nil, err
	}
	sessMap, _ := raw["sessions"].(map[string]any)
	out := make([]Session, 0, len(sessMap))
	for id, mv := range sessMap {
		m, _ := mv.(map[string]any)
		s := Session{ID: id}
		get := func(k string) string {
			if v, ok := m[k]; ok {
				if s2, ok := v.(string); ok {
					return s2
				}
				return fmt.Sprint(v)
			}
			return ""
		}
		s.Type, s.TunnelPeer, s.ViaExploit = get("type"), get("tunnel_peer"), get("via_exploit")
		s.TargetHost, s.Username, s.Platform = get("target_host"), get("username"), get("platform")
		s.Arch, s.Info, s.OpenedAt = get("arch"), get("info"), get("opened_at")
		out = append(out, s)
	}
	return out, nil
}

// ShellWrite 往会话里写一行命令（shell 型会话）。
func (c *Client) ShellWrite(ctx context.Context, sid, cmd string) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	_, err := c.call(ctx, "session.shell_write", sid, cmd+"\n")
	return err
}

// ShellRead 取回会话输出（可能为空）。
func (c *Client) ShellRead(ctx context.Context, sid string) (string, error) {
	if err := c.ensure(ctx); err != nil {
		return "", err
	}
	raw, err := c.call(ctx, "session.shell_read", sid)
	if err != nil {
		return "", err
	}
	data, _ := raw["data"].(string)
	return data, nil
}

// MeterpreterWrite / Read 对应 meterpreter 会话。
func (c *Client) MeterpreterWrite(ctx context.Context, sid, cmd string) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	_, err := c.call(ctx, "session.meterpreter_write", sid, cmd+"\n")
	return err
}

func (c *Client) MeterpreterRead(ctx context.Context, sid string) (string, error) {
	if err := c.ensure(ctx); err != nil {
		return "", err
	}
	raw, err := c.call(ctx, "session.meterpreter_read", sid)
	if err != nil {
		return "", err
	}
	data, _ := raw["data"].(string)
	return data, nil
}

// Jobs 列出后台任务（监听器就是 handler 的 job）。
func (c *Client) Jobs(ctx context.Context) (map[string]map[string]any, error) {
	if err := c.ensure(ctx); err != nil {
		return nil, err
	}
	raw, err := c.call(ctx, "job.list")
	if err != nil {
		return nil, err
	}
	out := make(map[string]map[string]any, len(raw))
	for k, v := range raw {
		if m, ok := v.(map[string]any); ok {
			out[k] = m
		}
	}
	return out, nil
}

// StartHandler 起一个 listener：payload 类型 + lhost/lport → 返回 job id。
func (c *Client) StartHandler(ctx context.Context, payload, lhost string, lport int) (string, error) {
	if err := c.ensure(ctx); err != nil {
		return "", err
	}
	opts := map[string]string{
		"PAYLOAD":       payload,
		"LHOST":         lhost,
		"LPORT":         fmt.Sprint(lport),
		"ExitOnSession": "false",
	}
	raw, err := c.call(ctx, "module.execute", "exploit", "multi/handler", opts)
	if err != nil {
		return "", err
	}
	return fmt.Sprint(raw["job_id"]), nil
}

// StopJob 停掉监听器。
func (c *Client) StopJob(ctx context.Context, jobID string) error {
	if err := c.ensure(ctx); err != nil {
		return err
	}
	_, err := c.call(ctx, "job.stop", jobID)
	return err
}
