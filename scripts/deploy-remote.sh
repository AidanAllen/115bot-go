#!/bin/sh
# 在服务器上执行：换入新二进制 -> 重建容器 -> 删除上一版遗留（旧镜像/源码构建残留/过期临时文件/旧备份）
#
# 由本机 scripts/deploy.sh 上传后调用，也可以单独执行：
#   VERSION=0.2.4 sh /home/docker/docker-compose/hi_115bot/scripts/deploy-remote.sh
#   CLEAN_ONLY=1 sh /home/docker/docker-compose/hi_115bot/scripts/deploy-remote.sh   # 只清理，不重建
#
# BIN_NAME 指定二进制文件名（多平台构建后名字带 os/arch）：
#   BIN_NAME=115bot-linux-arm64 VERSION=1.1.0 sh .../deploy-remote.sh
set -e

DIR="${DEPLOY_DIR:-/home/docker/docker-compose/hi_115bot}"
VERSION="${VERSION:-dev}"
CLEAN_ONLY="${CLEAN_ONLY:-0}"
KEEP_BACKUPS="${KEEP_BACKUPS:-5}"
BIN_NAME="${BIN_NAME:-115bot-linux-amd64}"

cd "$DIR"

echo "== 部署目录: $DIR  版本: $VERSION =="

# 1) 备份数据库（用 sqlite3 .backup 才能把 WAL 一起收进来；没有 sqlite3 就退回 cp）
if [ -f data/115bot.db ]; then
  BAK="data/115bot.db.bak-v${VERSION#v}"
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 data/115bot.db ".backup '$BAK'"
  else
    cp -f data/115bot.db "$BAK"
  fi
  echo "[1/6] 数据库已备份 -> $BAK"
else
  echo "[1/6] 未找到 data/115bot.db，跳过备份"
fi

# 备份只留最近 N 份，其余删掉
ls -1t data/115bot.db.bak-* 2>/dev/null | tail -n +$((KEEP_BACKUPS + 1)) | xargs -r rm -f

# 2) 清掉早期「源码构建版」遗留的文件（现在只用 Dockerfile.runtime，这些都不需要）
rm -rf src Dockerfile go.mod go.sum Makefile .gitignore
echo "[2/6] 已清理源码构建残留（src/ Dockerfile go.mod go.sum Makefile .gitignore）"

if [ "$CLEAN_ONLY" = "1" ]; then
  echo "== CLEAN_ONLY=1：跳过换二进制与重建 =="
else
  # 3) 换入新二进制
  if [ -f "bin/$BIN_NAME.new" ]; then
    mv -f "bin/$BIN_NAME.new" "bin/$BIN_NAME"
    chmod +x "bin/$BIN_NAME"
    echo "[3/6] 已换入新二进制 ($BIN_NAME)"
  else
    echo "[3/6] 未发现 bin/$BIN_NAME.new，沿用现有二进制"
  fi

  # 4) 记录当前镜像，重建后再删它
  OLD_IMAGE="$(docker images -q hi_115bot:latest)"
  PROFILE=""
  if grep -q 'telegram-bot-api' .env 2>/dev/null; then PROFILE="--profile localapi"; fi
  # shellcheck disable=SC2086
  docker compose $PROFILE up -d --build
  echo "[4/6] 容器已重建（profile: ${PROFILE:-默认}）"
fi

# 5) 删除本次被顶掉的镜像，以及历史上遗留的本项目悬空镜像
#    识别方式：悬空 + 入口是 /app/115bot —— 只可能是本项目的旧镜像，不会误伤别的容器
[ -n "${OLD_IMAGE:-}" ] && docker rmi -f $OLD_IMAGE 2>/dev/null || true
for id in $(docker images -f 'dangling=true' -q); do
  ep="$(docker image inspect --format '{{json .Config.Entrypoint}}' "$id" 2>/dev/null || echo '')"
  case "$ep" in
    *"/app/115bot"*) docker rmi -f "$id" >/dev/null 2>&1 || true ;;
  esac
done
echo "[5/6] 旧镜像已清理"

# 6) 清掉超过一天的下载临时文件（正常任务用完即删，这里只兜底）
find data/tmp -type f -mmin +1440 -delete 2>/dev/null || true

echo "[6/6] 剩余镜像："
docker images --format '  {{.Repository}}:{{.Tag}}\t{{.Size}}\t{{.CreatedSince}}' | grep -E 'hi_115bot|none' || true
echo "容器状态："
docker compose ps --format '  {{.Service}}\t{{.State}}\t{{.Image}}' 2>/dev/null || true
echo "最近日志："
docker compose logs --no-color --tail=5 2>/dev/null | sed 's/^/  /' || true
echo "== 完成 =="
