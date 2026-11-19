package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPostgresDSNPrecedence(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	os.WriteFile(cfgPath, []byte(`{"database":{"host":"10.1.2.3","port":6000,"user":"u","password":"p","dbname":"d","sslmode":"require"}}`), 0o644)
	t.Setenv("LANCE_CONFIG", cfgPath)

	// no env DSN → built from config file fields
	t.Setenv("LANCE_PG_DSN", "")
	got, _, err := PostgresDSN()
	want := "postgres://u:p@10.1.2.3:6000/d?sslmode=require"
	if err != nil || got != want {
		t.Fatalf("from file: got %q err %v want %q", got, err, want)
	}

	// env wins over config file
	t.Setenv("LANCE_PG_DSN", "postgres://envwins/x")
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://envwins/x" {
		t.Fatalf("env should win, got %q err %v", got, err)
	}

	// no env, no file → error (no built-in default)
	t.Setenv("LANCE_PG_DSN", "")
	t.Setenv("LANCE_CONFIG", filepath.Join(dir, "nope.json"))
	if got, _, err := PostgresDSN(); err == nil {
		t.Fatalf("missing config should error, got %q", got)
	}

	// full dsn in config file is used verbatim
	os.WriteFile(cfgPath, []byte(`{"database":{"dsn":"postgres://full/dsn"}}`), 0o644)
	t.Setenv("LANCE_CONFIG", cfgPath)
	if got, _, err := PostgresDSN(); err != nil || got != "postgres://full/dsn" {
		t.Fatalf("file dsn verbatim, got %q err %v", got, err)
	}
}

// TestLoadSurfacesParseError 钉住回归：config.json 存在但 JSON 坏了时，
// Load/PostgresDSN 必须报「解析失败」并给出位置，而不是静默返回零值配置、
// 再误报"没有 database 配置"（2.0.0 的真实坑：一个尾逗号让整份配置被当成空）。
func TestLoadSurfacesParseError(t *testing.T) {
	cases := []struct {
		name string
		body string
		loc  string // 期望错误里出现的位置片段（空=不检查）
	}{
		{"尾逗号", "{\n  \"database\": {\n    \"host\": \"127.0.0.1\",\n  },\n}\n", "第 4 行"},
		{"缺值", `{"a":}`, "第 1 行第 6 列"},
		{"未闭合", `{"database":{"host":"127.0.0.1"`, ""},
		{"类型错误", `{"database":{"port":"5433"}}`, "第 1 行"},
		{"空文件", "", ""},
		{"纯垃圾", "not json at all", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfgPath := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(cfgPath, []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LANCE_CONFIG", cfgPath)
			t.Setenv("LANCE_PG_DSN", "")

			_, err := Load()
			if err == nil {
				t.Fatalf("坏配置必须报错，body=%q", tc.body)
			}
			if !strings.Contains(err.Error(), "解析失败") || !strings.Contains(err.Error(), cfgPath) {
				t.Fatalf("错误应含「解析失败」与文件路径，got: %v", err)
			}
			if tc.loc != "" && !strings.Contains(err.Error(), tc.loc) {
				t.Fatalf("错误应含位置 %q，got: %v", tc.loc, err)
			}
			// PostgresDSN 不得再把坏配置误报成"没有 database 配置"。
			_, _, err = PostgresDSN()
			if err == nil || !strings.Contains(err.Error(), "解析失败") {
				t.Fatalf("PostgresDSN 应透传解析错误，got: %v", err)
			}
			if strings.Contains(err.Error(), "has no database") {
				t.Fatalf("坏配置被误报为缺 database 段: %v", err)
			}
		})
	}
}

// TestLoadMissingFileGraceful 文件不存在仍是正常路径：零值配置 + 无错误，
// 各调用方回退环境变量/默认值（保持 2.0.0 行为）。
func TestLoadMissingFileGraceful(t *testing.T) {
	t.Setenv("LANCE_CONFIG", filepath.Join(t.TempDir(), "nope.json"))
	t.Setenv("LANCE_PG_DSN", "")
	t.Setenv("LANCE_LANG", "")
	c, err := Load()
	if err != nil {
		t.Fatalf("缺失文件不应报错: %v", err)
	}
	if c.Language != "" || c.Database.User != "" {
		t.Fatalf("缺失文件应返回零值配置: %+v", c)
	}
	if got := Language(); got != DefaultLanguage {
		t.Fatalf("Language 应回退默认 %q, got %q", DefaultLanguage, got)
	}
	if _, _, err := PostgresDSN(); err == nil {
		t.Fatal("缺失文件且无 env DSN 时 PostgresDSN 应报错")
	}
}

// TestPostgresDSNEnvWinsOverBrokenFile 优先级回归：env DSN 在场时，
// 即使配置文件是坏的也直接用 env（不读文件、不报错）。
func TestPostgresDSNEnvWinsOverBrokenFile(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(cfgPath, []byte(`{"database":`), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANCE_CONFIG", cfgPath)
	t.Setenv("LANCE_PG_DSN", "postgres://envwins/x")
	got, src, err := PostgresDSN()
	if err != nil || got != "postgres://envwins/x" || !strings.Contains(src, "LANCE_PG_DSN") {
		t.Fatalf("env 应优先于坏配置: got=%q src=%q err=%v", got, src, err)
	}
}

// TestLoadValidFile 正常配置照常解析（修复不得改变合法路径）。
func TestLoadValidFile(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	body := `{"language":"zh","skill_dir":"/tmp/sk","database":{"user":"u","dbname":"d"}}`
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANCE_CONFIG", cfgPath)
	c, err := Load()
	if err != nil {
		t.Fatalf("合法配置不应报错: %v", err)
	}
	if c.Language != "zh" || c.SkillDir != "/tmp/sk" || c.Database.User != "u" || c.Database.DBName != "d" {
		t.Fatalf("字段解析不符: %+v", c)
	}
}
