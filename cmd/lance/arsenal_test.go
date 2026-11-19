package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestOpenPGSurfacesConfigParseError 钉住 CLI 回归：config.json 坏了时，
// arsenal 的 openPG 必须在连库之前就报「解析失败」，而不是误报
// "database configuration not found"（2.0.0 会把尾逗号配置当成空配置，
// 让用户去查根本不缺的 database 段）。本测试不触库：错误发生在 DSN 解析阶段。
func TestOpenPGSurfacesConfigParseError(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	// database 段齐全，但 JSON 有尾逗号——解析必失败。
	body := "{\n  \"database\": {\n    \"host\": \"127.0.0.1\",\n    \"user\": \"u\",\n    \"dbname\": \"d\",\n  }\n}\n"
	if err := os.WriteFile(cfgPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LANCE_CONFIG", cfgPath)
	t.Setenv("LANCE_PG_DSN", "")

	_, err := openPG()
	if err == nil {
		t.Fatal("坏配置必须让 openPG 报错")
	}
	if !strings.Contains(err.Error(), "解析失败") {
		t.Fatalf("应报解析错误（而非误报缺配置）: %v", err)
	}
	if strings.Contains(err.Error(), "database configuration not found") {
		t.Fatalf("坏配置被误报为缺 database 配置: %v", err)
	}
}
