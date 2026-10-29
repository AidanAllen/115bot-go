#!/usr/bin/env bash
# 交叉编译出所有受支持平台的二进制，供 GitHub Release 分发。
#
#   ./scripts/build-all.sh            # 版本号取 internal/bot/app.go 里的默认值
#   VERSION=1.1.0 ./scripts/build-all.sh
#   PLATFORMS="linux/amd64 linux/arm64" ./scripts/build-all.sh
#
# 产物落在 bin/dist/：
#   115bot-linux-amd64      115bot-linux-arm64     115bot-linux-armv7
#   115bot-windows-amd64.exe  115bot-windows-arm64.exe
#   115bot-darwin-amd64     115bot-darwin-arm64
#   SHA256SUMS.txt
#
# 说明：
#   - 全部 CGO_ENABLED=0 静态编译（SQLite 用的是 modernc.org/sqlite 纯 Go 实现，
#     不需要 cgo，所以交叉编译零外部依赖、产物可直接丢到目标机跑）。
#   - linux/arm 指 32 位 ARM（GOARM=7，软浮点设备请用 GOARM=5 自行调整）。
#   - Windows 产物带 .exe 后缀，其余平台不带。
set -euo pipefail

HERE="$(cd "$(dirname "$0")" && pwd)"
APP="$(cd "$HERE/.." && pwd)"
ROOT="$(cd "$APP/.." && pwd)"

# 版本号：优先用传入的，否则从源码默认值里抠出来
VERSION="${VERSION:-}"
if [ -z "$VERSION" ]; then
  VERSION="$(grep -m1 'var Version = ' "$APP/internal/bot/app.go" | sed 's/.*"\(.*\)".*/\1/')"
fi
VERSION="${VERSION#v}"

# 默认矩阵：改这个变量即可裁剪（例：只出服务器用的两个 → PLATFORMS="linux/amd64 linux/arm64"）
PLATFORMS="${PLATFORMS:-linux/amd64 linux/arm64 linux/arm windows/amd64 windows/arm64 darwin/amd64 darwin/arm64}"

# 本机没有系统 Go，用仓库内的离线工具链
export PATH="$ROOT/tools/go/bin:$PATH" \
       GOFLAGS=-mod=mod \
       GOCACHE="$ROOT/tools/gocache" \
       GOPATH="$ROOT/tools/gopath" \
       GOPROXY=off

PKG=github.com/AidanAllen/115bot-go
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
LDFLAGS="-s -w -X $PKG/internal/bot.Version=$VERSION -X $PKG/internal/bot.BuildTime=$BUILD_TIME"

OUT="$APP/bin/dist"
rm -rf "$OUT"
mkdir -p "$OUT"

cd "$APP"
echo "== 编译 115bot-go v$VERSION =="

for p in $PLATFORMS; do
  os="${p%/*}"
  arch="${p#*/}"
  arm=""
  if [ "$arch" = "arm" ]; then
    arm="7"   # GOARM=7：树莓派 2/3/4 等主流 32 位 ARM 板子
  fi

  name="115bot-$os-$arch"
  [ "$arch" = "arm" ] && name="115bot-$os-armv7"
  [ "$os" = "windows" ] && name="$name.exe"

  printf '  %-14s %-6s -> %s\n' "$os" "$arch" "$name"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" GOARM="$arm" \
    go build -ldflags "$LDFLAGS" -o "$OUT/$name" ./cmd/bot
done

# 校验和：下载方用来确认产物没被篡改/传坏
(
  cd "$OUT"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum 115bot-* > SHA256SUMS.txt
  else
    # macOS 没有 sha256sum
    shasum -a 256 115bot-* > SHA256SUMS.txt
  fi
)

echo
echo "== 产物（$OUT）=="
ls -lh "$OUT" | tail -n +2
