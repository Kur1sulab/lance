package db

import (
	"fmt"
	"testing"
	"time"
)

// InsertSettingIfAbsent 是 auth.password_hash 的安全网：调用方的 GetSetting 检查
// 可能因数据库错误而失效，也可能被并发请求插队（bcrypt 会耗费数十
// 使用毫秒）。因此“仅首次设置一次”的保证不应放在 if 判断上，而应落在主键约束中
// 执行。
func TestInsertSettingIfAbsentDoesNotOverwrite(t *testing.T) {
	d, err := Open(testDSN(t))
	if err != nil {
		t.Skipf("postgres unavailable (%v) — skipping", err)
	}
	defer d.Close()

	// 使用测试专用键，不触碰开发数据库中的真实 auth.password_hash。
	key := fmt.Sprintf("test.insert_if_absent.%d", time.Now().UnixNano())
	defer func() { _, _ = d.Exec(`DELETE FROM settings WHERE key=$1`, key) }()

	inserted, err := d.InsertSettingIfAbsent(key, "first")
	if err != nil {
		t.Fatal(err)
	}
	if !inserted {
		t.Fatal("首次写入时 inserted=true")
	}

	inserted, err = d.InsertSettingIfAbsent(key, "second")
	if err != nil {
		t.Fatal(err)
	}
	if inserted {
		t.Fatal("若键已存在，则 inserted 必须为 false")
	}

	got, ok, err := d.GetSetting(key)
	if err != nil || !ok {
		t.Fatalf("GetSetting: ok=%v err=%v", ok, err)
	}
	if got != "first" {
		t.Fatalf("值已被 %q 覆盖，必须保留 %q", got, "first")
	}
}
