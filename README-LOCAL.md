# LANCE 本地部署（macOS arm64，2026-11-12 源码自建）

LANCE = AI 自主渗透测试系统（多智能体 planner/worker/reporter + 资产图 + 流量录制 + 审批拦截）。
云端那份是 linux/amd64 二进制；这份是**本机从源码编译的 darwin/arm64 版**。

## 一键启动

```bash
cd ~/tools/platforms/lance-local
./start-local.sh          # 前台；关掉终端即停
```

打开 http://127.0.0.1:8787/ ，用户名 `LANCE`，密码在 `~/.lance-local-admin.pw`（600 权限）。

## 组成（全部本机，无外网依赖）

| 组件 | 位置 | 说明 |
|---|---|---|
| 后端 | `./lance` 38MB | darwin/arm64, go1.26.3, `-tags embedui`（前端已内嵌） |
| 前端 | `server/webui/dist` 11MB | `NEXT_EXPORT=1 next build` 的静态导出，编进二进制 |
| 数据库 | PostgreSQL 17 @ `127.0.0.1:5433` | 数据目录 `~/lance-pg/data`，库/用户均 `lance`，51 张表由程序自动建 |
| 流量录制代理 | `127.0.0.1:8788` | 抓包用，证书在 data 目录 |
| 技能 | `./skills/` | api-recon / playwright-cli / scopesentry |
| LLM | magpie 网关 `127.0.0.1:3425/v1` | provider=openai，model=`auto/deepseek-v4.1-flash` |

## 环境变量（start-local.sh 已封装）

```bash
LANCE_CONFIG=./config.json          # 只放 database / skill_dir / language
LANCE_LLM_PROVIDER=openai
LANCE_LLM_BASE_URL=http://127.0.0.1:3425/v1
LANCE_LLM_MODEL=auto/deepseek-v4.1-flash
OPENAI_API_KEY=magpie               # magpie 网关的 key
LANCE_ADMIN_PASSWORD=$(cat ~/.lance-local-admin.pw)
LANCE_LANG=zh
```

## 端口

| 端口 | 服务 | 开放范围 |
|---|---|---|
| 8787 | Web 控制台 + JSON API | 仅 127.0.0.1（`-addr 0.0.0.0:8787` 才对外） |
| 8788 | 流量录制代理 | 仅 127.0.0.1 |
| 5433 | PostgreSQL | 仅 127.0.0.1 |

## 踩坑记录（复现用）

1. **go.mod 要 go 1.26.3，本机是 1.24** → `GOTOOLCHAIN=auto` 自动拉工具链；若报
   `checksum database disabled by GOSUMDB=off`，直接用它自己的 go 跑：
   ```bash
   TC=~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.3.darwin-arm64/bin/go
   GOROOT=~/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.26.3.darwin-arm64 \
   GOTOOLCHAIN=local CGO_ENABLED=0 $TC build -tags embedui -o lance ./cmd/lance
   ```
2. **必须带 `-tags embedui`** —— 否则二进制不带前端，打开只有一行韩文提示
   「此二进制文件未内置前端」。
3. **前端要先构建再编 Go**：`cd web && npm ci && NEXT_EXPORT=1 npm run build:static`
   → `rsync -a --delete web/out/ server/webui/dist/` → 再 go build。
4. **PG 17 on macOS 启动报 `postmaster became multithreaded during startup`**
   → 必须 `export LC_ALL=C LANG=C` 再 `pg_ctl start`。
5. PG 用 5433 而非 5432，避开任何既有实例。
6. **`npm i -g playwright-cli` 装到的是占位包**（0.261.0，package.json 里没有 `bin`，
   `npx playwright-cli` 直接报 `could not determine executable to run`）。真包是
   **`@playwright/cli`**（0.1.24），装后 bin 落在 npm prefix 的 `bin/`；
   `/usr/local/bin` 无写权限 → 软链到 `~/.local/bin/playwright-cli`。
   浏览器另有一步：`npx playwright install chromium`（chromium + headless shell 共 584M）。
7. brew 装 PG17 会**顺手建一个默认集群** `/opt/homebrew/var/postgresql@17`（5432），
   本方案不用它；若哪天 `brew services start postgresql@17` 会多起一个实例，注意别混。

## 跑测试（**必须用隔离库**，否则污染真库）

`db` / `server` 包的集成测试**直接使用 `LANCE_PG_DSN` 指向的库**，没有独立测试库概念。
照着默认 DSN 跑一次 `go test ./...`，实际是在往正式库里写测试任务。

正确姿势：

```bash
export PATH=/opt/homebrew/opt/postgresql@17/bin:$PATH
BASE_DSN=$(cat /tmp/.lance_dsn)          # 600 权限，存正式库 DSN
psql "$BASE_DSN" -c "DROP DATABASE IF EXISTS lance_test" >/dev/null
psql "$BASE_DSN" -c "CREATE DATABASE lance_test" >/dev/null
export LANCE_PG_DSN="$(sed 's#/lance?#/lance_test?#' /tmp/.lance_dsn)"
go test ./... -count=1
```

**两个会让人误判的坑：**

1. **DSN 缺失时测试是静默 skip，不是通过。** 没有 `LANCE_PG_DSN` 时所有 DB 测试
   走 `t.Skip("LANCE_PG_DSN is required")`，输出照样一堆 `ok`。
   判据看耗时：**`db` < 5s 或 `server` < 5s 就是没跑**（真跑约 9s / 12s）。
2. **测试之间会互相污染，`server` 包曾因此必红。** 所有测试共享同一个 PG 库，
   而包里原本有 17 处 `t.Parallel()` —— 并发写同一批 `settings` / `tasks` 表，
   单独跑全绿、整体跑必挂，且失败集合每次漂移。
   已删除这些 `t.Parallel()`；顺带 `server` 包从 24.3s 降到 11.6s（省下的并行收益
   抵不过抢 PG 锁的等待）。

## 依赖升级（2026-11-18）

| | 升级前 (tag `pre-dep-upgrade-20261118-1609`) | 升级后 |
|---|---|---|
| 前端 advisory 命中包 | **124** | **2** |
| 其中 `next` critical | 4 条 | 0 |

- 前端：`npm update`（只升 semver `wanted` 范围内，**`package.json` 一字节未动**）
  → `added 22 / removed 108 / changed 282, audited 738, 9m`；剩余 outdated 8。
- `npm audit` 官方旧端点已退役（返回 `This endpoint is being retired` + HTTP 400），
  npmmirror 也不给明细。取明细要直接 POST 未退役的 bulk 端点：
  ```bash
  curl -s -x http://127.0.0.1:7897 -X POST \
    https://registry.npmjs.org/-/npm/v1/security/advisories/bulk \
    -H 'Content-Type: application/json' -d @/tmp/npm-bulk-body.json
  ```
- 后端：`go.mod` 内 patch/minor 升级，未动主版本。
- 构建验证：`NEXT_EXPORT=1 npm run build:static` → exit 0，`out/` 11M。

**已知残留（2 条，决定不动）：**

| 包 | advisory | 为什么不动 |
|---|---|---|
| `picomatch` | `<=4.0.7` high（ReDoS） | npm 上**最新就是 4.0.7**，上游无补丁；父依赖全是构建/lint/文件监听 |
| `minimatch` | `>=7.0.0 <11.0.1` moderate（globparts DoS） | 要修得跨 major 到 11.x，可能搞挂 `@ts-morph/common` |

两条都只在 dev / 构建链，不进生产运行时。按「纯增量、可回退、不动架构」原则留档。

## 启停

```bash
# 起
cd ~/tools/platforms/lance-local && ./start-local.sh
# 停
pkill -f "lance -addr"
# PG
export PATH=/opt/homebrew/opt/postgresql@17/bin:$PATH LC_ALL=C
pg_ctl -D ~/lance-pg/data stop        # 或 -l ~/lance-pg/pg.log start
```

## 与云端那份的关系

| | 云端 100.105.199.53:8787 | 本机 127.0.0.1:8787 |
|---|---|---|
| 版本 | 0.3.15-global.1（官方二进制） | 0.3.15-local（源码自建） |
| 架构 | linux/amd64 | darwin/arm64 |
| 数据库 | Docker PG 5433 | Homebrew PG 17 @5433 |
| 工具链 | 镜像被弃 → nmap/chromium 待确认 | nmap/ffuf/nuclei/node 齐；chromium 见下 |
| LLM | ocfree 免费 lane（易 429） | magpie 本地网关（稳定） |

**结论**：本机这份在「工具链 + LLM 通道」上比云端更完整，适合真跑；云端那份适合当远程控制台。
