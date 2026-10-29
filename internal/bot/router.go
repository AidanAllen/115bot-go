package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/AidanAllen/115bot-go/internal/p115"
	"github.com/AidanAllen/115bot-go/internal/store"
)

// handleMessage 处理普通消息与命令。
func (a *App) handleMessage(ctx context.Context, m *tgbotapi.Message) {
	if m.From == nil {
		return
	}
	ac := actorOf(m)

	// 命令路由
	if m.IsCommand() {
		a.routeCommand(ctx, m)
		return
	}

	// 待输入状态优先（两步式输入）。Take 是原子消费，
	// 并发到达的第二条消息会拿不到状态，从而不会被当成输入处理。
	// 输入态按用户登记：群聊里别人发消息顶不掉你的输入。
	if act, ok := a.sessions.Take(ac.UserID); ok {
		a.handlePendingInput(ctx, ac, act, strings.TrimSpace(m.Text))
		return
	}

	// 自动转存：开关打开后，发过来的每个媒体文件直接排进队列，
	// 不用再逐条回复 /d。放在磁力链之前，一条「视频 + 磁力链说明」两者都会处理。
	if a.userAutoTransfer(ac.UserID) && hasMedia(m) {
		a.enqueueTransfer(ctx, ac, m, "")
	}

	// 纯文本消息：尝试提取磁力链 / ed2k
	a.handleAutoLinks(ctx, m)
}

// routeCommand 分发斜杠命令。
func (a *App) routeCommand(ctx context.Context, m *tgbotapi.Message) {
	a.runCommand(ctx, actorOf(m),
		strings.ToLower(m.Command()),
		strings.TrimSpace(m.CommandArguments()),
		m)
}

// runCommand 执行一条命令。
//
// 文本命令与 /help 里的按钮共用这条路径，避免「菜单能点、实际没实现」的分裂。
// reply 为触发命令的那条消息；由按钮触发时为 nil（拿不到回复目标）。
//
// 任何 Telegram 用户都可以使用本机器人：115 凭据按用户隔离，各用各的账号。
// 拦人的不是身份，而是「这个用户还没登录 115」——由各命令自行判断并引导 /login。
func (a *App) runCommand(ctx context.Context, ac actor, cmd, args string, reply *tgbotapi.Message) {
	switch cmd {
	case "start":
		a.sendWithMenu(ac.ChatID, TxtWelcome, mainMenu())

	case "help":
		a.sendWithMenu(ac.ChatID, helpText(), helpMenu())

	case "s":
		a.cmdToggle(ac)

	case "v", "version":
		a.cmdVersion(ac.ChatID)

	case "state":
		a.cmdState(ac)

	case "set115", "set":
		a.cmdMenu(ac)

	case "login", "qrlogin":
		a.cmdLogin(ctx, ac, args)

	case "cookie":
		a.cmdAskCookie(ac)

	case "ua":
		a.cmdUA(ac, args)

	case "channel":
		a.cmdChannel(ac, args)

	case "transfercid":
		a.cmdAskTransferCID(ctx, ac, args)

	case "setcid":
		a.cmdAskOfflineCID(ctx, ac, args)

	case "checklogin":
		a.cmdCheckLogin(ctx, ac)

	case "initaccount":
		a.cmdInitAccount(ctx, ac)

	case "d":
		// 回复了某条消息 → 转存那一条（args 可作新文件名）；
		// 单独发 /d 或点菜单按钮 → 切换「自动转存」开关。
		if reply != nil && reply.ReplyToMessage != nil {
			a.enqueueTransfer(ctx, ac, reply.ReplyToMessage, args)
			return
		}
		a.cmdToggleAutoTransfer(ac)

	case "cancel":
		a.cmdCancel(ac)

	case "id":
		a.cmdID(ac)

	case "setid":
		a.cmdSetID(ctx, ac, args)

	default:
		a.sendWithMenu(ac.ChatID, fmt.Sprintf(TxtUnknownCommandFmt, escapeHTML(cmd)), helpMenu())
	}
}

// cmdCancel 取消进行中的扫码登录与待输入状态。
func (a *App) cmdCancel(ac actor) {
	canceledLogin := a.stopLogin(ac.UserID)
	_, hadInput := a.sessions.Take(ac.UserID)

	switch {
	case canceledLogin:
		a.send(ac.ChatID, TxtLoginCanceled)
	case hadInput:
		a.send(ac.ChatID, TxtCanceled)
	default:
		a.send(ac.ChatID, TxtNothingToCancel)
	}
}

// handleCallback 处理内联键盘回调。
//
// 面板上的按钮作用于「点按钮的人」而不是「消息的主人」：
// 群聊里 A 打开菜单、B 点了「设置 ua」，改的是 B 自己的配置。
func (a *App) handleCallback(ctx context.Context, cb *tgbotapi.CallbackQuery) {
	userID := cb.From.ID
	chatID := cb.Message.Chat.ID
	msgID := cb.Message.MessageID
	data := cb.Data
	ac := actor{UserID: userID, ChatID: chatID}

	// 绑定 owner 走 cmdSetID 自身的规则（未绑定时谁先点谁成为 owner）
	if data == cbBindMe {
		a.answerCallback(cb.ID, "")
		a.cmdSetID(ctx, ac, strconv.FormatInt(userID, 10))
		return
	}

	// 点按钮执行斜杠命令（/help、主菜单下方的命令按钮）
	if name, ok := strings.CutPrefix(data, cmdPrefix); ok {
		a.answerCallback(cb.ID, "")
		a.runCommand(ctx, ac, name, "", nil)
		return
	}

	// 返回上一层：就地退回目标面板。
	// 顺手丢弃待输入状态——从输入提示里点返回，意思就是放弃这次输入，
	// 否则状态会一直挂着，下一条正常消息会被误当成输入内容。
	if dest, ok := strings.CutPrefix(data, cbBackPrefix); ok {
		a.answerCallback(cb.ID, "")
		a.sessions.Take(ac.UserID)
		text, kb, ok2 := a.renderPanel(ac.UserID, dest)
		if !ok2 {
			text, kb = renderMenuText(a.userSnapshot(ac.UserID)), mainMenu()
		}
		a.editMenu(chatID, msgID, text, kb)
		return
	}

	// 目录浏览器：点选落盘目录
	if dc, ok := parseDirCallback(data); ok {
		a.handleDirCallback(ctx, ac, msgID, cb.ID, dc)
		return
	}

	// 选择扫码登录设备
	if app, ok := strings.CutPrefix(data, cbLoginAppPrefix); ok {
		a.answerCallback(cb.ID, "正在申请二维码…")
		a.cmdLogin(ctx, ac, app)
		return
	}

	// 选择 UA 档位：落库后把面板就地更新，避免刷屏
	if id, ok := strings.CutPrefix(data, cbUAPrefix); ok {
		p, err := a.selectUAProfile(ac.UserID, id)
		if err != nil {
			a.answerCallback(cb.ID, "切换失败")
			a.send(chatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
			return
		}
		a.answerCallback(cb.ID, "已切换为 "+p.Name)
		a.editMenu(chatID, msgID, uaPickerText(p.ID), uaPickerMenu(p.ID))
		return
	}

	// 选择转存通道：同样就地更新面板
	if id, ok := strings.CutPrefix(data, cbChannelPrefix); ok {
		ch, err := a.selectChannel(ac.UserID, id)
		if err != nil {
			a.answerCallback(cb.ID, "切换失败")
			a.send(chatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
			return
		}
		a.answerCallback(cb.ID, "已切换为 "+ch.Name)
		a.editMenu(chatID, msgID, channelPickerText(ch.ID), channelPickerMenu(ch.ID))
		return
	}

	switch data {
	case cbMain:
		a.answerCallback(cb.ID, "")
		a.editMenu(chatID, msgID, renderMenuText(a.userSnapshot(ac.UserID)), mainMenu())

	case cbLogin:
		a.answerCallback(cb.ID, "正在申请二维码…")
		a.cmdLogin(ctx, ac, "")

	case cbLoginApps:
		a.answerCallback(cb.ID, "")
		a.sendWithMenu(chatID, appPickerText(false), appPickerMenu(false))

	case cbLoginAll:
		a.answerCallback(cb.ID, "")
		a.editMenu(chatID, msgID, appPickerText(true), appPickerMenu(true))

	case cbCookie:
		a.answerCallback(cb.ID, "")
		a.startInput(ac, ActionCookie)

	case cbUA:
		a.answerCallback(cb.ID, "")
		cur := a.userUAProfileID(ac.UserID)
		a.editMenu(chatID, msgID, uaPickerText(cur), uaPickerMenu(cur))

	case cbUACustom:
		a.answerCallback(cb.ID, "")
		a.cmdAskUACustom(ac)

	case cbUADefault:
		if p, err := a.selectUAProfile(ac.UserID, p115.DefaultUAProfileID); err != nil {
			a.answerCallback(cb.ID, "恢复默认失败")
			a.send(chatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
			return
		} else {
			a.answerCallback(cb.ID, "已恢复为 "+p.Name)
			a.editMenu(chatID, msgID, uaPickerText(p.ID), uaPickerMenu(p.ID))
		}

	case cbChannel:
		a.answerCallback(cb.ID, "")
		cur := a.userChannelID(ac.UserID)
		a.editMenu(chatID, msgID, channelPickerText(cur), channelPickerMenu(cur))

	case cbChannelDefault:
		if ch, err := a.selectChannel(ac.UserID, p115.DefaultUploadChannelID); err != nil {
			a.answerCallback(cb.ID, "恢复默认失败")
			a.send(chatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
			return
		} else {
			a.answerCallback(cb.ID, "已恢复为 "+ch.Name)
			a.editMenu(chatID, msgID, channelPickerText(ch.ID), channelPickerMenu(ch.ID))
		}

	case cbTransferCID:
		a.answerCallback(cb.ID, "")
		a.openDirBrowser(ctx, ac, dirTargetTransfer)

	case cbOfflineCID:
		a.answerCallback(cb.ID, "")
		a.openDirBrowser(ctx, ac, dirTargetOffline)

	case cbCheckLogin:
		a.answerCallback(cb.ID, "正在检查…")
		a.cmdCheckLogin(ctx, ac)

	case cbInitAccount:
		a.answerCallback(cb.ID, "正在初始化…")
		a.cmdInitAccount(ctx, ac)

	case cbToggle:
		a.answerCallback(cb.ID, "")
		a.cmdToggle(ac)
		a.editMenu(chatID, msgID, renderMenuText(a.userSnapshot(ac.UserID)), mainMenu())

	case cbAutoTransfer:
		a.answerCallback(cb.ID, "")
		a.cmdToggleAutoTransfer(ac)
		a.editMenu(chatID, msgID, renderMenuText(a.userSnapshot(ac.UserID)), mainMenu())

	case cbState:
		a.answerCallback(cb.ID, "")
		a.cmdState(ac)

	case cbHelp:
		a.answerCallback(cb.ID, "")
		a.editMenu(chatID, msgID, helpText(), helpMenu())

	case cbCancel:
		a.answerCallback(cb.ID, "")
		a.cmdCancel(ac)

	default:
		a.answerCallback(cb.ID, "未知操作")
	}
}

// startInput 进入两步式输入：记录状态并提示用户输入。
// 提示语下方带「取消」按钮，用户不必记住 /cancel。
func (a *App) startInput(ac actor, act Action) {
	a.sessions.Set(ac.UserID, act)
	a.sendWithMenu(ac.ChatID, actionPrompt(act), cancelMenu())
}

// handlePendingInput 处理两步式输入的第二条消息。
func (a *App) handlePendingInput(ctx context.Context, ac actor, act Action, text string) {
	if text == "" {
		a.send(ac.ChatID, TxtInputEmpty)
		return
	}
	switch act {
	case ActionCookie:
		a.applyCookie(ctx, ac, text)
	case ActionUA:
		a.applyUA(ac, text)
	case ActionTransferCID:
		a.applyCID(ctx, ac, store.KeyTransferCID, text)
	case ActionOfflineCID:
		a.applyCID(ctx, ac, store.KeyOfflineCID, text)
	case ActionSetID:
		a.applySetID(ac, text)
	default:
		a.send(ac.ChatID, TxtUnknownAction)
	}
}
