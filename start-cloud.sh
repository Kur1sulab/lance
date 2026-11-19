#!/usr/bin/env bash
set -euo pipefail

# Render 会随机分配 PORT 环境变量，因此需指定参数使应用在该端口启动
#保留默认执行及技能/数据路径
: "${PORT:?必须设置 PORT 环境变量}"
echo "[*] Starting LANCE Server on port :$PORT"
exec ./lance -addr ":$PORT"
