# 115bot-go

[![Go Version](docs/badges/go.svg)](https://go.dev/)
[![License](docs/badges/license.svg)](LICENSE)
[![CI](docs/badges/ci.svg)](https://github.com/AidanAllen/115bot-go/actions/workflows/ci.yml)
[![Release](docs/badges/release.svg)](https://github.com/AidanAllen/115bot-go/releases)

<!-- 徽章为仓库内自托管 SVG（docs/badges/），不依赖 shields.io 等第三方服务：
     部分网络环境到 img.shields.io / github.com 间歇性超时，动态徽章还会因
     GitHub API 限流裂图，自托管后任何环境都能显示。release 版本号由
     release.yml 的 sync-badge job 在发版时自动更新 release.svg。 -->

115 网盘自动化 Telegram 机器人（Go 版）。把 Telegram 里的视频、文件、磁力链一键转存到你的 115 网盘。

- 🚀 **内存优先**：闲置常驻仅 ~8MB，单二进制部署（对比 Java 版 72MB / 镜像 323MB）
- 📱 **扫码登录**：bot 直接发二维码，手机扫一下即可，无需手抄 cookie
- 👥 **多用户隔离**：每个 Telegram 用户登录自己的 115 账号，文件各进各的网盘
- 🔗 **离线下载**：直接发磁力链 / ed2k 链接，由 115 服务器自己拉取
- 📦 **跨平台单文件**：7 个平台各一个可执行文件，零依赖，Windows 双击也能跑

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
- （要转存 >20MB 的文件）到 <https://my.telegram.org/> 申请 `api_id` / `api_hash`，见第 4 节

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

Windows 上直接跑：

```bat
copy .env.example .env
rem 编辑 .env 填入 BOT_TOKEN
115bot-windows-amd64.exe
```

### 3. 构建与运行

```bash
# 本机直接跑
make build && ./bin/115bot

# 或交叉编译 Linux 二进制（服务器部署用）
make VERSION=1.0.2 linux        # 产出 bin/115bot-linux-amd64

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

> Windows 下优雅退出只响应 Ctrl+C（Windows 没有 Unix 的 SIGTERM 语义）；
> Linux 下 `docker stop` / systemd 发的 SIGTERM 同样会优雅退出。

### 4. 突破 20MB：内置 MTProto 下载

官方 Bot API 硬性限制 `getFile` 只能下 20MB 以内的文件，**这是服务器端的限制，代码绕不过去**。
官方给出的解法是跑一个独立的 `telegram-bot-api` 服务，但它是 C++ 写的、只有 Linux 容器镜像，
Windows / macOS 上得先装 Docker，门槛过高。

本项目的做法是把「下载」这一步搬进主程序：用纯 Go 的 MTProto 实现（gotd/td）直连 Telegram 取文件，
**不需要 Docker，不需要额外进程**，Windows 双击 exe 同样有效。

```env
TG_API_ID=12345678
TG_API_HASH=0123456789abcdef0123456789abcdef
```

到 <https://my.telegram.org/> 申请（免费，一分钟的事）。配了之后：

- 超过 20MB 的文件自动走 MTProto 下载（官方那条路必然失败，不会白跑一趟）；
- 20MB 以内仍走官方 HTTP 下载，零额外开销；
- 官方下载失败时会用 MTProto 再兜一次；
- **不配置**则超过 20MB 的文件无法转存，其余功能不受影响。

实现在 `internal/tgfile`：把 Bot API 的 `file_id` 就地解码成文件位置再直连对应 DC 下载。
收发消息仍然走 Bot API，命令、相册、批量进度那些逻辑不受影响。

链路自检（不用启动机器人）：

```bash
115bot --check-mtproto <file_id>      # 需要代理的环境设 ALL_PROXY
```

`file_id` 可从 `curl https://api.telegram.org/bot<TOKEN>/getUpdates` 里取。

> 需要代理的环境设 `ALL_PROXY` / `NO_PROXY`（SOCKS5，golang.org/x/net/proxy 语义）；
> Docker 部署时也可以直接写进 `.env`，会随 `env_file` 一起注入容器。
> MTProto 会话（含 auth key）默认存在 `./data/tg.session.json`，属敏感数据。

**与多用户 / 115 账号配置的关系**：两者完全正交，互不影响。

| | `TG_API_ID` + `TG_API_HASH` | 115 账号配置 |
|---|---|---|
| 归属 | Telegram **应用**凭据 | 每个 TG 用户各自的 115 账号 |
| 存放 | 环境变量，进程级只有一份 | SQLite `user_config` 表，按 `user_id` 分行 |
| 内容 | 一对 id + hash | `account.cookie` / `account.ua` / `account.channel` / `account.offlineCid` / `account.transferCid` 等 |
| 作用 | 仅用于大文件下载 | 登录 115、选转存目录、选上传通道 |

MTProto 登录的身份是 **bot token**（`client.Auth().Bot`），不是某个 TG 用户，更不是 115 账号。
所以它只负责「把 bot 能看到的文件取回来」，落到谁的网盘、用谁的 cookie 仍由该用户的
`account.*` 决定——不会因为配了这组凭据就让多用户的 115 配置被覆盖或串号。

唯一的交汇点是**并发**：下载器在进程里是全局单实例，且用互斥锁串行化（session 文件被并发
读写会写坏）。所以多人同时转存大文件时，**下载这一步会排队**，前一个人的下完才轮到下一个；
各自的 115 上传仍走各自 cookie，互不影响。

### 5. Docker 部署

```bash
# 把 docker-compose.yml、Dockerfile.runtime、.dockerignore、
# bin/115bot-linux-amd64、.env 放到同一目录后：
mkdir -p data && chown -R 101:101 data
chmod 600 .env
docker compose up -d --build
```

ARM 机器：

```bash
BIN_NAME=115bot-linux-arm64 docker compose up -d --build     # 32 位用 -armv7
```

验证：

```bash
docker compose logs -f                                   # 看到「机器人已启动」
docker run --rm --env-file .env -v "$PWD/data:/app/data" \
  hi_115bot:latest --check-cookie                        # 校验 115 登录态
```

### 6. 升级（一键部署脚本）

已经在服务器上跑起来的，用仓库里的脚本更新最省事——它会
编译 → 上传 → 备份数据库 → 原子换二进制 → 重建容器 → **清理上一版遗留的镜像与临时文件**：

```bash
./scripts/deploy.sh 1.0.2              # 等价于 make deploy VERSION=1.0.2
SKIP_TESTS=1 ./scripts/deploy.sh 1.0.2 # 跳过 vet/test
CLEAN_ONLY=1 ./scripts/deploy.sh 1.0.2 # 只清理旧镜像与残留，不重建
TARGET_ARCH=arm64 ./scripts/deploy.sh 1.0.2   # 目标是 ARM 服务器
```

清理范围**只针对本项目**（按「悬空 + 入口 `/app/115bot`」识别），不会误删其他应用的镜像。

> 重建容器会中断进行中的转存任务，大文件转存期间别发版。

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

见 `.env.example`。真正需要关心的只有两项：

| 变量 | 必填 | 说明 |
|---|---|---|
| `BOT_TOKEN` | ✅ | Telegram Bot Token |
| `TG_API_ID` + `TG_API_HASH` | | **要转存 >20MB 文件才需要**：MTProto 直连下载，不需要 Docker，跨平台一致 |

其余变量**一般都不用设置**，保持 `.env.example` 里的默认或注释状态即可：

- `OWNER_ID` —— 所有者 id，只是归属标记（不限制谁能用），留空则首个点「绑定为我」（`/id` 或 `/setid`）的人成为所有者
- `TG_API_ENDPOINT` —— 自定义 Bot API 端点，只有访问 Telegram 不方便、要走自建反代时才填
- `TG_MTPROTO_SESSION` —— MTProto 会话落盘位置，默认 `./data/tg.session.json`（含 auth key，敏感），一般不用改
- `DB_PATH` / `TEMP_DIR` —— 数据库与临时文件位置，默认 `./data/` 下，一般不用改

> 115 凭据（cookie / UA / 通道 / 目录）**按用户存数据库**，环境变量只作兜底。
> `TRANSFER_CID` / `OFFLINE_CID` / `DEFAULT_UA` / `UPLOAD_CHANNEL` 在 `.env.example`
> 里默认就是注释状态，保持不动即可。

> `TG_API_ID` / `TG_API_HASH` 是 Telegram **应用级**凭据，全局只有一份，与每个用户各自的
> 115 账号配置互不影响（见第 4 节末尾的对照表）。

## 架构

```
cmd/bot           进程入口、配置加载、优雅退出
                  （signals_unix.go / signals_windows.go 按平台接退出信号）
internal/config   环境变量加载与启动校验
internal/store    SQLite 持久化（app_config / user_config / task）
internal/p115     115 适配层（登录 / 目录 / 离线 / 上传 / 转存通道）
internal/bot      Telegram 层（命令 / 路由 / 菜单 / 状态机）
                  mediagroup.go   相册缓冲与共享说明命名
                  app.go          转存队列 / 批量进度消息
internal/tgfile   MTProto 直连下载（gotd/td），用于突破官方 20MB 限制
scripts           deploy.sh（一键部署+清理）、deploy-remote.sh（服务器端）、
                  build-all.sh（多平台构建）、smoke_bot.py（本地冒烟）
.github/workflows ci.yml（测试 + 7 平台交叉编译校验）、
                  release.yml（push tag 后自动构建并上传 Release 产物）
```

跨平台相关的两条约定（改动时留意）：

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

- **20MB 门槛**：官方 Bot API 限制 `getFile` 20MB、上传 50MB，这是服务器端的硬限制。
  唯一的突破方式是配置 `TG_API_ID` + `TG_API_HASH` 走内置 MTProto 下载（见第 4 节），
  跨平台、无需 Docker；不配置则超过 20MB 的文件无法转存
- **大文件下载是全局串行**：MTProto 下载器单实例 + session 文件不能并发写，
  多人同时转存 >20MB 时下载阶段会排队（上传仍各自进行）
- 上传走 115 网页通道，不支持分片：115 回调校验的 `${sha1}` 在分片模式拿不到整体值，合并会报 `errno=10002`
- 上传吞吐受部署机器上行带宽限制，跨境部署时较慢（实测日本 → 阿里云深圳丢包约 15%，
  真实上传约 30KB/s；命中 115 秒传时是秒级完成）。大文件建议优先用离线下载
- 离线任务提交为同步方式，大批量链接（>50 条）会较慢
- 跨聊天的引用回复（Bot API 7.0 起改为 `external_reply`）解析不到，
  那种情况下 `/d` 回复会找不到文件（同聊天内回复正常）
- 重建容器会中断进行中的转存任务，大文件转存期间别发版

## License

[MIT](LICENSE) © 2026 AidanAllen

## 免责声明

本项目仅供学习与个人合法使用。使用者须自行确保其使用行为符合所在地法律法规，并遵守 115 网盘及 Telegram 的相关服务条款。

任何人下载、使用或分发本项目的代码，均视为已充分理解并同意：因使用本项目而产生的任何后果（包括但不限于侵犯他人权益、违反平台规则、违反当地法律等），均由使用者本人承担，与本项目作者无关。
