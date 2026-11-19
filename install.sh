#!/usr/bin/env bash
# LANCE 安装脚本：① 全 Docker  ② 本地编译运行
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }
rand(){ head -c 18 /dev/urandom | base64 | tr -dc 'A-Za-z0-9' | head -c 24; }

# ── docker 环境检测 / 自动安装 ──────────────────
ensure_docker(){
  if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
    ok "已确认 docker 和 docker compose"; return
  fi
  warn "找不到 docker / docker compose"
  case "$(uname -s)" in
    Linux)
      if [ "$(ask '是否自动安装 Docker？(y/n)' y)" = y ]; then
        curl -fsSL https://get.docker.com | sh
        sudo usermod -aG docker "$USER" || true
        ok "Docker 安装已完成（用户组变更需重新登录后才能免 sudo 生效）"
      else
        die "请直接安装 docker 后重新运行"
      fi ;;
    Darwin) die "在 macOS 上请安装 Docker Desktop：https://www.docker.com/products/docker-desktop/" ;;
    *)      die "请直接安装 docker 后重新运行" ;;
  esac
}

# ── ① 全部 Docker ──────────────────────────────
install_docker(){
  ensure_docker
  if [ ! -f .env ]; then
    cp .env.example .env 2>/dev/null || true
    local pw key
    pw="$(ask 'Postgres 密码（按回车则随机生成）' "$(rand)")"
    key="$(ask 'ANTHROPIC_API_KEY（可留空，稍后在 UI 中设置）' '')"
    sed -i.bak "s|^POSTGRES_PASSWORD=.*|POSTGRES_PASSWORD=${pw}|" .env
    sed -i.bak "s|^ANTHROPIC_API_KEY=.*|ANTHROPIC_API_KEY=${key}|" .env
    rm -f .env.bak
    ok "已创建 .env 文件（POSTGRES_PASSWORD 已设置）"
  else
    info "沿用已存在的 .env 文件"
  fi
  info "拉取镜像并启动…"
  docker compose pull || true
  if ! docker compose up -d; then
    warn "无法拉取或启动镜像 ghcr.io/Kur1sulab/lance(请确认 .env 中的 LANCE_TAG 是已发布的标签, 以及网络是否可用)"
    warn "重新运行此脚本并选择 \"2) 本地运行 (go 编译)\"，也可以按 README 中 \"从源码编译单一二进制\" 的路径构建"
    die "Docker Compose(全部 Docker) 启动失败"
  fi
  ok "启动完成 → http://localhost:8787"
  info "首次访问时需在 /setup 页面输入 \"设置令牌\"才能创建管理员密码"
  info "确认设置 token：docker compose logs lance | grep -F '[auth]'   （默认发布地址为 127.0.0.1，可通过 .env 的 LANCE_BIND 修改）"
  info "检查日志: docker compose logs -f lance"
}

# ── ② 本地编译运行 ────────────────────────────
install_local(){
  echo "数据库安装方式:"
  echo "  1) 连接现有 PostgreSQL"
  echo "  2) 用 Docker 启动一个 PostgreSQL (需要 docker)"
  case "$(ask '选择' 1)" in
    2)
      ensure_docker
      local pw; pw="$(ask 'Postgres 密码（按回车则随机生成）' "$(rand)")"
      docker run -d --name lance-pg -p 5432:5432 \
        -e POSTGRES_USER=lance -e POSTGRES_PASSWORD="$pw" -e POSTGRES_DB=lance \
        -v lance-pg:/var/lib/postgresql/data postgres:16-alpine
      DB_HOST=127.0.0.1 DB_PORT=5432 DB_USER=lance DB_PASS="$pw" DB_NAME=lance DB_SSL=disable ;;
    *)
      DB_HOST="$(ask '数据库地址' 127.0.0.1)"
      DB_PORT="$(ask '端口' 5432)"
      DB_USER="$(ask '账户' lance)"
      DB_PASS="$(ask '密码' '')"
      DB_NAME="$(ask '数据库名称' lance)"
      DB_SSL="$(ask 'sslmode (disable/require)' disable)" ;;
  esac

  # 生成 config.json
  cat > config.json <<JSON
{
  "database": {
    "host": "${DB_HOST}",
    "port": ${DB_PORT},
    "user": "${DB_USER}",
    "password": "${DB_PASS}",
    "dbname": "${DB_NAME}",
    "sslmode": "${DB_SSL}"
  }
}
JSON
  ok "已创建 config.json 文件"

  #go 环境检查
  command -v go >/dev/null 2>&1 || die "找不到 Go。请先安装 Go (>=1.26): https://go.dev/dl/"
  ok "Go: $(go version)"

  # 要内嵌前端，需要用 node 生成静态产物
  if command -v npm >/dev/null 2>&1; then
    info "构建前端静态产物…"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && mkdir -p server/webui && cp -r web/out server/webui/dist
    info "编译内嵌前端的单一二进制…"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o lance ./cmd/lance
  else
    warn "找不到 npm：仅编译未内嵌前端的后端（前端通过 npm run dev 单独运行）"
    CGO_ENABLED=0 go build -o lance ./cmd/lance
  fi
  ok "编译已完成 → ./lance"

  info "正在启动…（按 Ctrl-C 退出）。默认绑定为 127.0.0.1:8787，首次访问时请在 /setup 页面输入下方日志中的 [auth] 配置 token"
  ./lance
}

echo "=============================="
echo "  LANCE 安装"
echo "  1) 全部用 Docker 安装"
echo "  2) 本地运行 (go 编译)"
echo "=============================="
case "$(ask '选择' 1)" in
  1) install_docker ;;
  2) install_local ;;
  *) die "选择无效" ;;
esac
