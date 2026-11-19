#!/usr/bin/env bash
# LANCE cross-platform release builder.
#
# The default mode builds one target and embeds the already-exported frontend.
# `./build.sh --release` builds and packages all supported desktop/server targets.
#
# Environment variables:
#   LANCE_TARGET_OS=linux             One target OS in single-target mode.
#   LANCE_TARGET_ARCH=amd64           One target arch in single-target mode.
#   LANCE_TARGETS=linux/amd64,...     Comma-separated targets for multi-target mode.
#   LANCE_BUILD_VERSION=v0.3.3        Version embedded in the binary and archive name.
#   LANCE_OUTPUT=/path/to/lance       Explicit binary path in single-target mode.
#   LANCE_OUTPUT_DIR=dist             Directory for default binary paths.
#   LANCE_PACKAGE=1                   Create a zip archive for each target.
#   LANCE_PACKAGE_DIR=dist            Directory for release archives.
#   LANCE_COMPRESS=off                UPX mode: off, auto, or required.
#   LANCE_UPX_ARGS="--best --lzma"    Arguments passed to UPX.
#   LANCE_SKIP_FRONTEND=1             Reuse server/webui/dist (for CI artifact builds).
#   LANCE_SKIP_NPM_CI=1               Skip npm ci while rebuilding the frontend.
#   LANCE_GOSUMDB=sum.golang.org      Go checksum database.
set -euo pipefail

cd "$(cd "$(dirname "$0")" && pwd)"

info() { printf '\033[36m[*]\033[0m %s\n' "$*"; }
ok() { printf '\033[32m[+]\033[0m %s\n' "$*"; }
warn() { printf '\033[33m[!]\033[0m %s\n' "$*" >&2; }
die() { printf '\033[31m[x]\033[0m %s\n' "$*" >&2; exit 1; }

usage() {
  cat <<'EOF'
用法:
  ./build.sh                         按当前系统与架构编译
  ./build.sh --target linux/amd64   只编译指定的一个目标
  ./build.sh --release              编译全部支持的目标并打包

选项:
  --release              编译 Linux、macOS、Windows 的 amd64/arm64 目标并生成 zip
  --target OS/ARCH       指定单个目标，例如 windows/amd64
  --upx                  强制用 UPX 压缩二进制（个别 Linux 环境可能受影响）
  --no-compress          不用 UPX，只靠 Go linker 瘦身后再压缩 zip
  --help                 显示帮助

多个目标列表可用 LANCE_TARGETS 覆盖，例如:
  LANCE_TARGETS=linux/amd64,windows/amd64 ./build.sh --release
  LANCE_TARGETS=linux/amd64,windows/amd64 ./build.sh --release
EOF
}

RELEASE_TARGETS_DEFAULT="linux/amd64,linux/arm64,darwin/amd64,darwin/arm64,windows/amd64"
LANCE_RELEASE="${LANCE_RELEASE:-0}"
LANCE_COMPRESS="${LANCE_COMPRESS:-off}"
LANCE_PACKAGE="${LANCE_PACKAGE:-0}"

while [ "$#" -gt 0 ]; do
  case "$1" in
    --release)
      LANCE_RELEASE=1
      LANCE_PACKAGE=1
      shift
      ;;
    --target)
      [ "$#" -ge 2 ] || die "--target 需要 OS/ARCH 参数"
      target_arg="$2"
      case "$target_arg" in
        */*)
          LANCE_TARGET_OS="${target_arg%%/*}"
          LANCE_TARGET_ARCH="${target_arg##*/}"
          LANCE_TARGETS="$target_arg"
          ;;
        *) die "目标必须为 OS/ARCH 格式。例如: linux/amd64" ;;
      esac
      shift 2
      ;;
    --no-compress)
      LANCE_COMPRESS=0
      shift
      ;;
    --upx)
      LANCE_COMPRESS=required
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *) die "未知参数：$1（使用 --help 查看用法）" ;;
  esac
done

command -v go >/dev/null 2>&1 || die "找不到 Go（本项目需要 Go 1.26 及以上）"

LANCE_GOSUMDB="${LANCE_GOSUMDB:-sum.golang.org}"
if [ -z "${LANCE_BUILD_VERSION:-}" ]; then
  if command -v git >/dev/null 2>&1 && git rev-parse --is-inside-work-tree >/dev/null 2>&1; then
    LANCE_BUILD_VERSION="$(git describe --tags --always --dirty)"
  else
    LANCE_BUILD_VERSION="dev"
  fi
fi
# Release tags are commonly passed as v0.3.3; keep the binary version consistent.
LANCE_BUILD_VERSION="${LANCE_BUILD_VERSION#v}"
LANCE_OUTPUT_DIR="${LANCE_OUTPUT_DIR:-dist}"
LANCE_PACKAGE_DIR="${LANCE_PACKAGE_DIR:-$LANCE_OUTPUT_DIR}"
LANCE_UPX_ARGS="${LANCE_UPX_ARGS:---best --lzma}"

if [ "${LANCE_RELEASE}" = "1" ]; then
  LANCE_TARGETS="${LANCE_TARGETS:-$RELEASE_TARGETS_DEFAULT}"
else
  LANCE_TARGET_OS="${LANCE_TARGET_OS:-$(GOSUMDB="$LANCE_GOSUMDB" go env GOOS)}"
  LANCE_TARGET_ARCH="${LANCE_TARGET_ARCH:-$(GOSUMDB="$LANCE_GOSUMDB" go env GOARCH)}"
  LANCE_TARGETS="${LANCE_TARGETS:-${LANCE_TARGET_OS}/${LANCE_TARGET_ARCH}}"
fi

if [ "${LANCE_SKIP_FRONTEND:-0}" = "1" ]; then
  [ -d server/webui/dist ] || die "LANCE_SKIP_FRONTEND=1，但 server/webui/dist 不存在"
else
  command -v npm >/dev/null 2>&1 || die "找不到 npm（前端静态构建需要 Node.js/npm）"
  command -v rsync >/dev/null 2>&1 || die "找不到 rsync"
  info "构建前端静态资源"
  if [ "${LANCE_SKIP_NPM_CI:-0}" != "1" ]; then
    (cd web && npm ci)
  fi
  (cd web && npm run build:static)
  info "将前端资源同步到 server/webui/dist"
  mkdir -p server/webui/dist
  rsync -a --delete web/out/ server/webui/dist/
fi

compress_binary() {
  binary="$1"
  goos="$2"
  case "$LANCE_COMPRESS" in
    0|off|false|none)
      info "跳过 UPX：$binary"
      return 0
      ;;
    auto|required|true|1) ;;
    *) die "LANCE_COMPRESS 必须是 off、auto、required 之一" ;;
  esac

  if ! command -v upx >/dev/null 2>&1; then
    if [ "$LANCE_COMPRESS" = "required" ]; then
      die "LANCE_COMPRESS=required，但找不到 upx"
    fi
    warn "找不到 upx，保留 linker 压缩结果不变：$binary"
    return 0
  fi

  before=$(wc -c < "$binary" | tr -d ' ')
  upx_args="$LANCE_UPX_ARGS"
  [ "$goos" = "darwin" ] && upx_args="$upx_args --force-macos"
  # shellcheck disable=SC2086
  if ! upx $upx_args -- "$binary"; then
    if [ "$LANCE_COMPRESS" = "required" ]; then
      die "UPX 压缩失败：$binary"
    fi
    warn "UPX 不支持此目标格式，保留未压缩的二进制文件：$binary"
    return 0
  fi
  after=$(wc -c < "$binary" | tr -d ' ')
  ok "UPX 压缩完成：$binary（${before} -> ${after} bytes）"
}

package_binary() {
  binary="$1"
  goos="$2"
  goarch="$3"
  package_name="lance-${LANCE_BUILD_VERSION}-${goos}-${goarch}"
  package_root="${LANCE_PACKAGE_DIR}/${package_name}"
  archive="${LANCE_PACKAGE_DIR}/${package_name}.zip"

  command -v zip >/dev/null 2>&1 || die "打包需要 zip"
  rm -rf "$package_root" "$archive"
  mkdir -p "$package_root"
  cp "$binary" "$package_root/"
  # 守护进程启动脚本才是正式入口。界面的一键更新需要在该进程退出后由这个脚本重新拉起才能生效，
  # 直接运行 lance 主体时，更新后不会重新启动。只包含一套适配目标系统的版本。
  if [ "$goos" = "windows" ]; then
    cp start.bat "$package_root/"
  else
    cp start.sh "$package_root/"
    chmod +x "$package_root/start.sh"
  fi
  cp -R skills "$package_root/"
  cp config.example.json "$package_root/"
  if [ -f README.md ]; then cp README.md "$package_root/"; fi
  (cd "$LANCE_PACKAGE_DIR" && zip -q -r -9 "$(basename "$archive")" "$(basename "$package_root")")
  rm -rf "$package_root"
  ok "发布压缩包: $archive"
}

build_target() {
  target="$1"
  case "$target" in
    */*) ;;
    *) die "无效的目标: $target(必须为 OS/ARCH 格式)" ;;
  esac
  goos="${target%%/*}"
  goarch="${target##*/}"
  case "$goos" in
    linux|darwin|windows) ;;
    *) die "不支持的系统：$goos（支持 linux、darwin、windows）" ;;
  esac

  binary_name="lance"
  [ "$goos" = "windows" ] && binary_name="lance.exe"
  if [ -n "${LANCE_OUTPUT:-}" ] && [ "$LANCE_RELEASE" != "1" ]; then
    output="$LANCE_OUTPUT"
  else
    output="${LANCE_OUTPUT_DIR}/lance-${goos}-${goarch}/${binary_name}"
  fi
  mkdir -p "$(dirname "$output")"

  info "编译 ${goos}/${goarch}，版本 ${LANCE_BUILD_VERSION}"
  GOSUMDB="$LANCE_GOSUMDB" \
  CGO_ENABLED=0 \
  GOOS="$goos" \
  GOARCH="$goarch" \
  go build \
    -tags embedui \
    -trimpath \
    -ldflags "-s -w -buildid= -X main.version=${LANCE_BUILD_VERSION}" \
    -o "$output" \
    ./cmd/lance

  compress_binary "$output" "$goos"
  if command -v file >/dev/null 2>&1; then file "$output"; fi
  if [ "$LANCE_PACKAGE" = "1" ]; then package_binary "$output" "$goos" "$goarch"; fi
  ok "编译完成：$output"
}

write_checksums() {
  [ "$LANCE_PACKAGE" = "1" ] || return 0
  checksum_file="$LANCE_PACKAGE_DIR/SHA256SUMS"
  if command -v sha256sum >/dev/null 2>&1; then
    (cd "$LANCE_PACKAGE_DIR" && for archive in *.zip; do sha256sum "$archive"; done > "$(basename "$checksum_file")")
  elif command -v shasum >/dev/null 2>&1; then
    (cd "$LANCE_PACKAGE_DIR" && for archive in *.zip; do shasum -a 256 "$archive"; done > "$(basename "$checksum_file")")
  else
    warn "找不到 sha256sum 或 shasum，跳过 SHA256SUMS"
    return 0
  fi
  ok "校验和文件：$checksum_file"
}

mkdir -p "$LANCE_OUTPUT_DIR"
if [ "$LANCE_PACKAGE" = "1" ]; then mkdir -p "$LANCE_PACKAGE_DIR"; fi

old_ifs="$IFS"
IFS=','
read -r -a targets <<< "$LANCE_TARGETS"
IFS="$old_ifs"
[ "${#targets[@]}" -gt 0 ] || die "LANCE_TARGETS 不能为空"
for target in "${targets[@]}"; do
  target="${target//[[:space:]]/}"
  [ -n "$target" ] || continue
  build_target "$target"
done

if [ "$LANCE_PACKAGE" = "1" ]; then
  write_checksums
  info "已生成发布包: $LANCE_PACKAGE_DIR"
fi
