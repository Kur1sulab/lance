#!/usr/bin/env bash
# 评测层自检：构建 → 静态检查 → 单测（含端到端真跑靶）→ 顺序跑全部夹具题。
#
# 退出码有意义：
#   0  全绿
#   1  构建/运行失败（基础设施问题，不产生分数）
#   2  judge 标定不一致（判据有 bug：正例判 false 或反例判 true）
set -euo pipefail
cd "$(dirname "$0")/.."

echo "[1/4] go build ./..."
go build ./...

echo "[2/4] go vet ./eval/... ./cmd/evald/"
go vet ./eval/... ./cmd/evald/

echo "[3/4] go test ./eval/...（含端到端：真起靶、真判后果）"
go test ./eval/... -count=1

echo "[4/4] 顺序跑全部夹具题，打印三轴 scorecard"
go run ./cmd/evald -run-all
