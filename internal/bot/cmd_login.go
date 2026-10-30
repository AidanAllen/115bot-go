package bot

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/AidanAllen/115bot-go/internal/p115"
	"github.com/AidanAllen/115bot-go/internal/store"
)

const (
	// loginWait 是等待用户扫码确认的最长时间。
	// 115 的二维码本身约 3 分钟有效，这里略放宽，让「过期」由 115 判定而不是本地先超时。
	loginWait = 4 * time.Minute
	// loginPollInterval 是状态轮询间隔。
	loginPollInterval = 2 * time.Second
	// loginImageScale 是二维码放大倍数。官方图仅 205×205，放手机上偏小不易对焦。
	loginImageScale = 3
)

// qrLoginState 是一次进行中的扫码登录。
type qrLoginState struct {
	cancel context.CancelFunc
	app    string
	slot   string
	msgID  int
	token  *p115.QRToken
	// userID 是这次登录归属的用户：cookie 最后落到他的名下。
	// 按用户而不是会话登记，群聊里多个人同时扫码才不会互相覆盖。
	userID int64
}

// cmdLogin 发起扫码登录。
//
//	/login            使用默认设备类型（二维码下方有「换个设备」按钮）
//	/login alipaymini 指定设备类型
//	/login apps       打开设备选择面板
//
// 之所以把扫码做进 TG：cookie 由 115 绑定签发时的出口 IP 与设备，
// 必须在 bot 所在机器上完成登录，否则拿到的是「别的机器的会话」，部署后立刻失效。
func (a *App) cmdLogin(ctx context.Context, ac actor, args string) {
	arg := strings.ToLower(strings.TrimSpace(args))
	if arg == "apps" || arg == "list" {
		a.sendWithMenu(ac.ChatID, appPickerText(false), appPickerMenu(false))
		return
	}

	app := arg
	if app == "" {
		app = p115.DefaultQRApp
	}
	if _, ok := p115.QRAvailableApps[app]; !ok {
		a.send(ac.ChatID, fmt.Sprintf(TxtLoginBadApp, escapeHTML(app), renderAppList(), p115.DefaultQRApp))
		return
	}

	// 同一用户重复发起时，先终止上一条，避免两个 goroutine 抢着改同一条消息。
	a.stopLogin(ac.UserID)
	a.send(ac.ChatID, TxtLoginPreparing)

	qr := p115.NewQRLoginWithApp(a.cfg.HTTPTimeout, app)
	tok, err := qr.Token(ctx)
	if err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtLoginFailed, escapeHTML(friendlyErr(err))))
		return
	}

	img, err := qr.Image(ctx, tok.UID)
	if err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtLoginFailed, escapeHTML(friendlyErr(err))))
		return
	}
	if big, uerr := p115.UpscalePNG(img, loginImageScale); uerr == nil {
		img = big
	} else {
		a.log.Debug("二维码放大失败，使用原图", "err", uerr)
	}

	slot := p115.QRSlot[qr.App()]
	ttl := int(time.Until(tok.ExpiresAt).Seconds())
	if ttl <= 0 {
		ttl = 180
	}

	photo := tgbotapi.NewPhoto(ac.ChatID, tgbotapi.FileBytes{Name: "115-qr.png", Bytes: img})
	photo.Caption = fmt.Sprintf(TxtLoginCaption, qr.App(), slot, ttl)
	photo.ParseMode = "HTML"
	// 挂上按钮：换设备 / 取消，用户不必记住 /login 的参数写法
	photo.ReplyMarkup = qrActiveMenu()

	msg, err := a.api.Send(photo)
	if err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtLoginFailed, escapeHTML(err.Error())))
		return
	}

	lctx, cancel := context.WithTimeout(ctx, loginWait)
	st := &qrLoginState{cancel: cancel, app: qr.App(), slot: slot, msgID: msg.MessageID, token: tok, userID: ac.UserID}
	a.setLogin(ac.UserID, st)

	// 轮询必须独立于本次 update 的处理流程，否则 handler 返回就会中断等待。
	go a.waitLogin(lctx, ac, st, qr, tok)
}

// waitLogin 轮询扫码状态，成功后校验并落到**发起者自己**名下。
func (a *App) waitLogin(ctx context.Context, ac actor, st *qrLoginState, qr *p115.QRLogin, tok *p115.QRToken) {
	defer func() {
		st.cancel()
		a.clearLogin(st.userID, st)
	}()

	scanned := false
	cookie, _, err := qr.WaitForLogin(ctx, tok, loginPollInterval, func(s p115.QRStatus) {
		// 只在首次进入「已扫码」时刷新一次说明，避免反复编辑消息。
		if s == p115.QRScanned && !scanned {
			scanned = true
			a.editCaption(ac.ChatID, st.msgID, fmt.Sprintf(TxtLoginScanned, st.app, st.slot))
		}
	})
	// 流程结束（无论成败）后收起「换设备 / 取消」按钮，避免留下点不动的旧键盘。
	finish := func(text string) {
		a.editCaption(ac.ChatID, st.msgID, text)
		a.clearMenu(ac.ChatID, st.msgID)
	}
	if err != nil {
		finish(loginErrText(err))
		a.log.Info("扫码登录未完成", "user_id", st.userID, "app", st.app, "err", err)
		return
	}

	// 拿到 cookie 不等于可用，必须实调一次鉴权接口确认。
	//
	// UA 用扫码时实际使用的那一个（qandroid / alipaymini 等无专用 UA 的设备
	// 就是网页 UA，而 ios 系列各有专用 UA）：115 会校验 UA 与会话的一致性，
	// 用别的 UA 保存会埋雷。
	ua := qr.UA()
	cli := p115.New(cookie, ua, a.cfg.HTTPTimeout)
	vctx, vcancel := context.WithTimeout(context.Background(), a.cfg.HTTPTimeout)
	defer vcancel()

	info, err := cli.CheckLogin(vctx)
	if err != nil {
		finish(fmt.Sprintf(TxtLoginFailed, escapeHTML(friendlyErr(err))))
		a.log.Warn("扫码成功但登录校验失败", "user_id", st.userID, "err", err)
		return
	}

	if err := a.st.SetUserConfig(st.userID, store.KeyCookie, cookie); err != nil {
		finish(fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}
	// UA 与会话保持一致，否则后续请求可能被判定为异常设备。
	_ = a.st.SetUserConfig(st.userID, store.KeyUserAgent, ua)
	a.markUserLoginOK(st.userID, info)

	finish(fmt.Sprintf(TxtLoginSuccess,
		escapeHTML(info.UserName), info.UserID, st.app, st.slot))
	a.log.Info("扫码登录成功", "user_id", st.userID, "app", st.app, "user", info.UserName, "uid", info.UserID)
}

// loginErrText 把等待过程中的错误翻译成用户能理解的说明。
func loginErrText(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded), errors.Is(err, context.Canceled):
		return TxtLoginExpired
	}
	msg := err.Error()
	switch {
	case strings.Contains(msg, "过期"):
		return TxtLoginExpired
	case strings.Contains(msg, "取消"):
		return TxtLoginCanceled
	default:
		return fmt.Sprintf(TxtLoginFailed, escapeHTML(msg))
	}
}

// editCaption 就地更新图片说明，避免刷屏。
func (a *App) editCaption(chatID int64, messageID int, caption string) {
	edit := tgbotapi.NewEditMessageCaption(chatID, messageID, caption)
	edit.ParseMode = "HTML"
	if _, err := a.api.Request(edit); err != nil {
		a.log.Debug("更新二维码说明失败", "err", err)
	}
}

// ---- 进行中登录的登记与取消 ----

func (a *App) setLogin(userID int64, st *qrLoginState) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	if a.logins == nil {
		a.logins = make(map[int64]*qrLoginState)
	}
	// 理论上已在 cmdLogin 里 stopLogin 过，这里再兜一层，防止竞态下遗留 goroutine。
	if old, ok := a.logins[userID]; ok && old != st {
		old.cancel()
	}
	a.logins[userID] = st
}

// clearLogin 只在仍是同一次登录时才删除，避免误删后续新建的流程。
func (a *App) clearLogin(userID int64, st *qrLoginState) {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	if cur, ok := a.logins[userID]; ok && cur == st {
		delete(a.logins, userID)
	}
}

// stopLogin 取消该用户的登录流程，返回是否确实有进行中的登录。
func (a *App) stopLogin(userID int64) bool {
	a.loginMu.Lock()
	defer a.loginMu.Unlock()
	st, ok := a.logins[userID]
	if !ok {
		return false
	}
	st.cancel()
	delete(a.logins, userID)
	return true
}

// ---- 设备选择：全部做成按钮，用户不需要记住设备名 ----

// qrAppShortName 是按钮上显示的短名（官方全名太长，按钮放不下）。
var qrAppShortName = map[string]string{
	"alipaymini": "支付宝小程序",
	"wechatmini": "微信小程序",
	"qandroid":   "115管理·安卓",
	"qios":       "115管理·苹果",
	"qipad":      "115管理·平板",
	"tv":         "安卓电视",
	"apple_tv":   "苹果电视",
	"os_windows": "Windows 端",
	"os_mac":     "macOS 端",
	"os_linux":   "Linux 端",
	"harmony":    "鸿蒙端",
	"web":        "网页端",
	"ios":        "115生活·苹果",
	"115ios":     "115·苹果",
	"android":    "115生活·安卓",
	"115android": "115·安卓",
	"ipad":       "115生活·平板",
	"115ipad":    "115·平板",
}

// qrRecommendedApps 是推荐设备：与手机上的 115 App 不同槽位，不会互相踢下线。
var qrRecommendedApps = []string{
	"alipaymini", "wechatmini", "qandroid", "qios", "qipad", "tv", "apple_tv",
	"os_windows", "os_mac", "os_linux", "harmony",
}

// qrRiskyApps 是 115 App 本体 / 网页端：与你自己的登录同槽位，会把对方踢下线。
var qrRiskyApps = []string{"web", "ios", "115ios", "android", "115android", "ipad", "115ipad"}

// qrActiveMenu 是二维码下方的按钮：换设备、取消。
func qrActiveMenu() tgbotapi.InlineKeyboardMarkup {
	btn := tgbotapi.NewInlineKeyboardButtonData
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(btn("换个设备登录", cbLoginApps), btn("取消登录", cbCancel)),
	)
}

// appPickerText 生成设备选择面板的说明。
func appPickerText(all bool) string {
	var b strings.Builder
	b.WriteString("<b>选择登录设备</b>\n\n")
	b.WriteString("不同设备占用 115 的不同登录槽位。选<b>推荐</b>里的设备，")
	b.WriteString("不会把你手机 / 电脑上的 115 挤下线。\n")
	if all {
		b.WriteString("\n下方 ⚠️ 的设备与 115 App 本体或网页登录<b>同槽位</b>，")
		b.WriteString("登录后会互相踢下线，仅在你确实要用它时选择。")
	}
	return b.String()
}

// appPickerMenu 生成设备按钮，两列排布。
// all 为 false 时只列推荐设备，并在末尾给一个「显示会互踢的设备」入口。
func appPickerMenu(all bool) tgbotapi.InlineKeyboardMarkup {
	btn := tgbotapi.NewInlineKeyboardButtonData
	var rows [][]tgbotapi.InlineKeyboardButton

	appendRows := func(names []string, mark string) {
		var row []tgbotapi.InlineKeyboardButton
		for _, n := range names {
			label := qrAppShortName[n]
			if label == "" {
				label = n
			}
			if mark != "" {
				label = mark + label
			}
			row = append(row, btn(label, cbLoginAppPrefix+n))
			if len(row) == 2 {
				rows = append(rows, row)
				row = nil
			}
		}
		if len(row) > 0 {
			rows = append(rows, row)
		}
	}

	appendRows(qrRecommendedApps, "")
	if all {
		appendRows(qrRiskyApps, "⚠️ ")
	} else {
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn("⚠️ 显示会互踢的设备", cbLoginAll)))
	}
	rows = append(rows, backRow(backToSettings))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// renderAppList 渲染可选设备列表。
func renderAppList() string {
	keys := make([]string, 0, len(p115.QRAvailableApps))
	for k := range p115.QRAvailableApps {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		note := ""
		switch k {
		case p115.DefaultQRApp:
			note = "  ← 默认"
		case "web":
			note = "  ⚠️ 你在浏览器登录会顶掉 bot"
		case "ios", "android", "115ios", "115android":
			note = "  ⚠️ 与你手机 App 互踢，勿选"
		case "alipaymini", "wechatmini":
			note = "  ⚠️ 你用同名小程序会互踢"
		}
		fmt.Fprintf(&b, "<code>%s</code>  %s [%s]%s\n",
			k, p115.QRAvailableApps[k], p115.QRSlot[k], note)
	}
	return strings.TrimRight(b.String(), "\n")
}
