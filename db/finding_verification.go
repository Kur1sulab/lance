package db

// 6.2 验证质量一等闸门：状态机 + 可重放率 + 误报率。
//
// 设计红线（不可协商，改动前先读完这段）：
//
//  1. 只有 verified 的发现才允许进报告导出。闸门在后端导出路径上硬拦
//     （见 server.findingsExport），不是前端隐藏按钮。
//  2. 判定依据只能是环境 ground-truth：目标真实响应、平台按证据重放的结果、
//     人工用独立工具复现的结果。**LLM / Agent 自报一律不接受**——
//     LLM 说「我验证过了」不是证据，是待验证的断言。代码层用
//     IsGroundTruthMethod 白名单硬拒，注释、API 文档、报告头部都写死这条。
//  3. 两个指标的分母口径固定：
//     可重放率 = 可重放数 / verified 总数
//     误报率   = refuted / (verified + refuted)
//     分母为 0 时比率记 0，同时样本数一起返回，调用方必须看样本数。
//     不把 unverified / unreproducible 混进误报率分母：那是「未判定」，不是「判错」。
//
// 为什么：XBOW 的误报经济学——0–10% 才有赏金正循环。误报率是商业化前提，
// 不是优化项；指标必须由环境事实算出来，不能由被测对象自己报。

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"strings"
	"time"
)

// 验证状态（findings.verification_status）。
const (
	VerificationUnverified     = "unverified"     // 未判定：默认态，永不进报告
	VerificationVerified       = "verified"       // 已验证：环境事实证实存在
	VerificationRefuted        = "refuted"        // 已推翻：环境事实证明不存在 → 计误报
	VerificationUnreproducible = "unreproducible" // 不可复现：证据不足以证实或推翻
)

// 判定方法白名单。全部是「环境事实」来源；任何模型自报的措辞都不在表里，
// 因此会被 IsGroundTruthMethod 拒绝。
const (
	// VerifyMethodReplay 平台按证据库里的报文重建请求并真实发出，拿目标响应作为判定依据。
	VerifyMethodReplay = "replay"
	// VerifyMethodManualReplay 人工用独立工具（curl / 浏览器 / 自己的脚本）复现，
	// ground_truth 里必须贴真实响应或命令输出。
	VerifyMethodManualReplay = "manual_replay"
	// VerifyMethodTargetResponse 目标真实响应本身即判定依据（例如返回 200 + 敏感数据、
	// 或返回 404/错误页证明漏洞不存在）。
	VerifyMethodTargetResponse = "target_response"
	// VerifyMethodEvidenceChanged 仅系统内部使用：证据被替换，原判定失效，退回 unverified。
	VerifyMethodEvidenceChanged = "evidence_changed"
)

// verificationMethods 是允许出现在 finding_verifications.method 里的全部取值。
var verificationMethods = map[string]bool{
	VerifyMethodReplay:         true,
	VerifyMethodManualReplay:   true,
	VerifyMethodTargetResponse: true,
	VerifyMethodEvidenceChanged: true,
}

// llmMethodMarkers 是模型自报的特征词。出现在 method 里即拒绝——
// 宁可让人改成 manual_replay 并贴真实输出，也不接受「模型说已验证」。
var llmMethodMarkers = []string{
	"llm", "gpt", "claude", "model", "agent", "self_report", "selfreport",
	"ai_", "_ai", "reasoning", "prompt",
}

// IsGroundTruthMethod 报告 method 是否是合法的环境事实判定方法。
// 这是 6.2 的硬闸门之一：模型自报永远返回 false。
func IsGroundTruthMethod(method string) bool {
	m := strings.ToLower(strings.TrimSpace(method))
	if m == "" {
		return false
	}
	for _, marker := range llmMethodMarkers {
		if strings.Contains(m, marker) {
			return false
		}
	}
	return verificationMethods[m]
}

// ValidVerificationStatus 报告 s 是否是已知验证状态。
func ValidVerificationStatus(s string) bool {
	switch s {
	case VerificationUnverified, VerificationVerified, VerificationRefuted, VerificationUnreproducible:
		return true
	}
	return false
}

// VerificationStatuses 返回全部验证状态（用于 API 文档 / 前端枚举）。
func VerificationStatuses() []string {
	return []string{VerificationUnverified, VerificationVerified, VerificationRefuted, VerificationUnreproducible}
}

// verificationTransitions 是状态机。键 = from，值 = 允许到达的 to。
// 注意 verified 没有回 unverified 的边：撤销必须走 refuted（留证据），
// 唯一的降级通道是证据替换时的系统失效 InvalidateFindingVerification。
var verificationTransitions = map[string][]string{
	VerificationUnverified:     {VerificationVerified, VerificationRefuted, VerificationUnreproducible},
	VerificationVerified:       {VerificationRefuted, VerificationUnreproducible},
	VerificationRefuted:        {VerificationVerified, VerificationUnreproducible},
	VerificationUnreproducible: {VerificationVerified, VerificationRefuted},
}

// CanTransitionVerification 报告 from → to 是否合法。from == to 视为「重申判定」：
// verified / refuted 都是确定性结论，补了新证据后可以重新判一遍（刷新 verified_at
// 并追加一条流水）。unverified 没有「重申」的意义，unreproducible 重申也不产生新信息，
// 这两者都不允许。
func CanTransitionVerification(from, to string) bool {
	if !ValidVerificationStatus(from) || !ValidVerificationStatus(to) {
		return false
	}
	if from == to {
		return from == VerificationVerified || from == VerificationRefuted
	}
	for _, allowed := range verificationTransitions[from] {
		if allowed == to {
			return true
		}
	}
	return false
}

// ErrFindingNotFound 由 GetFinding 等使用；这里复用同一语义。
var ErrVerificationInvalid = errors.New("验证判定不合法")

// VerificationTransitionError 描述一次被拒的流转，带上人话原因。
type VerificationTransitionError struct {
	From string
	To   string
	Why  string
}

func (e *VerificationTransitionError) Error() string {
	return fmt.Sprintf("非法验证流转 %s → %s：%s", e.From, e.To, e.Why)
}

// IsVerificationInvalid 让上层（HTTP 层）把「判定不合法」映射成 400 而不是 500。
func IsVerificationInvalid(err error) bool {
	var te *VerificationTransitionError
	return errors.As(err, &te) || errors.Is(err, ErrVerificationInvalid)
}

// VerificationRecord 是 finding_verifications 的一行（判定流水）。
type VerificationRecord struct {
	ID         int64     `json:"id,string"`
	FindingID  int64     `json:"finding_id,string"`
	FromStatus string    `json:"from_status"`
	ToStatus   string    `json:"to_status"`
	Method     string    `json:"method"`
	Actor      string    `json:"actor"`
	Note       string    `json:"note"`
	GroundTruth string   `json:"ground_truth"`
	CreatedAt  time.Time `json:"created_at"`
}

// FindingReplay 是 finding_replays 的一行（重放流水，可重放率的事实来源）。
type FindingReplay struct {
	ID              int64     `json:"id,string"`
	FindingID       int64     `json:"finding_id,string"`
	BindingID       *int64    `json:"binding_id,string,omitempty"`
	SnapshotID      string    `json:"snapshot_id"`
	Replayed        bool      `json:"replayed"`
	MatchesOriginal bool      `json:"matches_original"`
	StatusCode      int       `json:"status_code"`
	RespLen         int64     `json:"resp_len"`
	RespSHA256      string    `json:"resp_sha256"`
	Error           string    `json:"error"`
	ElapsedMS       int64     `json:"elapsed_ms"`
	CreatedAt       time.Time `json:"created_at"`
}

// VerificationState 是「一条发现的验证现状」，GET 接口直接返回它。
type VerificationState struct {
	FindingID       int64                `json:"finding_id,string"`
	Status          string               `json:"status"`
	Method          string               `json:"method"`
	Actor           string               `json:"actor"`
	Note            string               `json:"note"`
	GroundTruth     string               `json:"ground_truth"`
	VerifiedAt      *time.Time           `json:"verified_at,omitempty"`
	EvidenceCount   int                  `json:"evidence_count"`   // 绑定的报文证据条数
	Replayable      bool                 `json:"replayable"`       // 至少一次成功重放
	LastReplay      *FindingReplay       `json:"last_replay,omitempty"`
	History         []VerificationRecord `json:"history"`
	// AllowedNext 给前端/调用方看「下一步能标什么」，避免猜状态机。
	AllowedNext []string `json:"allowed_next"`
}

// VerificationInput 是一次 ground-truth 判定请求。
type VerificationInput struct {
	FindingID  int64
	Status     string
	Method     string
	Actor      string
	Note       string
	GroundTruth string
}

// SetFindingVerification 执行一次状态流转：校验 → 加锁读现状 → 更新 → 追加流水。
// 校验失败返回 *VerificationTransitionError（HTTP 层映射 400）；
// 发现不存在返回 ErrFindingNotFound（映射 404）。
func (d *DB) SetFindingVerification(ctx context.Context, in VerificationInput) (*VerificationState, error) {
	in.Status = strings.TrimSpace(in.Status)
	in.Method = strings.TrimSpace(in.Method)
	in.Actor = strings.TrimSpace(in.Actor)
	in.Note = strings.TrimSpace(in.Note)
	in.GroundTruth = strings.TrimSpace(in.GroundTruth)

	if !ValidVerificationStatus(in.Status) {
		return nil, &VerificationTransitionError{To: in.Status, Why: "未知验证状态（只接受 verified / refuted / unreproducible）"}
	}
	if in.Status == VerificationUnverified {
		return nil, &VerificationTransitionError{
			To:  in.Status,
			Why: "不能把发现直接标回 unverified：撤销判定请标 refuted 并附证据；证据被替换时由系统自动失效",
		}
	}
	if !IsGroundTruthMethod(in.Method) {
		return nil, &VerificationTransitionError{
			To:  in.Status,
			Why: fmt.Sprintf("判定方法 %q 不是环境事实来源。只接受 %s / %s / %s；"+
				"LLM、Agent 或模型自报的结论不作为判定依据", in.Method,
				VerifyMethodReplay, VerifyMethodManualReplay, VerifyMethodTargetResponse),
		}
	}
	if in.Actor == "" {
		return nil, &VerificationTransitionError{To: in.Status, Why: "必须写明判定主体（actor），例如 operator:alice / replay-engine / curl"}
	}
	if in.GroundTruth == "" {
		return nil, &VerificationTransitionError{
			To:  in.Status,
			Why: "必须附环境事实（ground_truth）：目标真实响应片段、独立复现的命令输出或重放结果；不接受「模型认为」这类描述",
		}
	}

	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var from string
	err = tx.QueryRowContext(ctx, `SELECT verification_status FROM findings WHERE id=$1 FOR UPDATE`, in.FindingID).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFindingNotFound
	}
	if err != nil {
		return nil, err
	}
	if !CanTransitionVerification(from, in.Status) {
		return nil, &VerificationTransitionError{
			From: from,
			To:   in.Status,
			Why:  "状态机不允许这一步（" + strings.Join(verificationTransitions[from], " / ") + " 才可到达）",
		}
	}

	var verifiedAt *time.Time
	if in.Status == VerificationVerified {
		now := time.Now()
		verifiedAt = &now
	}
	if _, err = tx.ExecContext(ctx, `UPDATE findings SET
			verification_status=$2, verification_method=$3, verification_actor=$4,
			verification_note=$5, verification_ground_truth=$6,
			verified_at = CASE WHEN $2='verified' THEN COALESCE($7, verified_at) ELSE NULL END
		WHERE id=$1`,
		in.FindingID, in.Status, in.Method, in.Actor, in.Note, in.GroundTruth, verifiedAt); err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO finding_verifications
			(finding_id, from_status, to_status, method, actor, note, ground_truth)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		in.FindingID, from, in.Status, in.Method, in.Actor, in.Note, in.GroundTruth); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return d.GetFindingVerification(ctx, in.FindingID)
}

// InvalidateFindingVerification 是系统专用降级通道：证据被替换后原判定失效。
// 只在 finding 当前为 verified/refuted 时生效，退到 unverified 并留流水。
func (d *DB) InvalidateFindingVerification(ctx context.Context, findingID int64, reason string) error {
	if strings.TrimSpace(reason) == "" {
		reason = "证据已替换，原判定失效"
	}
	tx, err := d.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var from string
	err = tx.QueryRowContext(ctx, `SELECT verification_status FROM findings WHERE id=$1 FOR UPDATE`, findingID).Scan(&from)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrFindingNotFound
	}
	if err != nil {
		return err
	}
	if from == VerificationUnverified {
		return nil // 已经是未判定，无需失效
	}
	if _, err = tx.ExecContext(ctx, `UPDATE findings SET verification_status=$2,
			verification_method=$3, verification_note=$4, verified_at=NULL
		WHERE id=$1`, findingID, VerificationUnverified, VerifyMethodEvidenceChanged, reason); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO finding_verifications
			(finding_id, from_status, to_status, method, actor, note, ground_truth)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`,
		findingID, from, VerificationUnverified, VerifyMethodEvidenceChanged,
		"system", reason, "证据版本变更（evidence_version 递增）"); err != nil {
		return err
	}
	return tx.Commit()
}

// GetFindingVerification 读一条发现的验证现状 + 流水 + 最近一次重放。
func (d *DB) GetFindingVerification(ctx context.Context, findingID int64) (*VerificationState, error) {
	st := &VerificationState{FindingID: findingID, History: []VerificationRecord{}}
	err := d.QueryRowContext(ctx, `SELECT verification_status, verification_method, verification_actor,
			verification_note, verification_ground_truth, verified_at
		FROM findings WHERE id=$1`, findingID).
		Scan(&st.Status, &st.Method, &st.Actor, &st.Note, &st.GroundTruth, &st.VerifiedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrFindingNotFound
	}
	if err != nil {
		return nil, err
	}

	if err = d.QueryRowContext(ctx, `SELECT count(*) FROM finding_traffic_bindings WHERE finding_id=$1`, findingID).
		Scan(&st.EvidenceCount); err != nil {
		return nil, err
	}

	rows, err := d.QueryContext(ctx, `SELECT id, finding_id, from_status, to_status, method, actor, note, ground_truth, created_at
		FROM finding_verifications WHERE finding_id=$1 ORDER BY created_at DESC, id DESC LIMIT 50`, findingID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var rec VerificationRecord
		if err = rows.Scan(&rec.ID, &rec.FindingID, &rec.FromStatus, &rec.ToStatus, &rec.Method,
			&rec.Actor, &rec.Note, &rec.GroundTruth, &rec.CreatedAt); err != nil {
			return nil, err
		}
		st.History = append(st.History, rec)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}

	last, err := d.LatestFindingReplay(ctx, findingID)
	if err != nil {
		return nil, err
	}
	st.LastReplay = last
	st.Replayable = last != nil && last.Replayed
	if st.Replayable {
		// 「至少一次成功」而不是「最近一次成功」：可重放是证据的属性，
		// 一次成功就证明证据可回放；后来目标下线不算证据缺陷。
		var ok bool
		if err = d.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM finding_replays WHERE finding_id=$1 AND replayed)`, findingID).
			Scan(&ok); err != nil {
			return nil, err
		}
		st.Replayable = ok
	}

	st.AllowedNext = []string{}
	if ValidVerificationStatus(st.Status) {
		for _, to := range VerificationStatuses() {
			if CanTransitionVerification(st.Status, to) {
				st.AllowedNext = append(st.AllowedNext, to)
			}
		}
	}
	return st, nil
}

// ReplayRecordInput 是一次重放尝试的落库输入。
type ReplayRecordInput struct {
	FindingID       int64
	BindingID       *int64
	SnapshotID      string
	Replayed        bool
	MatchesOriginal bool
	StatusCode      int
	RespLen         int64
	RespSHA256      string
	ErrText         string
	ElapsedMS       int64
}

// RecordFindingReplay 落一条重放流水。可重放率只从这张表算，不从别处猜。
func (d *DB) RecordFindingReplay(ctx context.Context, in ReplayRecordInput) (*FindingReplay, error) {
	out := &FindingReplay{}
	err := d.QueryRowContext(ctx, `INSERT INTO finding_replays
			(finding_id, binding_id, snapshot_id, replayed, matches_original, status_code, resp_len, resp_sha256, error, elapsed_ms)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		RETURNING id, finding_id, binding_id, snapshot_id, replayed, matches_original,
		          status_code, resp_len, resp_sha256, error, elapsed_ms, created_at`,
		in.FindingID, in.BindingID, in.SnapshotID, in.Replayed, in.MatchesOriginal,
		in.StatusCode, in.RespLen, in.RespSHA256, in.ErrText, in.ElapsedMS).
		Scan(&out.ID, &out.FindingID, &out.BindingID, &out.SnapshotID, &out.Replayed, &out.MatchesOriginal,
			&out.StatusCode, &out.RespLen, &out.RespSHA256, &out.Error, &out.ElapsedMS, &out.CreatedAt)
	if err != nil {
		return nil, err
	}
	return out, nil
}

// LatestFindingReplay 读最近一次重放尝试（没有则 nil）。
func (d *DB) LatestFindingReplay(ctx context.Context, findingID int64) (*FindingReplay, error) {
	out := &FindingReplay{}
	err := d.QueryRowContext(ctx, `SELECT id, finding_id, binding_id, snapshot_id, replayed, matches_original,
			status_code, resp_len, resp_sha256, error, elapsed_ms, created_at
		FROM finding_replays WHERE finding_id=$1 ORDER BY created_at DESC, id DESC LIMIT 1`, findingID).
		Scan(&out.ID, &out.FindingID, &out.BindingID, &out.SnapshotID, &out.Replayed, &out.MatchesOriginal,
			&out.StatusCode, &out.RespLen, &out.RespSHA256, &out.Error, &out.ElapsedMS, &out.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return out, nil
}

// VerificationMetrics 是两个一等指标 + 它们的样本数。
//
// 口径（写进报告头部，改口径必须同步改这里和文档）：
//
//	可重放率 = replayable / verified            样本 = verified
//	误报率   = refuted / (verified + refuted)   样本 = verified + refuted
type VerificationMetrics struct {
	Total          int `json:"total"`
	Verified       int `json:"verified"`
	Refuted        int `json:"refuted"`
	Unreproducible int `json:"unreproducible"`
	Unverified     int `json:"unverified"`

	// Replayable = 当前 verified 且历史上至少一次重放成功（replayed=true）的条数。
	Replayable     int `json:"replayable"`
	ReplayAttempts int `json:"replay_attempts"`
	ReplaySuccess  int `json:"replay_success"`

	ReplaySample        int `json:"replay_sample"`
	FalsePositiveSample int `json:"false_positive_sample"`

	ReplayableRate    float64 `json:"replayable_rate"`
	FalsePositiveRate float64 `json:"false_positive_rate"`

	// GroundTruthOnly 恒为 true：这个结构体里的数字只由环境事实算出。
	GroundTruthOnly bool `json:"ground_truth_only"`
	// Rule 是公式原文，随指标一起返回，避免调用方自己脑补口径。
	Rule string `json:"rule"`

	GeneratedAt time.Time `json:"generated_at"`
}

// verificationMetricsRule 是随 API 返回的公式说明（报告头部也用这段）。
const verificationMetricsRule = "可重放率 = 可重放数 / verified 总数；误报率 = refuted / (verified + refuted)；" +
	"分母为 0 时比率记 0，必须同时看样本数。判定只接受环境 ground-truth，不接受 LLM/Agent 自报。"

// ComputeVerificationMetrics 是纯函数：给定计数算两个指标。
// 单独拆出来是为了让算法本身可单测（不依赖数据库）。
func ComputeVerificationMetrics(c VerificationMetrics) VerificationMetrics {
	if c.Verified > 0 {
		c.ReplayableRate = roundRate(float64(c.Replayable) / float64(c.Verified))
	}
	c.ReplaySample = c.Verified
	c.FalsePositiveSample = c.Verified + c.Refuted
	if c.FalsePositiveSample > 0 {
		c.FalsePositiveRate = roundRate(float64(c.Refuted) / float64(c.FalsePositiveSample))
	}
	c.GroundTruthOnly = true
	c.Rule = verificationMetricsRule
	c.GeneratedAt = time.Now()
	return c
}

func roundRate(v float64) float64 {
	return math.Round(v*10000) / 10000
}

// VerificationMetricsFilter 限定统计范围（目前只有按任务）。零值 = 全库。
type VerificationMetricsFilter struct {
	TaskID int64
}

// VerificationMetrics 从库里聚合两个指标。SQL 只做计数，比率由
// ComputeVerificationMetrics 算——口径只有一处实现。
func (d *DB) VerificationMetrics(ctx context.Context, filter VerificationMetricsFilter) (*VerificationMetrics, error) {
	where := ""
	var args []any
	if filter.TaskID > 0 {
		where = " WHERE f.task_id=$1"
		args = append(args, filter.TaskID)
	}
	var m VerificationMetrics
	err := d.QueryRowContext(ctx, `SELECT
			count(*),
			count(*) FILTER (WHERE f.verification_status='verified'),
			count(*) FILTER (WHERE f.verification_status='refuted'),
			count(*) FILTER (WHERE f.verification_status='unreproducible'),
			count(*) FILTER (WHERE f.verification_status='unverified'),
			count(*) FILTER (WHERE f.verification_status='verified' AND EXISTS (
				SELECT 1 FROM finding_replays r WHERE r.finding_id=f.id AND r.replayed))
		FROM findings f`+where, args...).
		Scan(&m.Total, &m.Verified, &m.Refuted, &m.Unreproducible, &m.Unverified, &m.Replayable)
	if err != nil {
		return nil, err
	}

	replayWhere := ""
	if filter.TaskID > 0 {
		replayWhere = ` WHERE r.finding_id IN (SELECT id FROM findings WHERE task_id=$1)`
	}
	if err = d.QueryRowContext(ctx, `SELECT count(*), count(*) FILTER (WHERE r.replayed) FROM finding_replays r`+replayWhere, args...).
		Scan(&m.ReplayAttempts, &m.ReplaySuccess); err != nil {
		return nil, err
	}
	out := ComputeVerificationMetrics(m)
	return &out, nil
}

// ReplayableFindingIDs 返回给定 id 集合里「可重放」的那些（导出头部用）。
func (d *DB) ReplayableFindingIDs(ctx context.Context, ids []int64) (map[int64]bool, error) {
	out := map[int64]bool{}
	if len(ids) == 0 {
		return out, nil
	}
	ph := make([]string, len(ids))
	args := make([]any, len(ids))
	for i, id := range ids {
		ph[i] = fmt.Sprintf("$%d", i+1)
		args[i] = id
	}
	rows, err := d.QueryContext(ctx, `SELECT DISTINCT finding_id FROM finding_replays
		WHERE replayed AND finding_id IN (`+strings.Join(ph, ",")+`)`, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		out[id] = true
	}
	return out, rows.Err()
}

// SplitByVerification 把待导出的发现切成 verified / blocked 两组。
// 导出路径必须调用它：blocked 非空就整体拒绝（硬拦，不静默丢数据）。
func SplitByVerification(findings []*DBFinding) (verified, blocked []*DBFinding) {
	for _, f := range findings {
		if f.VerificationStatus == VerificationVerified {
			verified = append(verified, f)
		} else {
			blocked = append(blocked, f)
		}
	}
	return verified, blocked
}
