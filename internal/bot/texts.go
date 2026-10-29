package bot

// 全部对用户可见的中文文案集中在此，便于统一调整措辞。
const (
	// 通用
	TxtNotOwner = "机器人所有者已绑定为别人，无法更改"
	// TxtNeed115Login 是「没登录 115」的统一说法。
	// 多用户模型下拦的不是人，而是「这个人还没有自己的 115 会话」。
	TxtNeed115Login = "你还没有登录 115，无法使用这个功能。\n\n发送 <code>/login</code> 扫码登录你自己的 115（推荐），或用 <code>/cookie</code> 手工设置 cookie。\n\n每个用户各自独立：你登录的是你自己的网盘，别人看不到你的文件。"
	// TxtUnknownCommandFmt 用于未知命令，承接 /help 的按钮，用户不必自己猜命令名。
	TxtUnknownCommandFmt = "未知命令：<code>/%s</code>\n\n下面是全部可用命令，点按钮即可执行："
	TxtUnknownAction     = "未知操作，已取消"
	TxtInputEmpty        = "输入不能为空"
	TxtInternalErr       = "操作失败：%s"
	TxtCanceled          = "已取消"
	TxtNothingToCancel   = "当前没有待输入的内容"

	// 开关 / 状态
	TxtBotEnabledOn  = "机器人 115 功能已开启"
	TxtBotEnabledOff = "机器人 115 功能已关闭"

	// 账号
	TxtLoginInvalid = "115 登录失效，请重新登录"
	TxtLoginOK      = "115 登录状态正常\n账号：<b>%s</b>（UID %d）"
	TxtAskCookie    = "请输入 cookie"
	TxtCookieOK     = "cookie 设置成功"
	TxtCookieBadFmt = "cookie 格式错误，请重新输入：%s"
	// TxtAskUA 用于两步式输入：告诉用户有现成档位可点，不必硬背 UA。
	TxtAskUA        = "请输入 UA（User-Agent）\n\n不知道填什么就发送 <code>/ua</code>，里面有网页 / App / 小程序等现成档位可直接点选。"
	TxtUAOKProfile  = "ua 已设置为 <b>%s</b>\n<code>%s</code>"
	TxtUAOKCustom   = "ua 已设置为自定义值\n<code>%s</code>\n\n这个值不属于任何预置档位，后续升级不会被自动替换。"
	TxtUABadFmt     = "ua 格式错误：%s"
	TxtUAUnknownArg = "无法识别 <code>/ua</code> 的参数：<code>%s</code>\n\n下面是一键可选的 UA 档位，点一下即可切换；也可以点「手动输入」粘贴自己的 UA。"

	// 转存通道
	TxtChannelOK         = "转存通道已切换为 <b>%s</b>\nappversion：<code>%s</code>\nUA：<code>%s</code>"
	TxtChannelUnknownArg = "无法识别 <code>/channel</code> 的参数：<code>%s</code>\n\n下面是一键可选的转存通道："

	// cid
	TxtAskTransferCID  = "请输入转存 cid"
	TxtAskOfflineCID   = "请输入离线保存 cid"
	TxtCIDBadFmt       = "cid 格式错误：请输入数字"
	TxtTransferCIDOK   = "设置转存 cid 成功：<code>%s</code>"
	TxtOfflineCIDOK    = "设置离线 cid 成功：<code>%s</code>"
	TxtCIDRootDenied   = "禁止使用根目录作为落盘目录"
	TxtCIDNotFound     = "未查询到该 cid，请确认目录存在：%s"
	TxtDirBrowserTitle = "📂 选择<b>%s</b>目录\n当前：<code>%s</code>"
	TxtDirBrowserPage  = "\n第 %d/%d 页 · 共 %d 个子目录"
	TxtDirListFailed   = "读取目录列表失败：%s\n可重试、或改为手动输入 cid"
	TxtCIDCheckWarn    = "\n⚠️ 目录校验未完成：%s\n已保存该 cid，如实际转存失败请重新设置"
	TxtNeedTransferCID = "请先设置转存 cid：发送 <code>/transfercid</code>"

	// 扫码登录
	TxtLoginPreparing = "正在向 115 申请二维码…"
	TxtLoginCaption   = "<b>115 扫码登录</b>\n\n设备：<code>%s</code>（槽位 %s）\n有效期：约 %d 秒\n\n请用 <b>115 手机 App</b> 扫描上图二维码，并在手机上点「确认登录」。\n\n发送 /cancel 可取消。"
	TxtLoginScanned   = "<b>115 扫码登录</b>\n\n✅ 已扫码，请在手机上点「确认登录」\n\n设备：<code>%s</code>（槽位 %s）"
	TxtLoginSuccess   = "<b>115 登录成功</b>\n\n账号：<b>%s</b>（UID %d）\n设备：<code>%s</code>（槽位 %s）\n\ncookie 与 ua 已保存，可直接使用 /d 与离线下载。"
	TxtLoginExpired   = "二维码已过期。\n\n发送 /login 重新获取。"
	TxtLoginCanceled  = "已取消扫码登录。"
	TxtLoginFailed    = "扫码登录失败：%s"
	TxtLoginBadApp    = "不支持的设备类型：<code>%s</code>\n\n可用类型：\n%s\n\n发送 <code>/login</code> 直接用默认设备（%s），或发送 <code>/login apps</code> 打开设备选择面板。"

	// 绑定
	TxtAskSetID    = "请输入要绑定的操作 id"
	TxtSetIDBadFmt = "id 格式错误：请输入数字"
	TxtSetIDOK     = "绑定成功：<code>%d</code>"
	TxtSetIDSame   = "id 已绑定：<code>%d</code>"

	// 离线下载
	TxtOfflineDisabled   = "当前添加磁力链、ed2k 功能已关闭！发送 /s 启用"
	TxtOfflineSubmitting = "识别到 %d 条链接，正在下发…"
	TxtOfflineResult     = "离线任务下发完成：成功 %d 条，失败 %d 条\n%s"
	TxtOfflineNoDetail   = "（115 未返回明细）"
	TxtOfflineUnknown    = "未知原因"
	TxtOfflineAllFailed  = "全部下发失败"
	TxtOfflineNeedCID    = "请先设置离线保存 cid：发送 <code>/setcid</code>"

	// 转存
	TxtTransferNoFile = "这条消息里没有可转存的视频 / 图片 / 文件。\n\n· <b>自动转存</b>：单独发 <code>/d</code> 是开关，开启后发过来的文件自动进 115\n· <b>单转一个</b>：<b>回复</b>那条消息再发 <code>/d</code>"

	// 欢迎语
	TxtWelcome             = "<b>115 机器人</b>\n\n下面是 115 设置菜单，点按钮即可操作，无需记命令。\n\n· 扫码登录：推荐，cookie 由本机签发不会失效\n· 转存：<code>/d</code> 是自动转存开关，开启后发文件即自动上传\n\n点最下方「📋 全部命令」可查看所有命令。"
	TxtTransferDisabled    = "当前转存功能未开启，发送 /s 启用"
	TxtTransferNoUploader  = "尚未登录 115，无法执行转存。\n\n请先发送 <code>/login</code> 扫码登录（推荐），或用 <code>/cookie</code> 手工设置 cookie。"
	TxtAutoTransferOn      = "自动转存已开启 ✅\n\n之后你发给机器人的<b>视频、图片、文件、音频</b>都会自动存进 115 的转存目录，不用再逐条回复 <code>/d</code>。\n\n多个文件会排队串行处理；再发一次 <code>/d</code> 即可关闭。"
	TxtAutoTransferOff     = "自动转存已关闭。\n\n想单转某个文件时，<b>回复</b>那条消息再发 <code>/d</code> 即可。"
	TxtTransferQueued      = "已加入转存队列：<b>%s</b>（%s）\n队列位置：第 %d 位"
	TxtTransferDuplicate   = "这个文件已经在队列里了，无需重复发送。"
	TxtTransferQueueFull   = "转存队列已满（上限 %d 个，当前 %d 个待处理），请等前面的任务跑完再发。\n\n发送 /state 可查看队列情况。"
	TxtTransferDownloading = "开始下载：<b>%s</b>（%s）"
	TxtTransferDownloaded  = "下载完成（%s），正在上传到 115，预计约 %s…"
	// 本地 Bot API 模式下文件已在磁盘上，不需要跨境下载
	TxtTransferLocalReady = "文件已在本地（本地 Bot API）：<b>%s</b>（%s）\n跳过下载，直接上传，预计约 %s…"
	// 单文件与批量共用：%2$s 由调用方拼（单文件「方式…耗时…平均」，批量「耗时 · 速度」）
	TxtTransferSuccess = "转存成功：<b>%s</b>\n%s\npick_code：<code>%s</code>"
)
