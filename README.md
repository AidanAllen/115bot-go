# 115bot-go

[![Go Version](https://img.shields.io/badge/Go-1.23.4-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![CI](https://github.com/AidanAllen/115bot-go/actions/workflows/ci.yml/badge.svg)](https://github.com/AidanAllen/115bot-go/actions/workflows/ci.yml)

115 网盘自动化 Telegram 机器人（Go 版）。把 Telegram 里的视频、文件、磁力链一键转存到你的 115 网盘。

- 🚀 **内存优先**：闲置常驻仅 ~8MB，单二进制部署（对比 Java 版 72MB / 镜像 323MB）
- 📱 **扫码登录**：bot 直接发二维码，手机扫一下即可，无需手抄 cookie
- 👥 **多用户隔离**：每个 Telegram 用户登录自己的 115 账号，文件各进各的网盘
- 🔗 **离线下载**：直接发磁力链 / ed2k 链接，由 115 服务器自己拉取

## 功能

| 功能 | 用法 |
|---|---|
| 磁力链 / ed2k 离线下载 | 直接发送链接 |
| 视频 / 文件转存到 115 | `/d`（自动转存开关）或回复消息 + `/d` |
| 扫码登录 115 | `/login` |
| 设置 cookie / UA / 转存通道 | `/cookie` `/ua` `/channel` |
| 检查登录状态 | `/checklogin` |
| 设置转存 / 离线目录 | `/transfercid` `/setcid` |
| 账号初始化体检 | `/initaccount` |
| 功能开关 / 状态 / 版本 | `/s` `/state` `/v` |

**不用记命令**：启动时全部命令已注册到 Telegram，输入框打 `/` 即弹出带说明的列表，`/help` 里每条命令都有可点按钮。

### 转存进度怎么显示

- **单个文件**：只占一条消息，入队 → 下载 → 上传 → 成功全程就地改写这一条。
- **相册 / 一批文件**：整批只发**一条**汇总消息，每个文件占一行，状态就地更新：

```
📦 正在转存 · 8 个文件（已完成 2）
1. ✅ 名字.jpg · 2s · 24.62 KB/s
   pick_code：<code>xxxx</code>
2. ⬆️ 名字(1).jpg · 预计 3 秒
3. ⏳ 名字(2).mp4
```

跑完自动变成「✅ 转存完成 · 8 个文件，用时 1 分 12 秒」。
刷新做了 1.2 秒节流（小文件密集完成时会触发 Telegram 限流），但最后一行必定刷新。

### 转存后的文件叫什么

优先级：**手动改名**（`/d 新名字` 回复某条消息）> **说明文字**（caption）> **TG 原文件名** > 类型兜底名（`photo_xxx.jpg`）。

说明取「第一行去掉 `#标签` 后还有内容」的那行：频道转发常把 `#标签` 堆在首行，
真正的标题在后面，直接取首行会叫出「#合集 #分享.jpg」这种名字。

相册（一次发多张图/视频）会**共用同一段说明**：Telegram 把相册拆成多条消息、说明只挂在其中一条上，
所以带 `media_group_id` 的消息会先攒 2.5 秒（最长 10 秒），等说明到齐后按消息顺序统一命名，
重名的自动排号 —— `名字.jpg`、`名字(1).jpg`、`名字(2).jpg`。视频与图片扩展名不同，各自独立排号。

## 快速开始

### 1. 准备

- 在 [@BotFather](https://t.me/BotFather) 创建 bot，拿到 `BOT_TOKEN`
- 复制配置模板：`cp .env.example .env`，填入 token

### 2. 构建与运行

```bash
# 本机直接跑
make build && ./bin/115bot

# 或交叉编译 Linux 二进制（服务器部署用）
make linux          # 产出 bin/115bot-linux-amd64
```

### 支持的平台

项目是纯 Go 实现（SQLite 用 `modernc.org/sqlite`，不依赖 cgo），
所以每个平台都能静态编译出一个**单文件、零依赖**的可执行程序，拷过去直接跑：

| 平台 | 产物 | 典型设备 |
|---|---|---|
| Linux x86_64 | `115bot-linux-amd64` | 云服务器（默认部署目标） |
| Linux ARM64 | `115bot-linux-arm64` | ARM 服务器、NAS、Oracle ARM 实例 |
| Linux ARMv7 | `115bot-linux-armv7` | 树莓派 2/3/4 等 32 位 ARM |
| Windows x64 | `115bot-windows-amd64.exe` | 日常 Windows 机器 |
| Windows ARM64 | `115bot-windows-arm64.exe` | Surface / ARM 笔记本 |
| macOS Intel | `115bot-darwin-amd64` | |
| macOS Apple Silicon | `115bot-darwin-arm64` | |

一次性出全部平台（Release 就是这么打的包）：

```bash
./scripts/build-all.sh          # 或 make dist；产物在 bin/dist/，附 SHA256SUMS.txt
PLATFORMS="linux/amd64 linux/arm64" ./scripts/build-all.sh   # 只要其中几个
```

Windows 上直接跑：

```bat
copy .env.example .env
rem 编辑 .env 填入 BOT_TOKEN
115bot-windows-amd64.exe
```

> Windows 下优雅退出只响应 Ctrl+C（Windows 没有 Unix 的 SIGTERM 语义）；
> Linux 下 `docker stop` / systemd 发的 SIGTERM 同样会优雅退出。
> 本地 Bot API（`telegram-bot-api`）只有 Linux 容器方案，Windows 直连时仍受官方 20MB 限制。

ARM 机器用 Docker：

```bash
BIN_NAME=115bot-linux-arm64 docker compose up -d --build     # 32 位用 -armv7
```

### 3. Docker 部署

```bash
# 把 docker-compose.yml、Dockerfile.runtime、.dockerignore、
# bin/115bot-linux-amd64、.env 放到同一目录后：
mkdir -p data tgdata && chown -R 101:101 data tgdata
chmod 600 .env
docker compose up -d --build      # 需要 >20MB 大文件：加 --profile localapi
```

验证：

```bash
docker compose logs -f                                   # 看到「机器人已启动」
docker run --rm --env-file .env -v "$PWD/data:/app/data" \
  hi_115bot:latest --check-cookie                        # 校验 115 登录态
```

## 命令速查

| 命令 | 说明 |
|---|---|
| `/start` `/set115` | 打开 115 设置菜单 |
| `/login [设备]` | 扫码登录（可点按钮选设备） |
| `/cookie` | 手工设置 cookie（保存前会验证） |
| `/ua [档位]` | 设置 UA：网页 / App / 支付宝小程序 / 微信小程序 |
| `/channel [标识]` | 选择转存通道：web / app / alipaymini / wechatmini |
| `/transfercid` `/setcid` | 设置转存 / 离线目录（可点选或输入 cid） |
| `/checklogin` | 检查登录状态 |
| `/initaccount` | 账号初始化体检 |
| `/d` | 自动转存开关 / 回复消息转存某一条 |
| `/s` `/state` `/v` | 开关 / 状态 / 版本 |
| `/id` `/setid` | 查看 id / 绑定所有者 |
| `/cancel` | 取消当前输入 |

## 配置

见 `.env.example`，重点项：

| 变量 | 必填 | 说明 |
|---|---|---|
| `BOT_TOKEN` | ✅ | Telegram Bot Token |
| `OWNER_ID` | | 所有者 id（仅归属标记，不限制使用），留空则首个 `/setid` 生效 |
| `TG_API_ENDPOINT` | | 本地 Bot API 端点，收发 >20MB 文件时必须配置 |

> 从 v0.2.0 起，115 凭据（cookie / UA / 通道 / 目录）**按用户存数据库**，环境变量只作兜底。
> 多用户部署时 `TRANSFER_CID` / `OFFLINE_CID` / `DEFAULT_UA` 请留空。

## 架构

```
cmd/bot           进程入口、配置加载、优雅退出
internal/config   环境变量加载与启动校验
internal/store    SQLite 持久化（配置表 + 任务表）
internal/p115     115 适配层（登录 / 目录 / 离线 / 上传）
internal/bot      Telegram 层（命令 / 路由 / 菜单 / 状态机）
scripts           部署脚本 + 本地冒烟测试
```

## 开发

```bash
make vet      # 静态检查
make test     # 单元测试
make build    # 本机构建
make linux    # 交叉编译 Linux amd64
make dist     # 所有平台一次性构建（= ./scripts/build-all.sh）
```

改动跨平台相关代码后，务必确认每个 GOOS 都能编过（CI 里 `cross-build` job 会做）：

```bash
for t in linux/amd64 linux/arm64 windows/amd64 darwin/arm64; do
  CGO_ENABLED=0 GOOS=${t%/*} GOARCH=${t#*/} go build -o /dev/null ./...
done
```

## 已知限制

- 上传走 115 网页通道，>5GB 文件因 115 回调校验限制不支持分片，会明确报错
- 上传吞吐受部署机器上行带宽限制，跨境部署时较慢（建议大文件优先用离线下载）
- 离线任务提交为同步方式，大批量链接（>50 条）会较慢

## License

[MIT](LICENSE) © 2026 AidanAllen

## 免责声明

本项目仅供学习与个人合法使用。使用者须自行确保其使用行为符合所在地法律法规，并遵守 115 网盘及 Telegram 的相关服务条款。

任何人下载、使用或分发本项目的代码，均视为已充分理解并同意：因使用本项目而产生的任何后果（包括但不限于侵犯他人权益、违反平台规则、违反当地法律等），均由使用者本人承担，与本项目作者无关。

### 使用边界（明确禁止）

本项目**禁止**用于以下任何用途：

- ❌ 转存、下载或传播**侵权内容**（盗版影视、音乐、软件、电子书等）
- ❌ 传播**违法违规内容**（涉黄、涉赌、涉毒、涉暴恐等）
- ❌ 利用本工具进行**批量盗链、绕过付费或滥用平台资源**等违反服务条款的行为
- ❌ 任何可能侵犯他人知识产权、隐私权或合法权益的行为

作者仅提供工具代码本身，不对使用者的具体使用行为负责。请勿将本项目用于任何非法或侵权用途。
