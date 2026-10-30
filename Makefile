APP        := 115bot
PKG        := github.com/AidanAllen/115bot-go
VERSION    ?= 1.0.1
BUILD_TIME := $(shell date -u +%Y-%m-%dT%H:%M:%SZ)
LDFLAGS    := -s -w \
	-X $(PKG)/internal/bot.Version=$(VERSION) \
	-X $(PKG)/internal/bot.BuildTime=$(BUILD_TIME)

.PHONY: all build linux test vet fmt clean run check-cookie dist deploy

all: vet test build

build:
	CGO_ENABLED=0 go build -ldflags "$(LDFLAGS)" -o bin/$(APP) ./cmd/bot

# 交叉编译到 Linux（部署目标），静态链接、免依赖
linux:
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -ldflags "$(LDFLAGS)" -o bin/$(APP)-linux-amd64 ./cmd/bot
	@ls -lh bin/$(APP)-linux-amd64

# 全部受支持平台的一次性产物（Release 用），等同 ./scripts/build-all.sh
# 矩阵改 PLATFORMS 变量即可，例：make dist PLATFORMS="linux/amd64 linux/arm64"
dist:
	@VERSION=$(VERSION) PLATFORMS="$(PLATFORMS)" ./scripts/build-all.sh

vet:
	go vet ./...

test:
	go test ./... -count=1

fmt:
	gofmt -w ./cmd ./internal

run:
	go run ./cmd/bot

clean:
	rm -rf bin

# 部署前自查：验证 115 cookie 登录态
check-cookie:
	go run ./cmd/bot --check-cookie

# 一键更新部署到服务器：编译 -> 上传 -> 重建 -> 清理上一版遗留镜像与文件
#   make deploy VERSION=0.2.4
#   CLEAN_ONLY=1 make deploy VERSION=0.2.4   # 只清理，不重建
deploy:
	@./scripts/deploy.sh $(VERSION)
