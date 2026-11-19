#!/usr/bin/env bash
# LANCE 更新脚本：① Docker 更新（拉取新镜像并重建）  ② 本地编译更新（重新构建二进制）
# 与 install.sh 配套。install 负责首次安装，update 负责升级到新版本。
# 无需手动执行 DB 迁移。lance 每次启动都会幂等地重新执行 schema.sql（ADD COLUMN/CREATE
# 含 INDEX IF NOT EXISTS）"重启即迁移"。数据（pgdata 卷、./data、./skills）不受影响。
set -euo pipefail
cd "$(cd "$(dirname "$0")" && pwd)"

info(){ printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok(){   printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn(){ printf '\033[33m[!]\033[0m %s\n' "$*"; }
die(){  printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }
ask(){  local p="$1" d="${2:-}" a; read -rp "$p${d:+ [$d]}: " a; echo "${a:-$d}"; }

# ── 可选：将仓库同步到最新代码（compose、脚本、本地编译源码都会随之更新） ───────
sync_repo(){
  [ -d .git ] && command -v git >/dev/null 2>&1 || { warn "不是 git 工作副本，跳过 git pull"; return; }
  [ "$(ask '是否拉取最新代码 (git pull --ff-only)? (y/n)' y)" = y ] || return
  if ! git pull --ff-only; then
    warn "git pull 无法快进（存在本地修改或分支已分叉）。请手动处理后重试。本次直接使用当前代码"
  fi
}

# ── ① Docker 更新 ───────────────────────────────
update_docker(){
  command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1 \
    || die "找不到 docker / docker compose。请先使用 ./install.sh 安装并部署"
  [ -f .env ] || die "找不到 .env。请先通过 ./install.sh 完成首次部署"

  # 可选：升级到指定的版本 tag（留空则遵循 .env 中的 LANCE_TAG，默认值为 latest）
  local tag; tag="$(ask '目标镜像 tag（回车则使用 .env / latest）' '')"
  if [ -n "$tag" ]; then
    if grep -q '^LANCE_TAG=' .env; then
      sed -i.bak "s|^LANCE_TAG=.*|LANCE_TAG=${tag}|" .env && rm -f .env.bak
    else
      printf '\nLANCE_TAG=%s\n' "$tag" >> .env
    fi
    ok "已将 LANCE_TAG 设置为 ${tag}"
  fi

  # 只改动 lance。postgres 固定为 16-alpine，无需跟着升级（拉取也只是浪费带宽，
  # 主版本变化时也存在兼容性风险）。lance 声明了 depends_on postgres，因此需附加服务名
  # 执行 up 时，若 pg 未启动会自动拉起；若已启动则保持原样，不重新构建。
  info "拉取新镜像（仅 lance）…"
  if ! docker compose pull lance; then
    warn "无法拉取镜像 ghcr.io/Kur1sulab/lance(请确认指定的 tag 是已发布的发行标签, 以及网络是否可用)"
    die "镜像更新失败。也可以用 \"2) 本地更新(用 go 重新编译)\" 进行更新"
  fi
  info "重新构建后启动(lance 重启时会自动迁移 schema)…"
  docker compose up -d lance
  ok "更新完成 → http://localhost:8787"
  info "查看日志: docker compose logs -f lance"
  info "清理旧镜像（可选）：docker image prune -f"
}

# ── ② 本地编译更新 ──────────────────────────────
update_local(){
  command -v go >/dev/null 2>&1 || die "找不到 Go (>=1.26): https://go.dev/dl/"
  [ -f config.json ] || warn "找不到 config.json。首次部署请使用 ./install.sh"
  ok "Go: $(go version)"

  if command -v npm >/dev/null 2>&1; then
    info "重新构建前端静态产物…"
    ( cd web && npm ci && npm run build:static )
    rm -rf server/webui/dist && cp -r web/out server/webui/dist
    info "重新编译内嵌前端的单一二进制…"
    CGO_ENABLED=0 go build -tags embedui -trimpath -o lance ./cmd/lance
  else
    warn "找不到 npm。仅编译未内嵌前端的后端（前端需通过 npm run dev 单独运行）"
    CGO_ENABLED=0 go build -o lance ./cmd/lance
  fi
  ok "编译完成 → ./lance"
  warn "要应用更改，请重启正在运行的 lance 进程（重启时会自动迁移 schema）"
}

echo "=============================="
echo "  LANCE 更新"
echo "  1) Docker 更新(拉取新镜像并重新构建)"
echo "  2) 本地更新(用 go 重新编译)"
echo "=============================="
case "$(ask '选择' 1)" in
  1) sync_repo; update_docker ;;
  2) sync_repo; update_local ;;
  *) die "选择无效" ;;
esac
