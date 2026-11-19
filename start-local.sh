#!/usr/bin/env bash
# Lance 本地启动器（macOS arm64, 2026-11-12 源码自建）
set -euo pipefail
cd "$(dirname "$0")"

export LANCE_CONFIG="$PWD/config.json"
export LANCE_LANG=zh
# 子进程（技能里的 bash）需要 node / playwright-cli 在 PATH 上
export PATH="$HOME/.local/bin:$HOME/go/bin:/Applications/ServBay/script/alias:/Applications/ServBay/bin:$PATH"
# 管理员口令：读 ~/.lance-local-admin.pw（改名前的旧文件名已随目录一同搬走）
PW_FILE="$HOME/.lance-local-admin.pw"
if [ ! -f "$PW_FILE" ]; then
  echo "缺少管理员口令文件：$PW_FILE（先跑一次控制台初始化，或写入一个初始口令）" >&2
  exit 1
fi
export LANCE_ADMIN_PASSWORD="$(cat "$PW_FILE")"
# LLM：本机 magpie 网关（OpenAI 兼容）
export LANCE_LLM_PROVIDER=openai
export LANCE_LLM_BASE_URL=http://127.0.0.1:3425/v1
export LANCE_LLM_MODEL=auto/deepseek-v4.1-flash
export OPENAI_API_KEY=magpie

ADDR="${LANCE_ADDR:-127.0.0.1:8790}"
# 端口被占时先说是谁占的，别让用户对着一句 bind: address already in use 猜
if lsof -nP -iTCP:"${ADDR##*:}" -sTCP:LISTEN >/dev/null 2>&1; then
  echo "端口 ${ADDR} 已被占用：$(lsof -nP -iTCP:"${ADDR##*:}" -sTCP:LISTEN | awk 'NR==2{print $1" pid="$2}')" >&2
  echo "换个端口：LANCE_ADDR=127.0.0.1:8791 ./start-local.sh" >&2
  exit 1
fi
exec ./lance -addr "$ADDR" -data "$PWD/data" -proxy 127.0.0.1:8788
