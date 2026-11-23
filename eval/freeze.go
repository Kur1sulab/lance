package eval

// 冻结机制：eval/freeze.lock 记录每个靶的版本与逐文件 SHA256，加载时校验，
// 不一致就拒绝运行。
//
// 为什么必须「拒绝运行」而不是「打警告」：这一层唯一的价值是分数的可比性。
// 一旦允许带着 drift 跑，得到的分数就既不能横向比（不同时间不是同一个靶），
// 也不能纵向比（不知道涨的是能力还是靶变简单了）。研究界已经吃过这个亏：
// 同一批 CVE 从单文件裁剪版换到全仓库真实构建，通过率从 66.7% 掉到 0.7%——
// 靶的形态变了，分数就完全不是一回事。所以宁可跑不起来，也不出一个脏分数。
//
// 三重比对，缺一不可：
//  1. 任务 id 必须在锁里登记过（没登记 = 没冻结 = 拒绝）；
//  2. 任务声明的 kind/ref 与锁一致（防止偷偷把 image_digest 换成 manifest）；
//  3. 任务文件自身的 SHA256 + 每个冻结物的磁盘 SHA256 都与锁一致
//     （防止改题面放宽判据，也防止改靶源码）。
// 第 3 条同时覆盖「改题」与「改靶」，因为一道题的难度是这两者共同决定的。

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// FreezeLockVersion 是锁文件格式版本。格式变更时递增，旧锁会被要求重生成。
const FreezeLockVersion = 1

// FreezeLock 是 eval/freeze.lock 的内存形态。
type FreezeLock struct {
	Version     int                     `json:"version"`
	GeneratedAt string                  `json:"generated_at"`
	Generator   string                  `json:"generator"`
	GitCommit   string                  `json:"git_commit,omitempty"`
	Entries     map[string]*FreezeEntry `json:"entries"`
}

// FreezeEntry 是单个任务的冻结登记。
type FreezeEntry struct {
	TaskID     string           `json:"task_id"`
	TaskFile   string           `json:"task_file"`
	TaskSHA256 string           `json:"task_sha256"`
	Layer      int              `json:"layer"`
	Kind       string           `json:"kind"`
	Ref        string           `json:"ref"`
	Artifacts  []FreezeArtifact `json:"artifacts"`
	FrozenAt   string           `json:"frozen_at"`
}

// FreezeStatus 是「本次运行用的是什么冻结版本」的凭证，会原样写进 scorecard。
// 没有这一段的分数不可比较，所以它是必填字段而不是附加信息。
type FreezeStatus struct {
	Verified   bool   `json:"verified"`
	Kind       string `json:"kind"`
	Ref        string `json:"ref"`
	LockSHA256 string `json:"lock_sha256"`
	Artifacts  int    `json:"artifacts"`
	TaskSHA256 string `json:"task_sha256"`
}

// LoadFreezeLock 读锁文件，同时返回原始字节（用于计算锁指纹）。
func LoadFreezeLock(path string) (*FreezeLock, []byte, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, fmt.Errorf("读取冻结锁失败 %s: %w（没有锁就不能跑：先 go run ./cmd/evald -write-freeze）", path, err)
	}
	var lock FreezeLock
	dec := json.NewDecoder(strings.NewReader(string(raw)))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&lock); err != nil {
		return nil, nil, fmt.Errorf("解析冻结锁失败 %s: %w", path, err)
	}
	if lock.Version != FreezeLockVersion {
		return nil, nil, fmt.Errorf("冻结锁版本 %d 与本程序要求的 %d 不一致，请重新生成", lock.Version, FreezeLockVersion)
	}
	if lock.Entries == nil {
		lock.Entries = map[string]*FreezeEntry{}
	}
	return &lock, raw, nil
}

// WriteFreezeLock 以稳定格式写锁（键有序、两空格缩进、结尾换行），
// 这样 diff 里只有真正变化的哈希，评审时一眼能看出是哪个靶动了。
func WriteFreezeLock(path string, lock *FreezeLock) error {
	lock.Version = FreezeLockVersion
	if lock.GeneratedAt == "" {
		lock.GeneratedAt = time.Now().Format(time.RFC3339)
	}
	data, err := json.MarshalIndent(lock, "", "  ")
	if err != nil {
		return fmt.Errorf("序列化冻结锁失败: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("创建锁目录失败: %w", err)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("写临时锁文件失败: %w", err)
	}
	// 先写临时文件再 rename：中途失败不会留下半截锁，避免「锁存在但不可用」的假象。
	if err := os.Rename(tmp, path); err != nil {
		return fmt.Errorf("替换锁文件失败: %w", err)
	}
	return nil
}

// BuildFreezeLock 按当前磁盘内容重建锁。这是一次显式的「我确认要换版本」动作，
// 只应该在建新题、或有意升级靶版本时执行；日常 CI 只做校验，不重建。
func BuildFreezeLock(repoRoot string, tasks []*Task) (*FreezeLock, error) {
	lock := &FreezeLock{
		Version:   FreezeLockVersion,
		Generator: "go run ./cmd/evald -write-freeze",
		GitCommit: detectGitCommit(repoRoot),
		Entries:   map[string]*FreezeEntry{},
	}
	now := time.Now().Format(time.RFC3339)
	for _, t := range tasks {
		relTask, err := relToRoot(repoRoot, t.SourcePath)
		if err != nil {
			return nil, err
		}
		taskHash, _, err := hashFile(t.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("任务 %s: %w", t.TaskID, err)
		}
		arts := make([]FreezeArtifact, 0, len(t.Freeze.Artifacts))
		for _, a := range t.Freeze.Artifacts {
			full := filepath.Join(repoRoot, filepath.FromSlash(a.Path))
			sum, _, err := hashFile(full)
			if err != nil {
				return nil, fmt.Errorf("任务 %s 的冻结物 %s 不可读: %w", t.TaskID, a.Path, err)
			}
			arts = append(arts, FreezeArtifact{Path: a.Path, SHA256: sum})
		}
		sort.Slice(arts, func(i, j int) bool { return arts[i].Path < arts[j].Path })
		lock.Entries[t.TaskID] = &FreezeEntry{
			TaskID:     t.TaskID,
			TaskFile:   relTask,
			TaskSHA256: taskHash,
			Layer:      t.Layer,
			Kind:       t.Freeze.Kind,
			Ref:        t.Freeze.Ref,
			Artifacts:  arts,
			FrozenAt:   now,
		}
	}
	return lock, nil
}

// VerifyFreeze 比对任务声明、磁盘现状与锁三方。任何一处不一致都返回错误。
// 错误信息里给「怎么修」，因为这道闸门在开发期会经常被有意触发。
func VerifyFreeze(repoRoot string, task *Task, lock *FreezeLock, lockRaw []byte) (*FreezeStatus, error) {
	entry, ok := lock.Entries[task.TaskID]
	if !ok {
		return nil, fmt.Errorf("任务 %s 未在 freeze.lock 登记，拒绝运行（新题请先 go run ./cmd/evald -write-freeze 并评审）", task.TaskID)
	}
	if entry.Kind != task.Freeze.Kind || entry.Ref != task.Freeze.Ref {
		return nil, fmt.Errorf("任务 %s 的冻结版本与锁不一致：任务声明 kind=%s ref=%s，锁登记 kind=%s ref=%s",
			task.TaskID, task.Freeze.Kind, task.Freeze.Ref, entry.Kind, entry.Ref)
	}

	// 题面本身也冻结：放宽判据（比如把 expect_contains 改短）等于降难度。
	if entry.TaskSHA256 != "" {
		sum, _, err := hashFile(task.SourcePath)
		if err != nil {
			return nil, fmt.Errorf("任务 %s 的题面文件不可读: %w", task.TaskID, err)
		}
		if sum != entry.TaskSHA256 {
			return nil, fmt.Errorf("任务 %s 题面已改动（锁内 %s，磁盘 %s），拒绝运行；确认要改版本就重新生成锁",
				task.TaskID, short(entry.TaskSHA256), short(sum))
		}
	}

	// 清单必须集合相等：少一项等于少冻一个文件，多一项等于冻了任务没声明的东西。
	got := map[string]string{}
	for _, a := range task.Freeze.Artifacts {
		got[a.Path] = a.SHA256
	}
	want := map[string]string{}
	for _, a := range entry.Artifacts {
		want[a.Path] = a.SHA256
	}
	if len(got) != len(want) {
		return nil, fmt.Errorf("任务 %s 的冻结清单与锁不一致（任务 %d 项，锁 %d 项）", task.TaskID, len(got), len(want))
	}
	paths := make([]string, 0, len(got))
	for p := range got {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		lockSum, ok := want[p]
		if !ok {
			return nil, fmt.Errorf("任务 %s 声明的冻结物 %s 不在锁里，拒绝运行", task.TaskID, p)
		}
		if got[p] != "" && got[p] != lockSum {
			return nil, fmt.Errorf("任务 %s 声明的 %s 哈希与锁不一致（任务 %s，锁 %s），拒绝运行",
				task.TaskID, p, short(got[p]), short(lockSum))
		}
		full := filepath.Join(repoRoot, filepath.FromSlash(p))
		if _, err := ensureInside(repoRoot, full); err != nil {
			return nil, fmt.Errorf("冻结物 %s 路径越界: %w", p, err)
		}
		sum, _, err := hashFile(full)
		if err != nil {
			return nil, fmt.Errorf("冻结物 %s 不可读: %w", p, err)
		}
		if sum != lockSum {
			return nil, fmt.Errorf("冻结物已 drift：%s（锁内 %s，磁盘 %s），拒绝运行——靶变了，历史分数不可比",
				p, short(lockSum), short(sum))
		}
	}

	sum := sha256.Sum256(lockRaw)
	return &FreezeStatus{
		Verified:   true,
		Kind:       entry.Kind,
		Ref:        entry.Ref,
		LockSHA256: hex.EncodeToString(sum[:]),
		Artifacts:  len(entry.Artifacts),
		TaskSHA256: entry.TaskSHA256,
	}, nil
}

func hashFile(path string) (string, int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}

// relToRoot 把绝对路径转成仓库相对路径，顺带做越界检查。
func relToRoot(root, path string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("路径 %s 在仓库根 %s 之外", path, root)
	}
	return filepath.ToSlash(rel), nil
}

// ensureInside 返回规范化后的绝对路径，并拒绝逃出 root 的路径。
// 任务文件是可评审的输入，但「可评审」不等于「可信任」：判据里的 file_read
// 如果允许读任意路径，一道恶意题就能把评测机上的密钥读进 scorecard。
func ensureInside(root, path string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", err
	}
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return "", err
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%s 不在允许的根 %s 内", absPath, absRoot)
	}
	return absPath, nil
}

func detectGitCommit(root string) string {
	cmd := exec.Command("git", "-C", root, "rev-parse", "HEAD")
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func short(s string) string {
	if len(s) <= 12 {
		return s
	}
	return s[:12]
}
