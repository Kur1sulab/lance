// Package state 把每一次决策与结果追加到 JSONL。
//
// 存在的理由很实际：AI 编排最大的坑是"跑到一半忘了自己干过什么"，
// 于是重复测同一个目标、或者把已经复验过的漏洞再打一遍。
// 把历史落在盘上，下一轮直接读回来当上下文 —— 不依赖任何模型的记忆。
package state

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"time"
)

type Record struct {
	TS        string `json:"ts"`
	Round     int    `json:"round"`
	Brain     string `json:"brain"`
	Kind      string `json:"kind"`
	Signature string `json:"signature"`
	Why       string `json:"why"`
	Verdict   string `json:"verdict"`
	Detail    string `json:"detail"`
}

type Store struct{ Path string }

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	return &Store{Path: filepath.Join(dir, "lance-ledger.jsonl")}, nil
}

func (s *Store) Append(r Record) error {
	if r.TS == "" {
		r.TS = time.Now().Format(time.RFC3339)
	}
	f, err := os.OpenFile(s.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}

// History 返回已执行过的动作签名，喂给 brain 防重复。
func (s *Store) History() ([]string, error) {
	f, err := os.Open(s.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Signature != "" {
			out = append(out, r.Signature)
		}
	}
	return out, sc.Err()
}

// Round 数一下已经跑过几轮，用于记录连续性。
func (s *Store) Round() int {
	f, err := os.Open(s.Path)
	if err != nil {
		return 1
	}
	defer f.Close()
	rounds := map[int]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<20)
	for sc.Scan() {
		var r Record
		if json.Unmarshal(sc.Bytes(), &r) == nil && r.Round > 0 {
			rounds[r.Round] = true
		}
	}
	return len(rounds) + 1
}
