package server

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// 上游 a951e4a 移植（待办：鉴权读取 fail-closed + 首次设置原子化 + 密码上限）的
// 回归防御。GetSetting 对“键不存在”和“读取失败”的返回值仅 error 一个不同（两者都
// value=""). 密码处理器若把 error 当作“未设置”，就会在数据库故障期间初始化
// 入口打开。下面两个测试通过关闭连接池制造读取失败，以验证 fail-closed(503)
// 进行确认。
func TestAuthStatusFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	s.authStatus(w, httptest.NewRequest("GET", "/api/auth/status", nil))

	if w.Code != 503 {
		t.Fatalf("status=%d，预期 503（若将读取失败视为未初始化，用户会前往 /setup 并覆盖密码）；body=%s", w.Code, w.Body.String())
	}
	var payload struct {
		Initialized *bool `json:"initialized"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &payload); err == nil && payload.Initialized != nil {
		t.Fatalf("读取失败时不应回答 initialized: %v", *payload.Initialized)
	}
}

func TestAuthInitFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m}
	w := httptest.NewRecorder()
	body := strings.NewReader(`{"password":"correct horse battery","setup_token":"x"}`)
	s.authInit(w, httptest.NewRequest("POST", "/api/auth/init", body))

	if w.Code != 503 {
		t.Fatalf("status=%d，预期 503（读取失败时若放行，未认证请求将覆盖现有密码）；body=%s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "token") {
		t.Fatalf("读取失败时不应签发 token: %s", w.Body.String())
	}
}

// 检查服务端是否强制密码字节上限（bcrypt 72 字节）。73 字节以 400
// 必须被拒绝（此前 bcrypt 在哈希阶段失败导致 500）。
func TestAuthInitRejectsTooLongPasswordDB(t *testing.T) {
	rig := newAuthTestRig(t)
	tok := rig.s.currentSetupToken()

	code, body := rig.do("POST", "/api/auth/init", "", map[string]any{
		"password":    strings.Repeat("a", 73),
		"setup_token": tok,
	})
	rig.expect(400, code, body, authErrPasswordTooLong)
}

// token 校验与启动时的首次运行处理也不能把读取失败当作「无密码」处理。若这样处理
// 空哈希被缓存（passHashLoaded=true），signingKey("") 回退到原始 jwt.key。
func TestVerifyTokenFailsClosedWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}

	s := &Server{m: m, jwtKey: []byte(strings.Repeat("s", 32))}
	tok, err := signJWT(s.jwtKey)
	if err != nil {
		t.Fatal(err)
	}
	if s.verifyToken(tok) {
		t.Fatal("无法读取密码状态期间，任何 token 都不应通过")
	}
}

func TestBootstrapAuthSkipsFirstRunWhenDataSourceUnavailable(t *testing.T) {
	m, err := NewManager(t.TempDir(), "")
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer m.Close()
	if err := m.pg.Close(); err != nil {
		t.Fatal(err)
	}
	t.Setenv(adminPasswordEnv, "env-admin-pass-123")

	s := &Server{m: m}
	s.bootstrapAuth()
	s.authMu.Lock()
	loaded := s.passHashLoaded
	s.authMu.Unlock()
	if loaded {
		t.Fatal("不应将读取失败缓存为「无密码」")
	}
}

// LANCE_ADMIN_PASSWORD 初始化也只能在首次执行一次：以不同值启动的第二个实例
// 不得覆盖现有密码。
func TestBootstrapAdminPasswordEnvDoesNotOverwriteDB(t *testing.T) {
	rig := newAuthTestRig(t)
	t.Setenv(adminPasswordEnv, "first-admin-pass-123")
	rig.s.bootstrapAuth()
	first, err := rig.s.reloadPassHash()
	if err != nil || first == "" {
		t.Fatalf("首个实例必须重置密码 (err=%v)", err)
	}

	// 第二个实例：启动时读取非空，因此应立即返回，但读取与
	// 直接检查写入路径，以模拟写入之间的竞态。
	inserted, err := rig.s.m.pg.InsertSettingIfAbsent(authPassKey, "second-hash")
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("已存在的密码上 insert 不应成功")
	}
	t.Setenv(adminPasswordEnv, "second-admin-pass-456")
	s2 := &Server{m: rig.s.m, jwtKey: rig.s.jwtKey}
	s2.bootstrapAuth()
	if got, _ := s2.reloadPassHash(); got != first {
		t.Fatal("第二个实例覆盖了原有密码")
	}
}

func TestBootstrapAdminPasswordEnvRejectsOverBcryptLimitDB(t *testing.T) {
	rig := newAuthTestRig(t)
	t.Setenv(adminPasswordEnv, strings.Repeat("a", 73))
	rig.s.bootstrapAuth()
	if h, _ := rig.s.reloadPassHash(); h != "" {
		t.Fatal("超过 72 字节的环境变量密码应被忽略")
	}
	if rig.s.currentSetupToken() == "" {
		t.Fatal("忽略之后应签发配置 token")
	}
}
