# 115bot-go 全栈测试与代码审查报告

日期：2026-10-30
范围：`bot-go/` 全部 Go 源码、部署脚本、Docker 编排、CI 工作流、README/.env 文档

---

## 一、测试基线（全部通过）

| 检查项 | 结果 |
|---|---|
| `gofmt -l ./cmd ./internal` | ✅ 无格式问题 |
| `go vet ./...` | ✅ 无告警 |
| `go test ./... -count=1` | ✅ 全绿（bot/p115/store/tgfile 四个有测试的包） |
| 七平台交叉编译（linux amd64/arm64/armv7、windows amd64/arm64、darwin amd64/arm64） | ✅ 全部 OK |

工具链：项目内置离线 Go 1.23.4（`tools/go`），`GOPROXY=off`。

---

## 二、本轮修复的缺陷（已改代码）

### P0-1 大文件下载被 getFile 提前堵死（致命，功能失效）

**文件**：`internal/bot/cmd_offline.go`（原 `runTransfer`）

**问题**：`runTransfer` 第一步无条件调 `a.api.GetFile(...)`，失败即 `return`。但 Telegram 官方 Bot API 对 >20MB 文件在 `getFile` 这一步就返回 `400 "file is too big"`（已多源查证）。结果是：**即使配好了 `TG_API_ID`/`TG_API_HASH`，>20MB 文件也永远走不到 MTProto 分支**——大文件转存功能实际失效。

**修复**：抽出 `fetchToLocal`，按正确顺序分派：
1. 明确 >20MB 且 MTProto 可用 → 跳过 getFile，直接 `file_id` 走 MTProto；
2. getFile 失败但 MTProto 可用 → 仍用 file_id 兜底（MTProto 不需要 file_path）；
3. 其余 → 官方 getFile + 下载。

### P0-2 队列计数时序竞态（会误报「队列已满」）

**文件**：`internal/bot/app.go`（`queueTransfer`）

**问题**：原实现「先入队，后 `queued.Add(1)`」。worker 可能抢先取出并 `Add(-1)`，使本次 `Add(1)` 返回 0，调用方误判「队列已满」→ 触发 `inflight.Delete` + `releaseName` 回滚，但文件其实已经在转了，导致去重标记与文件名占位被误撤（同一文件可能被转存两遍）。

**修复**：计数在入队前 `Add(1)`，入队失败再 `Add(-1)` 回滚。与 `TestTransferQueuePositionWhenDrained` 的语义一致，测试全绿。

### P1-3 文件名截断丢扩展名

**文件**：`internal/bot/cmd_offline.go`（`sanitizeFileName`）

**问题**：超长文件名直接 `[:180]` 切前缀，末尾 `.mp4` 会被一起切掉，115 识别不出文件类型。

**修复**：先取出扩展名、截前缀、再拼回扩展名。

### P1-4 队列满时单文件发出两条矛盾消息

**文件**：`internal/bot/cmd_offline.go`（`enqueueTransferIn`）

**问题**：单文件已发「已加入队列」消息，队列满时又 `sendReply` 一条「队列已满」，用户同时看到两句互相矛盾的话。

**修复**：改用 `report` 就地改写那条「已加入队列」消息。

---

## 三、发现的问题（已于同日全部修复）

### H1（高危）扫码登录的 UA 与设备不匹配 —— ✅ 已修复

**文件**：`internal/p115/qrlogin.go` `normalizeQRApp` + `NewQRLoginWithApp`；`internal/bot/cmd_login.go`

`normalizeQRApp` 为 ios 系列返回专用 UA（`UPhone/1.0.0`、`OfficePhone/1.0.0` 等），但 `NewQRLoginWithApp` 用 `name, _ :=` **丢弃了返回的 UA**，强制所有设备都用网页 Chrome UA；`cmd_login.go` 登录成功后写库的 UA 也硬编码 `p115.DefaultUA`。这违反「UA 与会话一致」红线（工作记忆明确）。

**修复内容**（2026-10-30 用户确认后执行）：
- `NewQRLoginWithApp` 改为 `name, ua := normalizeQRApp(app)`，`ua` 为空回落 `DefaultUA`，与 `ResultFull` 已有的取值路径对齐（整个登录流程：取码/轮询/换 cookie 用同一份 UA）；
- 新增 `QRLogin.UA()` 访问器，`cmd_login.go` 写库改为 `ua := qr.UA()`；
- 新增 `TestQRLoginUAByApp` 单元测试，锁定「设备 → 登录 UA」映射表防回归。

**行为影响**：默认设备 `qandroid`/`alipaymini`/`wechatmini`/`web` 的 UA 不变（仍是 `DefaultUA`），**只有 ios 系列（ios/qios/ipad/qipad）的扫码请求 UA 从网页 Chrome 变为其专用 UA**。这本来就是 `normalizeQRApp` 注释声明与 `ResultFull` 已实现的意图（「不匹配会被识别为伪造设备」），原实现是写了一半的疏漏。⚠️ 部署后建议用 `/login app=ios` 做一次真实会话验证。

### H2（中）`webUploader` 的 `u.token`/`u.ossEP`/`u.info` 并发读弱保护 —— ✅ 已修复

**文件**：`internal/p115/webupload.go`

`Upload` 里 `u.token = tok`、`u.ossEP = ep` 裸写。经核实，bot 侧 `userUploader` **每次上传都新建 uploader 实例**（`user.go` 现构造，不缓存），叠加单 worker 串行，双重保证无实害——但代码结构上没写明这一点，一旦未来复用实例就会踩雷。

**修复内容**：
- 删除 `Upload` 里冗余的裸写 `u.token = tok`（`uploadToken` 内部已带锁写缓存，此处是重复且绕锁的第二次写）；
- `ossEP` 缓存读写改为带锁（带锁读判空 → miss 请求 → 带锁写回），与 `token`/`info` 的既有模式统一，`Upload` 内不再有任何绕锁字段访问；
- 在 `webUploader` 结构注释上写明并发契约：**每上传任务使用独立实例，勿跨 goroutine 复用**；`mu` 只保证串行复用时缓存不撕裂。

### M3（低）`uploadUA` 常量近乎死代码 —— ✅ 核实为有效代码，已补注释

**文件**：`internal/p115/cipher.go`

复核发现 `uploadUA` 并非死代码，有两处真实用途：`newWebUploaderWithChannel` 在通道 UA 为空时兜底；`channel_test.go` 用它锚定默认通道 UA（常量与通道表两处取值不允许漂移）。已在定义处补注释写明这两个用途。

### M4（低）`showDirBrowser` 内两处 `answerCallback("", "")` —— ✅ 已修复

**文件**：`internal/bot/cmd_dirbrowser.go`

传空 callback id 应答，是注定失败的无效请求。经核实 `msgID != 0` 的调用唯一来源是 `handleDirCallback` 的 "e"/"p" 分支，外层均已应答过 `cbID`，这两行是纯冗余。**已删除**，并在函数注释写明约定：`msgID != 0` 的调用必须来自 `handleDirCallback`（入口已应答）。顺带简化了未登录分支的 if/else（两条路径合并为一句 `send`）。

**修复后回归**：`gofmt` / `go vet` / `go test ./...` 全绿，含新增的 `TestQRLoginUAByApp`。

---

## 四、安全审查结论

- ✅ **无硬编码密钥/Token**：所有真实凭据走环境变量（`${VAR}`）或 SQLite（`app_config`/`user_config`），`.env` 不落库。
- ✅ **无敏感信息写日志**：全库 grep 未发现 cookie/token/userkey/auth 写入日志。
- ✅ **用户数据按 user_id 隔离**：cookie/UA/通道/目录/task 全部 `user_id` 维度，无跨用户串号路径。
- ✅ **路径穿越防护**：`sanitizeFileName` 处理 `/\`、`\x00`、`..`、超长。
- ✅ **cookie 明文存 SQLite** 是既有约定（`data/` 不暴露），README 已提示 `chmod 600 .env`、`chown 101:101 data`。
- ⚠️ MTProto 会话文件（`tg.session.json` 含 auth key）落盘 `./data/`，属敏感数据，文档已标注。

---

## 五、一致性审查（上一轮已完成的部分，本次复核确认）

`.env.example` / README / `config.go` 三者环境变量已对齐：README 提到的变量、config 读取的变量、`.env.example` 声明的变量三方一致，唯一「多出」的 `ALL_PROXY`/`NO_PROXY` 由 Go 标准库读取（`http.ProxyFromEnvironment`），属预期。

---

## 六、结论

项目整体质量高：测试覆盖扎实（媒体组、队列竞态回归、UA/通道一致性、cookie 校验等都有专门测试），注释详实且大多带「实测/踩坑依据」，跨平台红线遵守到位。

本轮最关键成果是修复了 **P0-1**：这是会让「配置了 MTProto 却仍下不了大文件」的致命功能缺陷，此前被 getFile 的错误处理顺序掩盖。

第三节的 H1–M4 已于同日经用户确认后全部修复，验证全绿。唯一遗留事项：**ios 系列扫码的 UA 行为发生了变化**（恢复为 `normalizeQRApp` 声明的专用 UA），部署后建议在服务器用 `/login app=ios` 做一次真实会话验证。
