package eval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newFrozenFixture 在临时仓库里造一个最小可冻结场景：一个"靶源码" + 一道题。
func newFrozenFixture(t *testing.T) (root string, task *Task, lockPath string) {
	t.Helper()
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "eval", "fixtures", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	artifact := filepath.Join(root, "eval", "fixtures", "x", "main.go")
	if err := os.WriteFile(artifact, []byte("package main // 靶源码 v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	taskPath := filepath.Join(root, "eval", "tasks", "t1.yaml")
	if err := os.MkdirAll(filepath.Dir(taskPath), 0o755); err != nil {
		t.Fatal(err)
	}
	yaml := `
task_id: t1-frozen
name: 冻结测试题
layer: 2
target:
  kind: exec
  cmd: ["true"]
  ready: {kind: tcp_dial, addr: "127.0.0.1:1"}
freeze:
  kind: manifest
  ref: v1
  artifacts:
    - path: eval/fixtures/x/main.go
objective:
  description: 测试用后果
  probes:
    - id: p1
      kind: http_response
      url: "http://127.0.0.1:1/"
      expect_status: [200]
scoring: {mode: outcome}
`
	if err := os.WriteFile(taskPath, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	tk, err := LoadTask(taskPath)
	if err != nil {
		t.Fatalf("加载测试题失败: %v", err)
	}
	return root, tk, filepath.Join(root, "eval", "freeze.lock")
}

func TestFreezeRoundTripAndTamperRefusal(t *testing.T) {
	root, task, lockPath := newFrozenFixture(t)

	lock, err := BuildFreezeLock(root, []*Task{task})
	if err != nil {
		t.Fatalf("建锁失败: %v", err)
	}
	if err := WriteFreezeLock(lockPath, lock); err != nil {
		t.Fatalf("写锁失败: %v", err)
	}
	loaded, raw, err := LoadFreezeLock(lockPath)
	if err != nil {
		t.Fatalf("读锁失败: %v", err)
	}
	status, err := VerifyFreeze(root, task, loaded, raw)
	if err != nil {
		t.Fatalf("刚生成的锁应该校验通过: %v", err)
	}
	if !status.Verified || status.Artifacts != 1 || len(status.LockSHA256) != 64 {
		t.Fatalf("冻结凭证不完整: %+v", status)
	}

	// 1) 改靶源码 → 必须拒绝，并指出是哪个文件 drift。
	artifact := filepath.Join(root, "eval", "fixtures", "x", "main.go")
	if err := os.WriteFile(artifact, []byte("package main // 靶源码被改了\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFreeze(root, task, loaded, raw); err == nil {
		t.Fatal("靶被改后仍然通过校验——冻结机制失效")
	} else if !strings.Contains(err.Error(), "drift") {
		t.Fatalf("错误信息应说明 drift，实际: %v", err)
	}
	// 复原后应重新通过。
	if err := os.WriteFile(artifact, []byte("package main // 靶源码 v1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFreeze(root, task, loaded, raw); err != nil {
		t.Fatalf("复原后应重新通过: %v", err)
	}

	// 2) 改题面（放宽判据）→ 必须拒绝：题面也是难度的一部分。
	orig, err := os.ReadFile(task.SourcePath)
	if err != nil {
		t.Fatal(err)
	}
	weakened := strings.Replace(string(orig), "expect_status: [200]", "expect_status: [200, 403]", 1)
	if err := os.WriteFile(task.SourcePath, []byte(weakened), 0o644); err != nil {
		t.Fatal(err)
	}
	task2, err := LoadTask(task.SourcePath)
	if err != nil {
		t.Fatalf("改后的题面应仍可解析: %v", err)
	}
	if _, err := VerifyFreeze(root, task2, loaded, raw); err == nil {
		t.Fatal("题面被改后仍然通过校验——可以靠改题降难度，冻结就没意义了")
	}
	if err := os.WriteFile(task.SourcePath, orig, 0o644); err != nil {
		t.Fatal(err)
	}

	// 3) 没登记的任务 → 拒绝。
	other := *task
	other.TaskID = "t2-unregistered"
	if _, err := VerifyFreeze(root, &other, loaded, raw); err == nil {
		t.Fatal("未登记任务应被拒绝")
	}

	// 4) 版本号被换（manifest → image_digest）→ 拒绝。
	other2 := *task
	other2.TaskID = task.TaskID
	other2.Freeze.Kind = "image_digest"
	if _, err := VerifyFreeze(root, &other2, loaded, raw); err == nil {
		t.Fatal("冻结载体类型被换应被拒绝")
	}
}

// TestFreezeLockIsSingleSourceOfTruth 验证「题面不写 sha256，以锁为准」的设计：
// 题面只声明路径，哈希全部由锁提供，避免两处各写一份、改一处忘一处。
func TestFreezeLockIsSingleSourceOfTruth(t *testing.T) {
	root, task, lockPath := newFrozenFixture(t)
	if task.Freeze.Artifacts[0].SHA256 != "" {
		t.Fatal("夹具题面不该手写哈希")
	}
	lock, err := BuildFreezeLock(root, []*Task{task})
	if err != nil {
		t.Fatal(err)
	}
	if lock.Entries[task.TaskID].Artifacts[0].SHA256 == "" {
		t.Fatal("锁里必须有哈希")
	}
	if err := WriteFreezeLock(lockPath, lock); err != nil {
		t.Fatal(err)
	}
	loaded, raw, err := LoadFreezeLock(lockPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := VerifyFreeze(root, task, loaded, raw); err != nil {
		t.Fatalf("空 sha256 应表示以锁为准: %v", err)
	}
	// 但显式写错哈希必须拒绝。
	bad := *task
	bad.Freeze.Artifacts = []FreezeArtifact{{Path: task.Freeze.Artifacts[0].Path, SHA256: strings.Repeat("0", 64)}}
	if _, err := VerifyFreeze(root, &bad, loaded, raw); err == nil {
		t.Fatal("显式写错哈希应被拒绝")
	}
}
