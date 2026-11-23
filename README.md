# 朗基努斯 · Lance

**自主渗透测试平台。** LLM 多智能体驱动的攻击面评估系统：把一次授权测试拆成
`测绘 → 决策 → 执行 → 取证 → 报告` 的可回放流水线。

后端 Go 单二进制（前端静态资源内嵌），数据层 PostgreSQL，界面默认中文。

![license](https://img.shields.io/badge/license-AGPL--3.0-blue)
![go](https://img.shields.io/badge/go-1.24%2B-00ADD8)

> **两套 Go 代码，同一个产品（一个产品一个仓库）：**
> - **仓库根** —— 渗透测试平台：控制台 + 多智能体（planner / worker / reporter）+ 资产图 + 流量录制 + 审批拦截。
> - **[`orchestrator/`](orchestrator/)** —— 独立的**编排层**：只产出 action JSON、从不接触目标的决策进程，可单独 `go build` 与复用。
>
> 两者各带自己的 `go.mod`（嵌套 module），父模块 `go build ./...` 会自动跳过 `orchestrator/`。

---

## 它做什么

- **多智能体分工**：planner 决定下一步打哪儿，worker 并行执行，reporter 汇总证据写报告。三者通过事实图通信，个体崩溃不影响整张图。
- **资产图**：所有发现的 host / port / service / 接口进图，后续动作按图选路，不重复打同一个面。
- **流量录制**：本地 MITM 代理（默认 `127.0.0.1:8788`）抓全量请求，证书自动生成，证据可逐条回放。
- **拦截与审批**：高风险动作（写操作、命令执行、外发）默认进审批队列，人工放行才执行。
- **决策与执行分离**：决策层只产出结构化动作，参数走白名单，执行层不经 shell —— 模型再"想"也构造不出第二条命令。

## 验证质量闸门

渗透产品的分歧不在"能不能打"，在"报出来的东西是不是真的"。所以本平台把
**处置状态**与**验证状态**拆成两根轴，报告导出只看后者：

| 轴 | 取值 | 谁能改 |
|---|---|---|
| 处置状态 `status` | pending / in_progress / confirmed / resolved / fixed / false_positive / ignored / duplicate / risk_accepted | 人（UI 下拉） |
| 验证状态 `verification_status` | unverified / verified / refuted / unreproducible | 只接受带环境事实的判定接口 |

- **LLM 自报不算证据。** 判定方法带 `llm` / `gpt` / `claude` / `model` / `agent` / `self_report` / `reasoning`
  字样的一律拒绝（`db.IsGroundTruthMethod`）；每次判定必须给出 `actor` 与 `ground_truth`
  （响应状态行、关键响应片段、独立复现的命令输出）。
- **证据一变，旧判定立刻失效。** 绑定新增 / 修改 / 删除推高 `evidence_version`，
  原判定在同一事务里退回 `unverified` 并记一条 `evidence_changed` 流水 —— 防止一条过期
  `verified` 带着新证据进报告。
- 由此得到两个可以对外说的数：**可重放率**与**误报率**。设计说明见
  [`docs/finding-verification-gate-zh.md`](docs/finding-verification-gate-zh.md)。

## 架构

```
┌──────────────────────────────────────────────────────┐
│  控制台（Next.js 静态站，编进二进制）                │
└───────────────────────────┬──────────────────────────┘
                            │ HTTP / SSE
┌───────────────────────────▼──────────────────────────┐
│  Go 后端                                             │
│  ├ planner / worker / reporter  （多智能体）         │
│  ├ 事实图 + 资产图                                   │
│  ├ 拦截规则 / 审批队列                               │
│  ├ 验证闸门（verification_status + 可重放率）        │
│  └ 报告生成 + 证据链                                 │
└──────────┬───────────────────────────┬───────────────┘
           │                           │
    ┌──────▼──────┐            ┌───────▼────────┐
    │ PostgreSQL  │            │ MITM 录制代理  │
    └─────────────┘            └────────────────┘
```

## 快速开始

```bash
# 1. 数据库（PostgreSQL 17，本地 5433）
pg_ctl -D ~/lance-pg/data -l ~/lance-pg/pg.log start
#    注意：macOS 上 locale 不合法会让 postmaster 多线程化而启动失败
#    LC_ALL=C pg_ctl ... start

# 2. 配置：cp config.example.json config.json，填 database / skill_dir / language

# 3. 启动（守护脚本，退出码 75 表示更新重启）
LC_ALL=C ./start-local.sh

# 4. 打开
open http://127.0.0.1:8790/login/
```

端口：`8790` 控制台，`8788` 录制代理。

登录：用户名 `Lance`，初始密码由 `LANCE_ADMIN_PASSWORD` 注入；忘记密码用
`./reset-password.sh -p '新密码' -y` 重置（DB 内 bcrypt，无需重启）。

## 配置

环境变量统一 `LANCE_` 前缀：

| 变量 | 作用 |
|---|---|
| `LANCE_CONFIG` | config.json 路径 |
| `LANCE_PG_DSN` | 数据库连接串（优先于 config.json） |
| `LANCE_LANG` | 界面 / 输出语言，默认 `zh` |
| `LANCE_ADMIN_PASSWORD` | 首启动管理员密码 |
| `LANCE_LLM_PROVIDER` / `_MODEL` / `_BASE_URL` | LLM 端点（DB 里的设置优先） |
| `LANCE_SKILL_DIR` | 技能目录 |

LLM 走 OpenAI 兼容接口；数据库里的 LLM 配置优先于环境变量，改完在「系统 → LLM」页点测试确认。

## 构建

```bash
# 前端（产物要拷进 server/webui/dist，然后带 embedui 编译）
cd web && NEXT_EXPORT=1 npm run build:static && cp -R out/. ../server/webui/dist/

# 后端
CGO_ENABLED=0 go build -tags embedui -trimpath \
  -ldflags "-s -w -X main.version=2.0.0" -o lance ./cmd/lance
```

漏 `-tags embedui` 会得到「接口全 200、页面全 404」的假故障。

## 边界

这个产品**不承诺**以下事情，装之前先看清楚：

- **不做免杀、不做驻留、不做横向提权** —— 它是评估平台，不是 C2。
- **自更新默认关闭。** 页面一键更新会对准发布源下载 release 并覆盖本地程序，
  对二次开发版本等于把改动一次性抹掉；本地分支因此只走 `git pull` + 自行构建。
- **不替你判断授权范围。** 审批队列只拦"高风险动作"，不拦"你打错了目标"。
- 平台本身不内含任何漏洞利用武器库；用哪把刀、打哪里，由操作者与其授权书决定。

## 许可

AGPL-3.0（见 [LICENSE](LICENSE)）。本项目为独立维护的分支，界面、品牌、文案、构建链均已本地化；
分发或部署为网络服务时，请同样以 AGPL-3.0 提供完整源码。

> ⚠️ 仅限**获得书面授权**的目标使用。未授权测试在多数司法辖区构成犯罪。
