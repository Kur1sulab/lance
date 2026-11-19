package server

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// 对首次运行管理员账户抢占（/api/auth/init 未认证调用）报告的修复，在没有 DB 的情况下
// 用于守护的单元测试。配置 token 比较、绑定密码哈希的 token 签名、CORS 允许
// 列表、失败尝试限制、回环绑定判定分别确认。经过 DB 的完整流程则
// auth_init_db_test.go 负责。

func TestSigningKeyBoundToPasswordHash(t *testing.T) {
	s := &Server{jwtKey: []byte(strings.Repeat("k", 32))}
	if !bytes.Equal(s.signingKey(""), s.jwtKey) {
		t.Fatal("没有密码时应直接使用文件密钥（兼容无 DB 的测试）")
	}
	tok, err := signJWT(s.signingKey("hash-A"))
	if err != nil {
		t.Fatal(err)
	}
	if !verifyJWT(tok, s.signingKey("hash-A")) {
		t.Fatal("必须能用同一哈希派生的密钥验证通过")
	}
	if verifyJWT(tok, s.signingKey("hash-B")) {
		t.Fatal("密码（哈希）变化后旧 token 应失效")
	}
	if verifyJWT(tok, s.signingKey("")) {
		t.Fatal("用哈希派生密钥签名的 token 不得通过原始密钥验证")
	}
}

func TestVerifyTokenInvalidatedByPasswordChange(t *testing.T) {
	s := &Server{jwtKey: []byte(strings.Repeat("v", 32))}
	s.setPassHash("hash-A")
	tok, err := signJWT(s.signingKey("hash-A"))
	if err != nil {
		t.Fatal(err)
	}
	if !s.verifyToken(tok) {
		t.Fatal("使用当前哈希签名的 token 应能通过")
	}
	s.setPassHash("hash-B")
	if s.verifyToken(tok) {
		t.Fatal("修改密码后旧 token 不应通过")
	}
	if s.verifyToken("") {
		t.Fatal("空 token 不得通过")
	}
}

func TestSetupTokenFromEnvAndRandom(t *testing.T) {
	t.Setenv(setupTokenEnv, "fixed-setup-token")
	s := &Server{}
	if !s.checkSetupToken("  fixed-setup-token \n") {
		t.Fatal("环境变量 token 应忽略首尾空白后匹配")
	}
	if s.checkSetupToken("fixed-setup-tokeN") || s.checkSetupToken("") {
		t.Fatal("其他值或空值应被拒绝")
	}

	t.Setenv(setupTokenEnv, "")
	s = &Server{}
	tok := s.currentSetupToken()
	if len(tok) != setupTokenLen {
		t.Fatalf("随机配置 token 长度 = %d，期望 %d", len(tok), setupTokenLen)
	}
	if s.currentSetupToken() != tok {
		t.Fatal("已生成的 token 在删除前应保持不变")
	}
	if !s.checkSetupToken(tok) || s.checkSetupToken(tok[:len(tok)-1]) {
		t.Fatal("随机 token 必须完全相等时才应通过")
	}
	s.clearSetupToken()
	if s.currentSetupToken() == tok {
		t.Fatal("初始化完成并删除后应产生新 token")
	}
}

func TestAuthLimiterCooldown(t *testing.T) {
	l := newAuthLimiter()
	now := time.Date(2026, 10, 10, 12, 0, 0, 0, time.UTC)
	l.now = func() time.Time { return now }
	const key = "203.0.113.9"

	for i := 0; i < authFreeFailures; i++ {
		if !l.allow(key) {
			t.Fatalf("第 %d 次失败之前应予允许", i+1)
		}
		l.fail(key)
	}
	if l.allow(key) {
		t.Fatalf("第 %d 次失败后应在冷却时间内被阻断", authFreeFailures)
	}
	now = now.Add(authCooldownBase + time.Second)
	if !l.allow(key) {
		t.Fatal("首次冷却时间过后应重新允许")
	}
	l.fail(key)
	now = now.Add(authCooldownBase + time.Second)
	if l.allow(key) {
		t.Fatal("每次追加失败冷却时间应翻倍")
	}
	now = now.Add(authCooldownBase)
	if !l.allow(key) {
		t.Fatal("应经过双倍冷却时间后才允许")
	}
	l.reset(key)
	l.fail(key)
	if !l.allow(key) {
		t.Fatal("初始化为成功后，允许一次失败的容差范围")
	}
	for i := 0; i < 20; i++ {
		l.fail(key)
	}
	if got := l.clients[key].until.Sub(now); got > authCooldownMax {
		t.Fatalf("冷却时间上限超出：%v > %v", got, authCooldownMax)
	}
	now = now.Add(authFailureWindow + time.Second)
	if !l.allow(key) {
		t.Fatal("失败记录窗口过后，记录应被清除并予以放行")
	}
	if l.allow("198.51.100.1") != true {
		t.Fatal("其他客户端不应受到影响")
	}
}

func TestCORSAllowlist(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := corsWith(map[string]bool{"http://localhost:5173": true}, ok)

	call := func(method, origin string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, "/api/auth/init", nil)
		if origin != "" {
			r.Header.Set("Origin", origin)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, r)
		return rec
	}

	if rec := call(http.MethodPost, ""); rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("没有 Origin 头(同源)的请求不应添加 CORS 头")
	}
	rec := call(http.MethodPost, "https://evil.example")
	if rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("不得为不在允许列表中的来源返回 Access-Control-Allow-Origin")
	}
	if rec.Header().Get("Vary") != "Origin" {
		t.Fatal("响应因来源而异，因此必须包含 Vary: Origin")
	}
	if rec := call(http.MethodOptions, "https://evil.example"); rec.Code != 204 || rec.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("未允许来源的预检请求：code=%d ACAO=%q", rec.Code, rec.Header().Get("Access-Control-Allow-Origin"))
	}
	rec = call(http.MethodPost, "http://LOCALHOST:5173")
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://LOCALHOST:5173" {
		t.Fatalf("允许的来源应忽略大小写原样返回：%q", got)
	}
	if !strings.Contains(rec.Header().Get("Access-Control-Allow-Headers"), "Authorization") {
		t.Fatal("允许的来源应允许使用 Authorization 头")
	}

	wild := corsWith(map[string]bool{"*": true}, ok)
	r := httptest.NewRequest(http.MethodGet, "/api/health", nil)
	r.Header.Set("Origin", "https://anything.example")
	w := httptest.NewRecorder()
	wild.ServeHTTP(w, r)
	if w.Header().Get("Access-Control-Allow-Origin") != "https://anything.example" {
		t.Fatal("Origin 为 * 时应允许所有来源（保持原有行为）")
	}
}

func TestCORSAllowedOriginsEnv(t *testing.T) {
	t.Setenv(corsOriginsEnv, "")
	got := corsAllowedOrigins()
	for _, o := range defaultCORSOrigins {
		if !got[o] {
			t.Fatalf("默认允许列表中没有 %s", o)
		}
	}
	t.Setenv(corsOriginsEnv, " https://UI.example.com , http://10.0.0.2:5173,, ")
	got = corsAllowedOrigins()
	if !got["https://ui.example.com"] || !got["http://10.0.0.2:5173"] || len(got) != 2 {
		t.Fatalf("环境变量列表解析结果不一致：%v", got)
	}
	if got["http://localhost:5173"] {
		t.Fatal("提供环境变量后不应再包含默认值")
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		":8787":           false,
		"0.0.0.0:8787":    false,
		"[::]:8787":       false,
		"10.0.0.5:8787":   false,
		"127.0.0.1:8787":  true,
		"127.0.0.1":       true,
		"localhost:8787":  true,
		"[::1]:8787":      true,
		"192.168.0.10:80": false,
	}
	for addr, want := range cases {
		if got := isLoopbackAddr(addr); got != want {
			t.Errorf("isLoopbackAddr(%q) = %v, 期望 %v", addr, got, want)
		}
	}
}

func TestClientKeyIgnoresForwardedFor(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/api/auth/login", nil)
	r.RemoteAddr = "198.51.100.7:40000"
	r.Header.Set("X-Forwarded-For", "1.2.3.4, 5.6.7.8")
	if got := clientKey(r); got != "198.51.100.7" {
		t.Fatalf("限制键必须是 TCP 对端地址，而不是可伪造的 X-Forwarded-For：%q", got)
	}
	if id := clientLogID(r); !strings.Contains(id, "198.51.100.7:40000") || !strings.Contains(id, "1.2.3.4") {
		t.Fatalf("日志标识符必须同时包含这两个值: %q", id)
	}
}
