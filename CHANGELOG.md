# 更新日志

版本号按 [语义化版本](https://semver.org/lang/zh-CN/)：修 bug 走 patch，加能力走 minor，
数据库结构或 API 契约不兼容才走 major。

## v2.3.0 — 2026-11-23

### 新增 · 私有冻结真实漏洞评测层（总纲 6.1）

回答一个卖产品时必须能回答的问题：**这套 agent 的能力，凭什么可证明？**

答案是三层任务结构，分数不合成一个总分：

| 层 | 测什么 | 计分 |
|---|---|---|
| layer 1 | 合成 in-vitro，组件能力 | **milestone** 阶梯（如 30 → 70 → 100 累计） |
| layer 2 | 真实历史 CVE **冻结版本**，真实能力 | **outcome**（判攻击后果达成） |
| layer 3 | 防御层（WAF / 统一响应陷阱），对抗生存 | 三轴之一：**防御生存率** |

判「后果达成」，不判 flag。原因不是洁癖：flag 是靶里一个静态字符串，位置固定，
读源码/猜路径/抄 writeup 都能拿到，拿到它只能证明猜中了出题人的路径约定。
本层三种判据读的全是靶的**环境事实**，且哨兵运行期现场铸造 —— `inline` 型只存在于
评测方监听器内存里，物理上不可能被读文件拿到，只能让目标真的出站连过来；
`file_read` 读的是靶进程自己写的 `SERVED <绝对路径>`，响应体可以伪造，进程 open 过哪个
inode 伪造不了。layer 2 要求响应泄漏**和**宿主日志两条独立通道同时成立。

### 冻结闸门

`eval/freeze.lock` 记录每个靶的提交/文件哈希，运行时校验，不一致直接拒跑：

```
运行失败: 冻结物已 drift：eval/fixtures/pathtraversal/main.go
（锁内 03065719c06f，磁盘 ede070d7bd39），拒绝运行——靶变了，历史分数不可比
```

不做这一步，靶被改过之后的历史分数会静默失去可比性。

### 独立评测服务

`cmd/evald` 独立进程，默认只监听回环，非回环需 `-allow-remote` + token：
`POST /api/eval/runs`、`GET /api/eval/summary`。`error ≠ miss` —— 靶中途退出记
`undetermined` 单列，不进 outcome 分母，避免把基础设施故障算成能力不足。

### 修复（过程中被测试抓到的两个真 bug）

- `expect_not_contains` 取错了返回值（拿了 missing 而非 matched），导致禁止片段永不生效 ——
  典型的「judge 恒真」，已修。
- 初版 SSRF 阶梯的 m2/m3 打的是同一份 banner（SHA256 相同），阶梯是假的。改成监听器按
  客户端请求行分档回送（`GET /creds` 才给凭据），22 字节 vs 72 字节是三个真实深度。

### 已知边界

容器编排与 `image_digest` 只有 schema 位未实现；当前为单机进程级隔离；
提交动作真实性只做到「评测方重放 + 只认后果」。

## v2.2.0 — 2026-11-20

### 新增 · 验证质量为一级闸门（总纲 6.2）

处置状态与验证状态拆成两根轴，报告导出只看验证状态；LLM 自报不算证据
（method 带 `llm`/`gpt`/`claude`/`model`/`agent`/`self_report`/`reasoning` 一律拒绝）；
证据一变，原判定在同一事务里失效。设计说明见 `docs/finding-verification-gate-zh.md`。

### 新增 · 编排层并入本仓

`orchestrator/` 作为独立 module 并入，只产出 action JSON、从不接触目标。

## v2.0.0 — 2026-11-19

品牌去上游化：界面、文案、构建链本地化；module 改为 `Kur1sulab/lance`；默认中文。
