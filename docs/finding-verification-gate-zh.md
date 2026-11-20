# 验证质量闸门（可重放率 · 误报率 · ground-truth 判定）

对应调研总纲 6.2：只有**过验证**的发现才进报告；在证据可追溯的基础上加**可重放率**和**误报率**两个指标；成功判定用环境 ground-truth，**不用 LLM 自报**。理由不是洁癖——XBOW 的误报经济学说明 0–10% 误报率才有赏金正循环，这两个数是商业化前提。

## 两轴分离：处置状态 vs 验证状态

| 轴 | 列 | 取值 | 谁来改 |
| --- | --- | --- | --- |
| 处置状态 | `findings.status` | pending / in_progress / confirmed / resolved / fixed / false_positive / ignored / duplicate / risk_accepted | 人（三态流转、UI 下拉） |
| 验证状态 | `findings.verification_status` | unverified / verified / refuted / unreproducible | 只接受 ground-truth 判定接口 |

两轴故意不复用：`status` 可以被随手改成 `confirmed`（甚至被 Agent 改），`verification_status` 只能由带环境事实的判定入口改。报告导出**只看验证状态**。

## 状态机

```
unverified ──┬──> verified
             ├──> refuted
             └──> unreproducible

verified   ──┬──> refuted            （复核推翻）
             ├──> unreproducible
             └──> verified           （重申判定，刷新 verified_at）

refuted    ──┬──> verified           （纠错，需带新证据）
             ├──> unreproducible
             └──> refuted            （重申误报）

unreproducible ──┬──> verified
                 └──> refuted

系统通道：verified/refuted/unreproducible ──> unverified   method='evidence_changed'
```

- **不允许**把发现直接标回 `unverified`：撤销必须走 `refuted`（留证据、进误报率分母）。
  唯一降级通道是证据变更时的系统失效。
- 证据一变（绑定新增/修改/删除 → `evidence_version` 递增），原判定在同一事务里失效，
  退回 `unverified` 并追加一条 `evidence_changed` 流水。否则一条过期 verified 会带着
  新证据进报告，正是闸门要防的事。

## 判定方法白名单（环境事实）

| method | 含义 |
| --- | --- |
| `replay` | 平台按证据库里的报文重建请求并真实发出，用目标响应作为依据 |
| `manual_replay` | 人工用独立工具（curl / 浏览器 / 自己的脚本）复现 |
| `target_response` | 目标真实响应本身即判定依据（含独立第三方复现结果） |
| `evidence_changed` | 仅系统内部：证据被替换，原判定失效 |

**硬规则：任何带 `llm` / `gpt` / `claude` / `model` / `agent` / `self_report` / `reasoning`
字样的 method 一律拒绝（`db.IsGroundTruthMethod`）。** 同时必须提供：

- `actor`：判定主体（`operator:alice`、`replay-engine`、`curl`……），不接受空；
- `ground_truth`：具体环境事实（响应状态行、关键响应片段、独立复现的命令输出）。

LLM 说「我验证过了」不是证据，是待验证的断言。这条写在代码注释、API 响应
（`rejects_llm_self_report: true`）、报告头部元数据三处，改口径必须三处一起改。

## 两个指标

```
可重放率 = 可重放数 / verified 总数          样本 = verified
误报率   = refuted / (verified + refuted)    样本 = verified + refuted
```

- **可重放**：该发现当前为 `verified`，且 `finding_replays` 里至少一次 `replayed=true`。
  `replayed=true` 的含义是「凭证据把请求重建出来并拿到了目标响应」——目标回 200/403/500
  都算（那是事实，不是好坏）。`matches_original` 是更强的保真度信号，单独统计，不进可重放率。
- **不进分母**：`unverified`（未判定）和 `unreproducible`（证据不足）不算误报。把
  「未判定」混进误报率会让指标变成噪声。
- **分母为 0**：比率记 0，同时返回样本数。看指标必须连样本数一起看。

## API

### `GET /api/metrics/verification[?task_id=]`

```json
{
  "metrics": {
    "total": 42, "verified": 12, "refuted": 2, "unreproducible": 3, "unverified": 25,
    "replayable": 9, "replay_attempts": 14, "replay_success": 11,
    "replay_sample": 12, "false_positive_sample": 14,
    "replayable_rate": 0.75, "false_positive_rate": 0.1429,
    "ground_truth_only": true,
    "rule": "可重放率 = 可重放数 / verified 总数；误报率 = refuted / (verified + refuted)；分母为 0 时比率记 0，必须同时看样本数。判定只接受环境 ground-truth，不接受 LLM/Agent 自报。",
    "generated_at": "2026-11-21T02:45:00Z"
  },
  "statuses": ["unverified", "verified", "refuted", "unreproducible"],
  "transitions": {"unverified": ["verified", "refuted", "unreproducible"], "...": []},
  "methods": ["replay", "manual_replay", "target_response"],
  "ground_truth_only": true,
  "rejects_llm_self_report": true
}
```

### `GET /api/exploration/findings/{id}/verification`

返回 `status / method / actor / note / ground_truth / verified_at / evidence_count /
replayable / last_replay / history[] / allowed_next[]`。`allowed_next` 是状态机算出来的，
调用方不用猜下一步能标什么。

### `POST /api/exploration/findings/{id}/verification`

```json
{
  "status": "verified",
  "method": "manual_replay",
  "actor": "operator:alice",
  "note": "按原证据复现",
  "ground_truth": "curl -s 'http://target/api/users?id=2' → HTTP/1.1 200 OK，返回他人用户记录"
}
```

- `200`：返回最新 `VerificationState`。
- `400`：非法流转 / method 不在白名单（含任何 LLM 自报）/ 缺 actor 或缺 ground_truth。
- `404`：发现不存在。

### `POST /api/exploration/findings/{id}/replay`

可选 body：`binding_id` / `payload_override` / `url_override`（仅同 host 改路径或查询串）/
`header_override`。真实发一次请求，落一条 `finding_replays`，返回
`replay`（落库行）、`request`、`response`、`original`、`verification`。

**它只记录事实，不改验证状态**：判定仍要人拿这次真实响应去调 POST /verification。
`replayed=true` 不等于漏洞仍存在，也不等于已修复。

## 报告导出闸门（后端硬拦）

`GET /api/exploration/findings/export` 在**所有格式**（md-single / md-zip / csv / json）之前
统一切分：只有 `verification_status='verified'` 的发现进导出。存在未验证条目时整体返回
**409**，不静默丢弃（静默丢弃会让报告看起来比实际更可信）：

```json
{
  "error": "报告导出被验证闸门拦截",
  "reason": "只有 verification_status='verified' 的发现允许进报告；判定必须来自环境 ground-truth（重放实测 / 独立复现 / 目标真实响应），不接受 LLM 或 Agent 自报",
  "blocked": [{"id": "123", "name": "SQL 注入", "vulnclass": "sqli", "verification_status": "unverified"}],
  "verified": 4,
  "hint": "对每条发现调 POST /api/exploration/findings/{id}/verification ..."
}
```

放行时，报告头部（md / csv / zip 内的 `_VERIFICATION.md`）写入质量元数据：
可重放率、误报率、库内样本分布、本报告批次的条数与可重放数、判定来源。
单条发现带 `验证状态：verified（环境 ground-truth 判定）`；万一有未验证条目绕过闸门，
报告会显式标注「未过验证闸门，不应出现在本报告中」。

## 指标口径只有一处实现

`db.ComputeVerificationMetrics` 是纯函数，SQL 只做计数；报告头部、CSV 注释行、
HTTP API 全部调它。改口径只改这一处（以及 `verificationMetricsRule` 文案）。
