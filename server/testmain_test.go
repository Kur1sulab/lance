package server

import (
	"database/sql"
	"os"
	"testing"

	"github.com/Kur1sulab/lance/db"
	_ "github.com/jackc/pgx/v5/stdlib"
)

// TestMain acquires a PostgreSQL advisory lock (7337741002) for the entire
// server test suite so cross-package DELETE cleanup races with db/agent
// packages are avoided when running `go test ./...`.
func TestMain(m *testing.M) {
	// server.New starts the realtime notifier's background delivery loop
	// (notifyTick) unless this env var is set. The notify integration tests
	// drive stepRealtime/stepDigest directly (see newNotifyFixture) and only
	// that fixture disabled the loop per-test. Other tests construct a server
	// with a live context.Background() that is never cancelled, so their
	// notifier goroutine leaks and keeps ticking for the rest of the binary;
	// when it fires inside a later test's window it claims that test's
	// deliveries out of band and corrupts the exact per-delivery attempt
	// accounting. That is what made TestNotifyDeliveriesHistoryAndRetry flake
	// intermittently ("重试耗尽后应为 failed，得到 pending"). No test asserts on the
	// background loop itself, so disable it suite-wide for determinism; the
	// loop's dispatch is covered through direct stepRealtime/stepDigest calls.
	os.Setenv(notifyBackgroundDisabledEnv, "1")
	// The server suite verifies the localization of user-facing output, so pin the
	// default output language to Chinese for the whole test binary. Production
	// defaults to Chinese too (config.DefaultLanguage); individual tests that need
	// another language set it explicitly (t.Setenv / the settings table).
	os.Setenv("LANCE_LANG", "zh")


	dsn, _, err := db.DSN()
	if err != nil {
		os.Exit(m.Run())
	}
	conn, err := sql.Open("pgx", dsn)
	if err != nil || conn.Ping() != nil {
		os.Exit(m.Run())
	}
	defer conn.Close()
	if _, err := conn.Exec(`SELECT pg_advisory_lock(7337741002)`); err != nil {
		os.Exit(m.Run())
	}
	defer conn.Exec(`SELECT pg_advisory_unlock(7337741002)`) //nolint:errcheck
	// 清掉其它测试包（db/agent）遗留的任务：NewManager 会把库里的 running 任务
	// 连同 worker 一起复活到当前测试的 t.TempDir()，测试结束后这些 worker 仍在
	// 重试写盘，与 TempDir 清理竞争，造成 "directory not empty" 假失败。
	//  advisory 锁已持有、跨包串行，且每个测试自建 fixture、不依赖遗留数据，
	// 故此处清空是安全的（表不存在时——全新库——跳过即可）。
	if _, err := conn.Exec(`DELETE FROM tasks`); err != nil {
		_ = err // 全新测试库还没有 schema，无需清理
	}
	os.Exit(m.Run())
}
