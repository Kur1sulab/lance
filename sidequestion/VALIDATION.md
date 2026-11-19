# `/btw` 验证记录

日期：2026-09-10。分支：`codex/btw-side-question`。基准提交（baseline）：`8dae851b9b622f2ff2631f332fde9719d0b16fba`。

使用了独立的 PostgreSQL 测试 DB 与数据目录。真实模型凭据仅注入到独立测试环境，未写入代码或本记录，也未更改产品默认模型。Go 1.26.3, norma v0.3.6, Next.js 16.2.9.

实际模型对话、返回对象、工程断言、Qwen 原始审查文本已保存到 [validation-2026-09-10.json](validation-2026-09-10.json)，其中不包含 API 凭据。

## 工程检查

以下项目均已通过，括号内为佐证测试。

- 结构化消息与工具参数的深拷贝：通过（依据 `TestCheckpointDeepCopyAndBoundaries`）。
- 摘要·压缩请求不覆盖、发布完成的响应与结束状态、排除生成中途的半截响应：通过（依据 `TestCheckpointDeepCopyAndBoundaries`、`TestSnapshotExcludesPartialStreamAndSelectsPoolMember`）。
- 真实模型池成员身份：通过（依据 `TestSnapshotExcludesPartialStreamAndSelectsPoolMember`）。
- 工具配对、20 组重放、预算削减与超限错误：通过（依据 `TestBuildRequestCompactionToolPairingAndBudget`）。
- 主会话与侧问并行执行、双向取消隔离：通过（依据：阻塞式 Provider，`TestMainSideConcurrencyAndIndependentCancellation`）。
- 无工具执行、流式与非流式、失败时点的既有用量：通过（依据 `TestServiceNoToolsAndUsageOnFailure`）。
- 真实 norma ChatAgent 与本地 Read 工具、主 transcript·活动隔离：通过（依据 `TestSideActualChatCheckpointToolResultAndTranscriptIsolation` 的流式与非流式子用例）。
- 持久化、分页、幂等性、重启后保留部分响应：通过（依据 `TestSideHistoryIdempotencyPagingAndRecovery`）。
- 清空与延迟写入的竞争、父资源删除、版本比较：通过（依据 `TestSideClearLateWritersAndDeletedParent`）。
- MainAgent·Worker 归档与还原（v1·v2·v3）：通过（依据 `TestSideTaskArchiveVersions`）。
- 三种父接口、认证、资源归属、Worker 逻辑删除：通过（依据 `TestSideHTTPGlobalLimitTaskWorkerAndDeletion`、`TestSideCheckpointPersistsBeforeAdmissionAndRestart`）。
- 繁忙主会话中仍可侧问、独立 SSE 重连与断开、取消、清空：通过（依据 `TestSideHTTPBusyIsolationClearAndReconnect`）。
- 每个父会话 1 个、全局 4 个并发执行：通过（依据：两个 `TestSideHTTP…` 用例）。
- 提交前保存快照、重启后继续追问、旧会话无法伪造快照：通过（依据 `TestSideCheckpointPersistsBeforeAdmissionAndRestart`）。
- 缓存中的配置被删除或模型变更时拒绝继续：通过（依据 `TestSideRejectsDeletedOrChangedCachedProfile`）。
- 归档前取消并等待最终响应与用量保存：通过（依据 `TestSideTaskDrainPersistsBeforeArchive`）。
- 即使流式消费者提前取消，用量也只记录一次并归属侧问：通过（依据 `TestSideUsageRecordedOnceOnConsumerCancellation`）。
- 因重启自动恢复的 Worker·deadline 执行上下文持续发布新快照：通过（依据 `TestSideRestoredWorkerRuntimePublishesNewCheckpoint`）。
- 相关包的 race 检查：通过（依据：以下命令）。
- TypeScript 与生产构建：通过（依据 `npx tsc --noEmit`、`npm run build`）。
- 新增前端模块的 Biome 检查：通过（依据 `biome check`，新模块 3 个）。

在可丢弃的数据库上设置 `LANCE_PG_DSN` 即可复现自动化检查(请勿指向生产 DB):

```sh
go test -race ./agent ./db ./server ./sidequestion ./llmrec ./llmpool \
  -run 'Test(Side|Checkpoint|Snapshot|BuildRequest|Service|MainSide|CaptureRun|TaskArchive|CompleteForwards|StopIntent|CancelIntent)' -count=1
cd web
npx tsc --noEmit
npx biome check src/lib/side-questions.ts src/hooks/use-side-questions.ts src/components/side-question-workspace.tsx
npm run build
```

整体 Go 回归测试并非全部通过(green)。`server` 包中已有的两个测试在临时目录清理阶段失败, 两者都报告 `TempDir RemoveAll … directory not empty`:

- `TestInheritedActivityDetailAndRelationDeletion`
- `TestTaskMetadataPatchReturnsRenameAndPin`

从上述未修改的基准提交下载源码，并在同一隔离环境中重新运行 `server` 包，这两个清理失败仍会同样复现。基准提交运行中还单独出现了 `TestCoreTaskLifecyclePG` 的目标节点数量断言失败，但最终修复版的 `server` 回归中没有该断言失败。其他包均通过，本次侧问相关用例和 race 检查也通过。没有将基准提交的问题标记为本次审查通过，也没有为了掩盖隐藏问题而修改现有断言。

Next.js 构建会输出关于多个 lockfile 与 workspace 根目录推断的既有警告，但构建会完成，所有页面均成功生成。

## 浏览器检查

使用 Codex 内置浏览器连接了独立的本地 Go 服务和 Next.js 开发服务器。在桌面和 390 × 844 窄屏下执行了以下手动·自动操作，并确认了截图和浏览器日志:

- 普通聊天执行期间输入 `/btw` 时，正文内容与旁问会同时显示，桌面端侧边栏也正常。
- 随后又提出了追加问题。停止旁问后已生成的部分仍然保留，正文流程继续。
- 关闭面板后请求仍继续，再次打开会恢复已完成的响应。刷新页面后仅输入不带内容的 `/btw` 即可恢复记录。
- 窄屏 Drawer 中输入、按钮、记录、关闭操作均正常，没有横向溢出。
- 清空会弹出确认弹窗，清空后记录消失，但主 transcript 与快照保持不变。
- 向任务的 MainAgent 和两个 Worker 分别提问并切换时，Agent 标签页与记录没有相互混淆。
- 在通过阻塞式本地模型 fixture 让 Worker 持续运行的状态下，向 Worker 的主输入框提交了 `/btw`。停止侧问后，Worker 仍显示实时运行状态及其暂停按钮，侧问保存了部分响应。
- 浏览器错误与警告日志为空。

可控 fixture 用于精确验证并发执行时机, 不依赖真实模型的输出速度。调试期间的两次 Worker 执行检查未能形成有效的并发区间(任务已结束或响应提前结束), 修改 fixture 后重新执行才通过。这次初始操作不计为有效通过。

## 真实模型对话

首先探测到 `grok-4.6`。OpenAI 兼容接口为 `http://127.0.0.1:12580/tingly/openai`。探测结果返回 HTTP 200，并返回模型名称 `grok-4.6` 和 `READY`，耗时 2.82 秒。由于第一优先项可用，未启用 Tingly 的 `glm` 或 Zhipu 的 `glm-5.3` 备用链，本次也未验证这两个备用服务。

- 主会话运行期间询问 资产·目标·标记：`redhaze.top`，返回首屏读取、目标摘要和 `BTW-REAL-0910`，侧问已完成（16.97 秒）。
- 主会话完成首屏读取后询问工具依据：WebFetch 200、curl 的 301 → 302 → 200 重定向、准确引用页面标题（7.24 秒）。
- 侧问要求用 Bash 创建测试文件：拒绝执行，目标文件未生成（7.74 秒）。
- 完成后的旁问不改变主上下文：主 transcript 的 SHA-256 与主活动记录完全一致，旁问的工具执行次数为 0。
- 实际停止并重启 Go 服务后继续提问：保留了此前 3 条侧问记录，直接从持久化快照回答了资产、标记、标题，且未重新运行主代理。
- 新会话使用 Grok 非流式设置：准确回答了资产和 `ATOMIC-0910`，并返回、保存用量（input 11734, output 138, cache_read 11520）。

资产案例的主会话使用 WebFetch 和 Bash/curl 读取了公开的首个页面，落地页为 `https://id.redhaze.top/home`，页面返回的标题是“红幕科技 RedHaze Group · 全球综合集团门户”（因是模型返回的原文而原样引用）。Bash 只是将响应临时保存到本地测试文件，并未执行远程写入。此事已与“旁问未执行工具”这一点分开确认。

主 transcript 校验值：`e7e61f135a4a120954b539f357e8c4205d7d5cd7460dcaf3dc0fd066463e1d00`。

**用量限制：** Tingly 的 Grok 流式响应未返回 usage。单独直接发送 `stream_options.include_usage=true` 验证时，HTTP 200，数据帧 12 个，usage 帧 0 个。因此流式测试中的 0 表示端点不提供用量，不应解读为没有产生计费。非流式用量以及 fixture 中失败、取消的用量均已正确保存。

## Qwen 审查

审查模型为 `qwen-flash`，OpenAI 兼容接口为 `https://dashscope.aliyuncs.com/compatible-mode/v1`，HTTP 200。提供了前三个实际侧问对话、主会话的工具依据、工程断言，并返回了 `verdict: accept` 和 `concerns: []`。判断响应与资产、标记、页面读取证据一致，且侧问工具拒绝符合约束。审查用量：prompt 6625, completion 312, total 6937。

本次 Qwen 审查范围不包含后来添加的服务重启和非流式测试。Qwen 所谓“无写入”的概括过于宽泛。主会话的 curl 确实创建了本地响应临时文件, 这一点前面已明确记录。并发、工具执行 0 次、transcript 隔离按工程断言判断, 模型审查仅辅助评估响应质量。
