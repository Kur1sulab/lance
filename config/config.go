// Package config loads runtime configuration from a JSON file, with environment
// variables taking precedence. Currently it carries the PostgreSQL connection.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Database is the PostgreSQL connection config. Either set DSN directly, or set
// the component fields and a DSN is assembled from them.
type Database struct {
	DSN      string `json:"dsn"`
	Host     string `json:"host"`
	Port     int    `json:"port"`
	User     string `json:"user"`
	Password string `json:"password"`
	DBName   string `json:"dbname"`
	SSLMode  string `json:"sslmode"`
}

// Config is the on-disk config file shape.
type Config struct {
	Database Database `json:"database"`
	SkillDir string   `json:"skill_dir"`
	// Language is the bootstrap/default user-facing output language for the
	// Lance ("en"/"zh"/"es"/"ja"). Empty → DefaultLanguage. At
	// runtime a value stored in the settings table (edited in the UI) overrides it.
	Language string `json:"language"`
}

// SupportedLanguages lists the user-facing output languages Lance can
// switch between. Agent output, UI strings and backend messages all key off these.
var SupportedLanguages = []string{"en", "zh", "es", "ja"}

// DefaultLanguage is the out-of-box user-facing language when nothing is
// configured. Lance defaults to Chinese; English, Spanish and
// Japanese are selectable at runtime via the language setting (and LANCE_LANG).
const DefaultLanguage = "zh"

// NormalizeLanguage lower-cases and validates a language code, returning "" when
// the code is empty or unsupported (so callers can fall through to the next source).
func NormalizeLanguage(code string) string {
	code = strings.ToLower(strings.TrimSpace(code))
	for _, c := range SupportedLanguages {
		if c == code {
			return c
		}
	}
	return ""
}

// CurrentLanguage, when wired by the server, returns the active user-facing
// output language picked at runtime (settings table) layered over the bootstrap
// default. It is the single source backend display strings (enum labels, etc.)
// read through ActiveLanguage. nil (unit tests / standalone) → ActiveLanguage
// falls back to DefaultLanguage.
var CurrentLanguage func() string

// ActiveLanguage resolves the language for backend display strings. Precedence:
// the runtime hook (settings table, via the server) over DefaultLanguage. The
// fallback only applies when the hook is absent (unit tests / standalone).
func ActiveLanguage() string {
	if CurrentLanguage != nil {
		if v := NormalizeLanguage(CurrentLanguage()); v != "" {
			return v
		}
	}
	return DefaultLanguage
}

// Language resolves the default user-facing output language with precedence:
//
//	env LANCE_LANG  >  config file (language)  >  DefaultLanguage
//
// This is the bootstrap default only. The runtime value a user picks in the UI is
// stored in the settings table and takes precedence over this — the server layers
// the two and wires the result into the agent and message catalogs.
func Language() string {
	if v := NormalizeLanguage(os.Getenv("LANCE_LANG")); v != "" {
		return v
	}
	// 语言是尽力而为的引导项：配置坏了就回退默认值，不在此处报错；
	// 坏配置的硬错误由 PostgresDSN 在启动/连库时统一暴露。
	c, _ := Load()
	if v := NormalizeLanguage(c.Language); v != "" {
		return v
	}
	return DefaultLanguage
}

// BaseDir is the directory that anchors all runtime artifacts (config.json and
// the data/ store). It is the directory the running binary lives in, so a
// distributed executable keeps its files next to itself on any OS (Windows,
// Linux, …) regardless of the working directory it is launched from.
//
// When launched via `go run`, the binary is throwaway: it sits either in a temp
// build dir (cache miss → fresh link) OR straight inside the Go build cache
// (cache hit → run from $GOCACHE/.../...-d). We detect both and fall back to the
// current working directory so dev artifacts (data/, transcripts) and config
// resolve against the project dir, not the throwaway binary's location.
func BaseDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	dir := filepath.Dir(exe)
	if isGoRunDir(dir) {
		return "." // throwaway `go run` binary → use CWD
	}
	return dir
}

// isGoRunDir reports whether dir is where `go run` parked its executable: under
// the system temp dir (cache miss), or anywhere inside a Go build cache
// (…/go-build/…, the cache-hit case — NOT under os.TempDir(), which is why the
// old temp-only check failed intermittently). In both cases the binary is
// throwaway, so config/data must resolve against the CWD.
func isGoRunDir(dir string) bool {
	if tmp := os.TempDir(); tmp != "" {
		if rel, err := filepath.Rel(tmp, dir); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return true
		}
	}
	for _, seg := range strings.Split(filepath.ToSlash(dir), "/") {
		if seg == "go-build" {
			return true
		}
	}
	return false
}

// Path returns the config file path. Resolution order:
//  1. env LANCE_CONFIG (explicit override)
//  2. ./config.json in the current working directory (running from the project
//     dir — robust no matter where `go run` placed the temp/cached binary)
//  3. config.json next to the executable (a distributed binary keeps it beside)
//
// The first existing file wins. If none exist, the CWD path is returned so the
// "not found" message points at the project dir the user most likely expected.
func Path() string {
	if v := strings.TrimSpace(os.Getenv("LANCE_CONFIG")); v != "" {
		return v
	}
	var candidates []string
	if cwd, err := os.Getwd(); err == nil {
		candidates = append(candidates, filepath.Join(cwd, "config.json"))
	}
	candidates = append(candidates, filepath.Join(BaseDir(), "config.json"))
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c
		}
	}
	return candidates[0]
}

// Load 读取并解析配置文件。
//   - 文件不存在：返回零值 Config 且无错误（调用方回退环境变量/默认值，属正常路径）。
//   - 文件存在但读取失败（权限等）或 JSON 解析失败：返回错误。坏配置必须显式失败，
//     绝不能静默当成"没配置"——否则整份配置被当作空，用户只会看到误导性的
//     "没有 database 配置"，而真正的问题（一个尾逗号/引号）被吞掉。
func Load() (Config, error) {
	var c Config
	path := Path()
	b, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return c, nil
		}
		return c, fmt.Errorf("配置文件 %s 读取失败: %w", path, err)
	}
	if err := json.Unmarshal(b, &c); err != nil {
		return c, fmt.Errorf("配置文件 %s 解析失败%s: %w", path, jsonErrLocation(b, err), err)
	}
	return c, nil
}

// jsonErrLocation 把 JSON 错误的字节偏移换算成「第 X 行第 Y 列」，让操作者能直接
// 定位坏行；拿不到偏移的错误返回空串（不影响主错误信息）。
// 注意 Offset 的语义是「已读过的字节数」，出错字符本身在 Offset-1 处。
func jsonErrLocation(b []byte, err error) string {
	var off int64
	switch e := err.(type) {
	case *json.SyntaxError:
		off = e.Offset
	case *json.UnmarshalTypeError:
		off = e.Offset
	default:
		return ""
	}
	pos := off - 1 // 出错字符的 0-based 位置
	if pos < 0 {
		pos = 0
	}
	if pos > int64(len(b)) {
		pos = int64(len(b))
	}
	line := 1 + bytes.Count(b[:pos], []byte("\n"))
	col := pos - int64(bytes.LastIndex(b[:pos], []byte("\n")))
	return fmt.Sprintf("（第 %d 行第 %d 列）", line, col)
}

// SkillDir returns the skill root directory with precedence:
//
//	env LANCE_SKILL_DIR  >  config file (skill_dir)  >  BaseDir()/skills
//
// The directory is created if it does not exist.
func SkillDir() string {
	// 与 Language 同理：坏配置时回退默认目录，硬错误由 PostgresDSN 统一暴露。
	c, _ := Load()
	var d string
	if v := strings.TrimSpace(os.Getenv("LANCE_SKILL_DIR")); v != "" {
		d = v
	} else if v := strings.TrimSpace(c.SkillDir); v != "" {
		d = v
	} else {
		d = filepath.Join(BaseDir(), "skills")
	}
	_ = os.MkdirAll(d, 0o755)
	return d
}

// PostgresDSN resolves the connection string with precedence:
//
//	env LANCE_PG_DSN  >  config file (database.dsn, or assembled from fields)
//
// There is NO built-in fallback: when neither source supplies a database config,
// it returns an error naming the config path it inspected, so startup fails loudly
// instead of silently connecting to a wrong default. source describes where the
// DSN came from (for startup logging).
func PostgresDSN() (dsn, source string, err error) {
	if v := strings.TrimSpace(os.Getenv("LANCE_PG_DSN")); v != "" {
		return v, "environment variable LANCE_PG_DSN", nil
	}
	c, loadErr := Load()
	if loadErr != nil {
		// 配置文件存在但坏了：直接透传解析/读取错误，不再误报"没有 database 配置"。
		return "", "", loadErr
	}
	db := c.Database
	if d := strings.TrimSpace(db.DSN); d != "" {
		return d, fmt.Sprintf("config file %s (database.dsn)", Path()), nil
	}
	if db.Host != "" || db.DBName != "" || db.User != "" {
		return db.buildDSN(), fmt.Sprintf("config file %s (database fields)", Path()), nil
	}
	return "", "", fmt.Errorf("database configuration not found: environment variable LANCE_PG_DSN is not set, and config file %s has no database (dsn or host/user/dbname) configuration. Create a config file or set the environment variable, then try again", Path())
}

func (d Database) buildDSN() string {
	host := d.Host
	if host == "" {
		host = "127.0.0.1"
	}
	port := d.Port
	if port == 0 {
		port = 5432
	}
	ssl := d.SSLMode
	if ssl == "" {
		ssl = "disable"
	}
	u := url.URL{
		Scheme: "postgres",
		Host:   host + ":" + strconv.Itoa(port),
		Path:   "/" + d.DBName,
	}
	if d.User != "" {
		if d.Password != "" {
			u.User = url.UserPassword(d.User, d.Password)
		} else {
			u.User = url.User(d.User)
		}
	}
	u.RawQuery = url.Values{"sslmode": {ssl}}.Encode()
	return u.String()
}

// String is a redacted view of the resolved DSN (password masked) for logging.
func Redact(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	if u.User != nil {
		if _, hasPw := u.User.Password(); hasPw {
			u.User = url.UserPassword(u.User.Username(), "****")
		}
	}
	return fmt.Sprintf("%s", u.String())
}
