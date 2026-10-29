# 115bot-go

[![Go Version](https://img.shields.io/badge/Go-1.23.4-00ADD8?style=flat-square&logo=go&logoColor=white)](https://go.dev/)
[![License](https://img.shields.io/badge/License-MIT-green?style=flat-square)](LICENSE)
[![CI](https://github.com/AidanAllen/115bot-go/actions/workflows/ci.yml/badge.svg)](https://github.com/AidanAllen/115bot-go/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/AidanAllen/115bot-go?style=flat-square)](https://github.com/AidanAllen/115bot-go/releases)

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

### 2. 直接下载二进制（不想编译就走这条）

[Releases](https://github.com/AidanAllen/115bot-go/releases) 里有每个平台的预编译二进制，
下载对应那个、加执行权限、配好 `.env` 就能跑：

```bash
# Linux x86_64 为例；其他平台换文件名（见下表）
wget https://github.com/AidanAllen/115bot-go/releases/latest/download/115bot-linux-amd64
chmod +x 115bot-linux-amd64
cp .env.example .env && vi .env          # 至少填 BOT_TOKEN
./115bot-linux-amd64
```

下载后可校验：`sha256sum -c 115bot-linux-amd64.sha256`（同名的 `.sha256` 文件一起下）。

### 3. 构建与运行

```bash
# 本机直接跑
make build && ./bin/115bot

# 或交叉编译 Linux 二进制（服务器部署用）
make VERSION=1.1.0 linux        # 产出 bin/115bot-linux-amd64

# 所有平台一次性构建（Release 就是这么打的包）
make dist                       # = ./scripts/build-all.sh，产物在 bin/dist/
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
> 想突破 20MB：本地 Bot API 服务端本身只有 Linux 容器镜像，Windows 上要靠
> Docker Desktop 跑它（见下节），exe 通过 HTTP 用它；不跑就仍是官方 20MB 限制。

ARM 机器用 Docker：

```bash
BIN_NAME=115bot-linux-arm64 docker compose up -d --build     # 32 位用 -armv7
```

### 4. Docker 部署

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

### 5. 突破 20MB：内置 MTProto 下载（推荐）

配好 `TG_API_ID` + `TG_API_HASH` 即可，**不需要 Docker，Windows 跑 exe 也有效**：

```env
TG_API_ID=12345678
TG_API_HASH=0123456789abcdef0123456789abcdef
```

到 <https://my.telegram.org/> 申请（与本地 Bot API 服务用的是同一组凭据，可以复用）。
配了之后：

- 超过 20MB 的文件自动走 MTProto 下载（官方 API 那条路必然失败，不会白跑一趟）；
- 20MB 以内仍走官方 HTTP 下载，零额外开销；
- 官方下载失败时会用 MTProto 再兜一次；
- 没配就完全等同老版本行为。

实现在 `internal/tgfile`：用 gotd/td（纯 Go 的 MTProto 2.0）把 Bot API 的
`file_id` 就地解码成文件位置，再直连对应 DC 下载。收发消息仍然走 Bot API，
命令、相册、批量进度那些逻辑一行没动。

> 需要代理的环境设 `ALL_PROXY` / `NO_PROXY`（SOCKS5，golang.org/x/net/proxy 语义）。
> MTProto 会话（含 auth key）默认存在 `./data/tg.session.json`，属敏感数据。

### 5b. 备选：Windows 下跑本地 Bot API 容器（不配 MTProto 凭据时）

`telegram-bot-api` 服务端只有 Linux 镜像，Windows 跑不了原生版，但可以用
Docker Desktop（WSL2 后端）单独起这一个容器，exe 走 HTTP 用它：

```powershell
docker run -d --name tgbotapi `
  -p 127.0.0.1:8081:8081 `
  -v "${PWD}\tgdata:/var/lib/telegram-bot-api" `
  -e TELEGRAM_API_ID=12345678 `
  -e TELEGRAM_API_HASH=0123456789abcdef0123456789abcdef `
  -e TELEGRAM_LOCAL=1 `
  aiogram/telegram-bot-api:10.3
```

（CMD 里把行尾反引号换成 `^`，`${PWD}` 换成 `%CD%`。api_id / api_hash 到
<https://my.telegram.org/> 申请，属于凭据，别外传。）

然后在 `.env` 里指过去：

```env
TG_API_ENDPOINT=http://127.0.0.1:8081/bot%s/%s
```

验证：`curl http://127.0.0.1:8081/bot<TOKEN>/getMe` 能返回 bot 信息即可。

**Windows 与 Linux 的差异**：`--local` 返回的是 Linux 容器内的绝对路径
（`/var/lib/telegram-bot-api/...`），exe 上 `os.Stat` 不到，程序会打一行
「本地 Bot API 路径不可读，退回 HTTP 下载」并自动走回环 HTTP 拉文件——
**20MB 限制照样被突破**（限制来自官方服务器，不在这一步），只是少了
Linux 下的「零拷贝直读」，多一次本机 HTTP 拷贝（回环速度，可忽略）。

> `tgdata/` 会缓存 bot 收到的所有文件，注意留磁盘；
> 同一个 bot token 不能同时挂官方 server 和本地 server（会互相踢），
> 想换回官方，把 `TG_API_ENDPOINT` 删掉重启即可。

### 6. 升级（一键部署脚本）

已经在服务器上跑起来的，用仓库里的脚本更新最省事——它会
编译 → 上传 → 备份数据库 → 原子换二进制 → 重建容器 → **清理上一版遗留的镜像与临时文件**：

```bash
./scripts/deploy.sh 1.1.0              # 等价于 make deploy VERSION=1.1.0
SKIP_TESTS=1 ./scripts/deploy.sh 1.1.0 # 跳过 vet/test
CLEAN_ONLY=1 ./scripts/deploy.sh 1.1.0 # 只清理旧镜像与残留，不重建
TARGET_ARCH=arm64 ./scripts/deploy.sh 1.1.0   # 目标是 ARM 服务器
```

清理范围**只针对本项目**（按「悬空 + 入口 `/app/115bot`」识别），不会误删其他应用的镜像。

> 重建容器会中断进行中的转存任务，大文件转存期间别发版。

### 7. 本地 Bot API 的版本为什么钉死在 10.3

compose 里写的是 `aiogram/telegram-bot-api:10.3`，不是 `latest`，原因：

- **latest 会漂**：Hub 上同一个 `10.3` 在 2026-10 内就被重建过多次
  （实测 10-28 与 10-29 两次 digest 不同），每次 pull 都可能换掉 tdlib，
  行为和目录约定都可能变，出问题时往往表现为「整条消息解析失败」这种静默故障。
- **本项目客户端库是 `go-telegram-bot-api v5.5.1`（对应 Bot API 5.x）**，
  与服务端 10.3 差了好几个大版本。目前用到的字段
  （`Photo` / `Video` / `Document` / `MediaGroupID` / `Caption` / `FileID` / `FileSize`、
  `getFile.FilePath` 在 `--local` 下返回绝对路径）都是常年稳定的老字段，实测可用；
  已知边界是 Bot API 7.0 起的 `external_reply`——跨聊天的引用回复不再给
  `reply_to_message`，v5.5.1 解析不到，那种情况下 `/d` 回复会找不到文件
  （同聊天内回复正常）。
- **tdlib 的库只升不降**：换回旧镜像可能起不来。

`10.3` 是 multi-arch manifest，amd64 / arm64 机器都写同一个 tag；
还有 `10.3-linuxarmv7`、`10.3-linux386` 等专用 tag。
想钉到某一次具体构建，把 tag 换成 `@sha256:<digest>`（`docker inspect` 可查）。

升级步骤：备份 `tgdata/` → 改 tag → `docker compose --profile localapi up -d` →
验证 `getMe` 与一次真实转存 → 不行就改回原 tag。

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
| `TG_API_ENDPOINT` | | 本地 Bot API 端点（Linux 容器方案），见下 |
| `TG_API_ID` + `TG_API_HASH` | | **大文件（>20MB）推荐**：MTProto 直连下载，不需要 Docker，Windows exe 同样有效 |

> 从 v0.2.0 起，115 凭据（cookie / UA / 通道 / 目录）**按用户存数据库**，环境变量只作兜底。
> 多用户部署时 `TRANSFER_CID` / `OFFLINE_CID` / `DEFAULT_UA` 请留空。

## 架构

```
cmd/bot           进程入口、配置加载、优雅退出
                  （signals_unix.go / signals_windows.go 按平台接退出信号）
internal/config   环境变量加载与启动校验
internal/store    SQLite 持久化（app_config / user_config / task）
internal/p115     115 适配层（登录 / 目录 / 离线 / 上传 / 转存通道）
internal/bot      Telegram 层（命令 / 路由 / 菜单 / 状态机）
                  pathutil.go     跨平台路径判断（本地 Bot API 给的是 POSIX 绝对路径）
                  mediagroup.go   相册缓冲与共享说明命名
                  app.go          转存队列 / 批量进度消息
internal/tgfile   MTProto 直连下载（gotd/td），用于突破官方 20MB 限制
scripts           deploy.sh（一键部署+清理）、deploy-remote.sh（服务器端）、
                  build-all.sh（多平台构建）、smoke_bot.py（本地冒烟）
.github/workflows ci.yml（测试 + 7 平台交叉编译校验）、
                  release.yml（push tag 后自动构建并上传 Release 产物）
```

跨平台相关的三条约定（改动时留意）：

- 判断本地 Bot API 给的路径必须用 `internal/bot` 的 `isAbsPath`，
  不能直接 `filepath.IsAbs`（后者按运行平台判断，Windows 上会把 POSIX 绝对路径认成相对路径）
- 退出信号写在 `cmd/bot/signals_<平台>.go`，不要在 `main.go` 里写死 `syscall.SIGTERM`
- 临时目录、账号文件一类路径用 `os.TempDir()`，不要硬写 `/tmp`

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

- **20MB 门槛**：官方 Bot API 限制 `getFile` 20MB、上传 50MB。
  突破办法有二选一：配置 `TG_API_ID` + `TG_API_HASH` 走内置 MTProto 下载（推荐，
  跨平台、无需 Docker），或部署本地 Bot API 服务（只有 Linux 容器镜像，
  Windows 上要靠 Docker Desktop 跑，见 5b）。两者都不做就仍受 20MB 限制
- 上传走 115 网页通道，不支持分片：115 回调校验的 `${sha1}` 在分片模式拿不到整体值，合并会报 `errno=10002`
- 上传吞吐受部署机器上行带宽限制，跨境部署时较慢（实测日本 → 阿里云深圳丢包约 15%，
  真实上传约 30KB/s；命中 115 秒传时是秒级完成）。大文件建议优先用离线下载
- 离线任务提交为同步方式，大批量链接（>50 条）会较慢
- 重建容器会中断进行中的转存任务，大文件转存期间别发版

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
