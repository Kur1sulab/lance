#!/usr/bin/env bash
set -euo pipefail

# 1. 安装前端（Next.js）依赖并做静态构建
echo "[*] Building Frontend..."
cd web
npm ci
npm run build:static
cd ..

# 2. 将构建好的前端产物复制到 Go 可内嵌（embed）的位置
echo "[*] Copying static assets..."
mkdir -p server/webui/dist
cp -r web/out/* server/webui/dist/

# 3. 编译 Go 单一二进制（必须带 embedui 标签）
echo "[*] Compiling Go Backend..."
CGO_ENABLED=0 go build -tags embedui -o lance ./cmd/lance

echo "[+] Build Completed successfully!"
