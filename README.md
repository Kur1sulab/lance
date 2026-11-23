# 朗基努斯 · Lance

自主渗透测试平台 —— LLM 多智能体驱动的攻击面评估系统。

后端 Go 单二进制（前端静态资源已内嵌），数据层 PostgreSQL，界面默认中文。

> **两套 Go 代码，同一个产品（一个产品一个仓库）：**
> - **仓库根** —— 渗透测试平台：控制台 + 多智能体（planner / worker / reporter）+ 资产图 + 流量录制 + 审批拦截。
> - **[`orchestrator/`](orchestrator/)** —— 独立的**编排层**（v1.4.0）：只产出 action JSON、从不接触目标的决策进程，与 litmus2 执行层解耦，可单独 `go build` 与复用。
>
> 两者各带自己的 `go.mod`（嵌套 module），父模块 `go build ./...` 会自动跳过 `orchestrator/`。

---

## 它做什么

把一次授权渗透测试拆成可回放的流水线：**测绘 → 决策 → 执行 → 取证 → 报告**。

- **多智能体分工**：planner 决定下一步打哪儿，worker 并行执行，reporter 汇总证据写报告。三者通过事实图通信，个体崩溃不影响整张图。
- **资产图**：所有发现的 host/port/service/接口进图，后续动作按图选路，不重复打同一个面。
- **流量录制**：本地 MITM 代理（默认 `127.0.0.1:8788`）抓全量请求，证书自动生成，证据可逐条回放。
- **拦截与审批**：高风险动作（写操作、命令执行、外发）默认进审批队列，人工放行才执行。
- **决策与执行分离**：决策层只产出结构化动作，参数走白名单，执行层不经 shell —— 模型再"想"也构造不出第二条命令。

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

# 2. 配置：config.json 填 database / skill_dir / language

# 3. 启动（守护脚本，退出码 75 表示更新重启）
LC_ALL=C ./start-local.sh

# 4. 打开
open http://127.0.0.1:8790/login/
```

端口：`8790` 控制台，`8788` 录制代理。

登录：用户名 `Lance`，初始密码由 `LANCE_ADMIN_PASSWORD` 注入；忘记密码用
`./reset-password.sh -p '新密码' -y` 重置（DB 内 bcrypt，无需重启）。

## 配置

环境变量统一 `LANCE_` 前缀（旧的 `LANCE_` 仍被读取，启动时自动搬到新名）：

| 变量 | 作用 |
|---|---|
| `LANCE_CONFIG` | config.json 路径 |
| `LANCE_PG_DSN` | 数据库连接串（优先于 config.json） |
| `LANCE_LANG` | 界面/输出语言，默认 `zh` |
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

## 许可

AGPL-3.0（见 LICENSE）。本项目为独立维护的分支，界面、品牌、文案、构建链均已本地化。
