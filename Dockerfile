# 多阶段构建：静态二进制 + 极简运行镜像
FROM golang:1.23-alpine AS build

WORKDIR /src

# 先只拷贝依赖清单，命中缓存
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG VERSION=1.0.1
ARG BUILD_TIME=unknown

RUN CGO_ENABLED=0 GOOS=linux go build \
    -ldflags "-s -w \
      -X github.com/AidanAllen/115bot-go/internal/bot.Version=${VERSION} \
      -X github.com/AidanAllen/115bot-go/internal/bot.BuildTime=${BUILD_TIME}" \
    -o /out/115bot ./cmd/bot

# ---- 运行镜像 ----
FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata ffmpeg \
    && ln -snf /usr/share/zoneinfo/Asia/Shanghai /etc/localtime \
    && echo "Asia/Shanghai" > /etc/timezone

WORKDIR /app
COPY --from=build /out/115bot /app/115bot

# 数据与临时目录通过卷挂载，保证容器重建不丢配置
VOLUME ["/app/data"]

# 非 root 运行
RUN adduser -D -u 10001 appuser && chown -R appuser:appuser /app
USER appuser

ENV DB_PATH=/app/data/115bot.db \
    TEMP_DIR=/app/data/tmp \
    LOG_LEVEL=info

ENTRYPOINT ["/app/115bot"]
