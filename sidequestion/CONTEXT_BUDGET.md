# 附带提问长对话与上下文预算

2026-09-11 修改。旧实现直接把请求 JSON 的字符数当作 token 数，并原样继承主任务的 32K 输出预留量，因此在 HTML·JS·工具结果较多时会提前拒绝正常的旁问。

## 开源实现对照

- [Grok CLI 侧问上下文](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/agent.ts#L739)：从最近一次的用户·助手文本中摘取部分片段，字符预算约 2000 字，单条最多截取 400 字。此路径不保留连续的侧问问答记录。
- [Grok CLI 独立请求](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/utils/side-question.ts)：单独设置取消信号。若模型支持，输出上限为 2048 token。不提供工具。
- [Grok CLI 主会话压缩](https://github.com/superagent-ai/grok-cli/blob/fb97af83f06dca873281d60168430f06c8de6324/src/agent/compaction.ts)：估算 token，保留最近内容，将新内容并入既有摘要，并处理跨轮次边界的截断。
- [OpenCode 会话压缩](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/compaction.ts)：估算整个请求，预留输出与缓冲区，为最近内容追加滚动摘要，并发送不带工具的摘要请求。该实现默认使用 8000 的最近预算、4096 token 的摘要输出上限。
- [OpenCode 溢出恢复](https://github.com/anomalyco/opencode/blob/b3f1a96c6dd7adeb28b36dd11add1998fc84d67b/packages/core/src/session/runner/llm.ts)：仅在助手输出尚未开始时才尝试溢出恢复，恢复后的调用不会再次进入同一溢出恢复路径。

LANCE 参考了独立输出预算、最近内容与滚动摘要、受限恢复的方式。保留 norma v0.3.6 的结构化消息与工具配对，但没有照搬 Grok 的文本摘录方式，也不把 OpenCode 的主会话压缩事件记录到 LANCE 主 transcript 中。

## 请求预算与执行

- 消息沿用 norma 的方式，按内容块估算 UTF-8 字节并加上 4/3 余量。再额外计入系统提示词、工具 schema、消息封装开销。该估算值不是模型的精确 token 数。
- 侧问输出默认最多 8192 token，且不超过主配置中已设定的输出上限。可通过服务环境变量 `LANCE_BTW_MAX_OUTPUT_TOKENS` 设置 256–32768 范围内的上限，不会改变产品默认模型或主任务参数。
- 输入预算为上下文窗口减去输出上限与安全余量，窗口大小未知时使用平台默认值 200K。安全余量为窗口的 5%，最小 128 token，最大 8192 token。
- 成功的问答按递增序号一次最多加载 20 组。原文最多保留 20 组，其 token 预算不超过输入预算的 1/4，且不超过 16K。
- 超出预算的问答通过滚动摘要体现。摘要同时包含来源记录与上下文时点。过去的助手回答不等同于新的工具证据，因此发生冲突时优先采用最新的主快照。
- 如果主上下文仍然过长，只摘要副本中的旧消息，最近内容最多保留 8K token。切分点不会拆开工具调用及其结果。如果单个分组过大，则将整个分组放进摘要。
- 摘要输入按实际剩余窗口大小以 UTF-8 安全切分，输出上限为 2048 token。空摘要、被截断的响应、工具调用、摘要预算超限都不会写入缓存。单次旁问中摘要调用最多 12 次，并共同适用相同的 120 秒超时。达到上限时不会无限重复，而是明确以失败结束。
- 如果模型首次返回上下文超限，且尚未输出文本或工具调用，则进一步缩减后最多重试一次。如果估算大小没有减少，就立即停止恢复。其他模型错误或仅部分流式输出的内容不会触发此恢复。
- 包括摘要、失败尝试、取消时刻的用量在内，所有已统计的用量都累加到同一个旁问请求上。若 Provider 不返回用量则只记为 0，不把估算值伪装成实际用量。

## 持久化与界面

`side_question_sessions.memory` 保存旧问答摘要、已反映的序号（ordinal），以及按快照身份缓存的主上下文摘要。`side_question_requests.context_info` 保存准备阶段、实际重新加载的批次数、摘要使用明细、预算估计值。

仅当原始请求仍在执行且清理版本一致时才保存摘要。清空操作会同时清除缓存，延迟到达的写入不会恢复已清理的数据。新快照不会复用前一快照的摘要。摘要字段与 v3 任务归档一同保存；恢复缺少该字段的旧版 v3 时用空对象填充，v1·v2 仍保持兼容。

POST 先接受请求并返回，准备与压缩在后台执行，不持有准入锁（admission lock）或数据库事务。SSE 与记录页面会显示准备、问答整理、副本压缩、响应各阶段。压缩失败会以该侧问题的失败终止状态保存。前端保留错误并恢复本次失败问题的草稿，浮动提示不会遮挡输入框。记录轮询也不再清除提交错误。

## 验证记录

- 19·20·21·50 批次回放、保留超过 20 批次的旧结论、摘要缓存重启后复用：自动化通过。
- 超长中文回答与代码上下文、请求预算拆分、工具配对、快照不可变、新快照缓存失效：自动化通过。
- 摘要失败·取消·截断·超长·工具返回、清空竞争、调用上限、仅一次溢出恢复且部分流不重试：自动化通过。
- 独立 PostgreSQL 上的分页、重启、v1·v2·v3 归档、摘要缓存与预算元数据的归档还原、缺少新字段的旧 v3、20 个父会话共享四个并发执行位：通过。
- Go 候选服务构建、前端 TypeScript 检查、所修改组件的 Biome 检查，以及在独立目录中的 Next.js 生产构建：通过。
- 使用内置浏览器和独立 UI fixture，在 1280×720 与 390×844 分辨率下验证了问答整理阶段、摘要范围提示、失败后草稿恢复、无悬浮错误提示、无横向溢出、无控制台错误。临时 fixture 已移除。
- 以只读方式重放本地已有的 Worker 快照时，新的预算检查通过了。例如 Worker #3 的 293085 字符快照不再因本地字符数计算而被错误拒绝。此项没有外部模型调用。
- 本次打算把非公开 Worker 快照发送给 Grok 的真实对话测试被自动审批审核拒绝而未执行，因此不计为通过项。
- 2026-09-11 00:37 应客户请求重启了本地后端，并沿用原有的数据库、数据目录和登录配置。可执行文件与候选二进制的 SHA-256 一致，后端和前端代理的 `/api/health` 均返回正常。

验证命令（仅使用独立测试 DB）：

```sh
go test -race ./sidequestion ./db ./server -run 'TestSide|TestCheckpoint|TestSnapshot|TestBuildRequest|TestService|TestMainSide|TestTaskArchive' -count=1
go build ./cmd/lance
npx tsc --noEmit
npm run build -- --webpack
```

前端生产构建改用独立副本，避免覆盖当前预览的 `.next`。候选服务位于 `/private/tmp/lance-btw-budget-candidate`，已复制到 `/private/tmp/lance-btw-preview/lance` 并运行。原二进制已备份为同目录下的 `lance.before-context-budget`。
