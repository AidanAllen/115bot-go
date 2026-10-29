#!/usr/bin/env bash
# 本机一键更新部署新版 115bot（Go 版）
#
#   ./scripts/deploy.sh 0.2.4            # 编译 -> 上传 -> 服务器重建 -> 清理上一版遗留
#   SKIP_TESTS=1 ./scripts/deploy.sh 0.2.4
#   CLEAN_ONLY=1 ./scripts/deploy.sh 0.2.4   # 只上传并清理，不重建容器
#
# 清理范围（只针对新版 115bot，不碰老 Java 版 115bot 与其他容器）：
#   - 上一版 hi_115bot 镜像、本项目构建产生的悬空镜像（每次重建都会留一个约 30MB）
#   - 早期源码构建遗留：src/ Dockerfile go.mod go.sum Makefile .gitignore
#   - data/ 下超过保留份数的数据库备份、超过 24 小时的下载临时文件
set -euo pipefail

VERSION_ARG="${1:-}"
if [ -z "$VERSION_ARG" ]; then
  echo "用法: $0 <版本号>   例: $0 0.2.4" >&2
  exit 1
fi
VERSION="${VERSION_ARG#v}"

HERE="$(cd "$(dirname "$0")" && pwd)"                 # .../115bot/bot-go/scripts
APP="$(cd "$HERE/.." && pwd)"                          # .../115bot/bot-go
ROOT="$(cd "$APP/.." && pwd)"                          # .../115bot
REMOTE_DIR="${REMOTE_DIR:-/home/docker/docker-compose/hi_115bot}"
RSH="$ROOT/rsh.sh"
CLEAN_ONLY="${CLEAN_ONLY:-0}"

# 本机没有系统 Go，用仓库内的离线工具链
export PATH="$ROOT/tools/go/bin:$PATH" \
       GOFLAGS=-mod=mod \
       GOCACHE="$ROOT/tools/gocache" \
       GOPATH="$ROOT/tools/gopath" \
       GOPROXY=off

upload() { # 本地文件 -> 服务器路径（一次命令只喂一个 cat，避免 stdin 串味）
  echo "↑ $2"
  cat "$1" | "$RSH" "mkdir -p '$(dirname "$2")' && cat > '$2'"
}

cd "$APP"

# 1) 编译前自检
if [ "${SKIP_TESTS:-0}" != "1" ]; then
  echo "== go vet / go test =="
  go vet ./...
  go test ./... -count=1
fi

# 2) 交叉编译 Linux 二进制
#
# 不用 make：本机（Git Bash）没有 make，直接调 go 更稳。
# 版本号通过 ldflags 注入，/v 与启动日志里看到的就是它。
echo "== 编译 v$VERSION =="
BUILD_TIME="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build \
  -ldflags "-s -w -X github.com/ghs/115bot-go/internal/bot.Version=$VERSION -X github.com/ghs/115bot-go/internal/bot.BuildTime=$BUILD_TIME" \
  -o bin/115bot-linux-amd64 ./cmd/bot
ls -lh bin/115bot-linux-amd64

# 3) 上传二进制（先落 .new，由服务器脚本原子替换）
upload "$APP/bin/115bot-linux-amd64" "$REMOTE_DIR/bin/115bot-linux-amd64.new"
"$RSH" "chmod +x '$REMOTE_DIR/bin/115bot-linux-amd64.new'"

# 4) 上传编排文件与服务器端脚本
upload "$APP/docker-compose.yml"     "$REMOTE_DIR/docker-compose.yml"
upload "$APP/Dockerfile.runtime"     "$REMOTE_DIR/Dockerfile.runtime"
upload "$APP/.dockerignore"          "$REMOTE_DIR/.dockerignore"
upload "$HERE/deploy-remote.sh"      "$REMOTE_DIR/scripts/deploy-remote.sh"
"$RSH" "chmod +x '$REMOTE_DIR/scripts/deploy-remote.sh'"

# 5) 远端执行：备份 -> 换二进制 -> 重建 -> 清遗留
echo "== 服务器端执行 =="
"$RSH" "VERSION=$VERSION CLEAN_ONLY=$CLEAN_ONLY '$REMOTE_DIR/scripts/deploy-remote.sh'"

echo "== 部署结束：v$VERSION =="
