# 115bot-go

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

### 3. Docker 部署

```bash
# 把 docker-compose.yml、Dockerfile.runtime、.dockerignore、
# bin/115bot-linux-amd64、.env 放到同一目录后：
mkdir -p data && chown -R 10001:10001 data
chmod 600 .env
docker compose up -d --build
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
```

## 已知限制

- 上传走 115 网页通道，>5GB 文件因 115 回调校验限制不支持分片，会明确报错
- 上传吞吐受部署机器上行带宽限制，跨境部署时较慢（建议大文件优先用离线下载）
- 离线任务提交为同步方式，大批量链接（>50 条）会较慢

## License

暂无
