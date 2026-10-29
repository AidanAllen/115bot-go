package bot

import (
	"context"
	"errors"
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ghs/115bot-go/internal/p115"
	"github.com/ghs/115bot-go/internal/store"
)

// cmdMenu 输出 115 设置菜单（内容按发起者自己的账号渲染）。
func (a *App) cmdMenu(ac actor) {
	a.sendWithMenu(ac.ChatID, renderMenuText(a.userSnapshot(ac.UserID)), mainMenu())
}

// cmdAskCookie 进入 cookie 输入。
func (a *App) cmdAskCookie(ac actor) { a.startInput(ac, ActionCookie) }

// cmdUA 处理 /ua：可选档位优先，也保留手工输入。
//
//	/ua               打开档位面板（点按钮即切换）
//	/ua web           直接套用档位，标识见面板上的按钮
//	/ua Mozilla/5.0…  当作一条自定义 UA
//	/ua xxx           识别不了的短参数 → 打开面板并提示
//
// 之所以做点选：UA 是一长串不好手打的字符串，记不住也容易打错，
// 而打错的 UA 会让 115 把请求判成未知设备。
func (a *App) cmdUA(ac actor, args string) {
	arg := strings.TrimSpace(args)
	current := a.userUAProfileID(ac.UserID)
	if arg == "" {
		a.sendWithMenu(ac.ChatID, uaPickerText(current), uaPickerMenu(current))
		return
	}
	if p, ok := p115.LookupUAProfile(arg); ok {
		a.applyUAProfile(ac, p)
		return
	}
	// UA 原文必然带空格或以 Mozilla 开头；像 /ua help 这种单词不该被当成 UA 存进去。
	if strings.Contains(arg, " ") || strings.HasPrefix(strings.ToLower(arg), "mozilla") {
		a.applyUA(ac, arg)
		return
	}
	a.sendWithMenu(ac.ChatID,
		fmt.Sprintf(TxtUAUnknownArg, escapeHTML(arg)),
		uaPickerMenu(current))
}

// cmdAskUACustom 进入手工 UA 输入（档位面板的「手动输入」按钮）。
func (a *App) cmdAskUACustom(ac actor) { a.startInput(ac, ActionUA) }

// ---- UA 档位面板：全部做成按钮，用户不用记标识也不用抄 UA ----

// applyUAProfile 套用一个预置档位。
func (a *App) applyUAProfile(ac actor, p p115.UAProfile) {
	if err := a.saveUserUA(ac.UserID, p.UA); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}
	a.send(ac.ChatID, fmt.Sprintf(TxtUAOKProfile, escapeHTML(p.Name), escapeHTML(p.UA)))
}

// selectUAProfile 落库一个预置档位，成功返回该档位。供面板按钮复用（不自带交互输出）。
func (a *App) selectUAProfile(userID int64, id string) (p115.UAProfile, error) {
	p, ok := p115.LookupUAProfile(id)
	if !ok {
		return p115.UAProfile{}, fmt.Errorf("未知 UA 档位：%s", id)
	}
	if err := a.saveUserUA(userID, p.UA); err != nil {
		return p, err
	}
	return p, nil
}

// uaPickerText 生成 UA 面板说明。current 为空表示当前是自定义 UA。
func uaPickerText(current string) string {
	var b strings.Builder
	b.WriteString("<b>选择 User-Agent</b>\n\n")
	b.WriteString("115 会按 UA 判断请求来自哪个端，建议和你登录 115 的方式保持一致。\n")
	if p, ok := p115.LookupUAProfile(current); ok && current != "" {
		fmt.Fprintf(&b, "\n当前：<b>%s</b>", escapeHTML(p.Name))
	} else {
		b.WriteString("\n当前：自定义 UA")
	}
	b.WriteString("\n\n不确定就选「网页端 Chrome」；有自己抓包的 UA 就点「手动输入」粘贴。")
	return b.String()
}

// uaPickerMenu 生成 UA 档位按钮：每个档位一个按钮，当前档位打勾，另给手动输入 / 恢复默认 / 取消。
func uaPickerMenu(current string) tgbotapi.InlineKeyboardMarkup {
	btn := tgbotapi.NewInlineKeyboardButtonData
	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton

	for _, p := range p115.UAProfiles() {
		label := p.Name
		if p.ID == current {
			label = "✅ " + label
		}
		row = append(row, btn(label, cbUAPrefix+p.ID))
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn("✍️ 手动输入 UA", cbUACustom)))
	rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn("↩️ 恢复默认", cbUADefault), backButton(backToSettings)))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}

// cmdAskTransferCID 设置转存 cid。
// 带参数直接生效；不带参数打开目录浏览器（可点选，也可切换为手工输入）。
func (a *App) cmdAskTransferCID(ctx context.Context, ac actor, args string) {
	if args == "" {
		a.openDirBrowser(ctx, ac, dirTargetTransfer)
		return
	}
	a.applyCID(ctx, ac, store.KeyTransferCID, args)
}

// cmdAskOfflineCID 设置离线保存 cid，交互方式同上。
func (a *App) cmdAskOfflineCID(ctx context.Context, ac actor, args string) {
	if args == "" {
		a.openDirBrowser(ctx, ac, dirTargetOffline)
		return
	}
	a.applyCID(ctx, ac, store.KeyOfflineCID, args)
}

// applyCookie 校验格式 → 验证登录态 → 落库。
// 先本地校验再远端验证，避免无效内容覆盖掉原本可用的配置。
func (a *App) applyCookie(ctx context.Context, ac actor, raw string) {
	if err := p115.ValidateCookie(raw); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtCookieBadFmt, escapeHTML(strings.TrimPrefix(err.Error(), p115.ErrInvalidCookie.Error()+"："))))
		return
	}

	tmp := p115.New(raw, a.userUA(ac.UserID), a.cfg.HTTPTimeout)
	ctx, cancel := context.WithTimeout(ctx, a.cfg.HTTPTimeout)
	defer cancel()

	info, err := tmp.CheckLogin(ctx)
	if err != nil {
		if errors.Is(err, p115.ErrLoginRequired) {
			a.send(ac.ChatID, TxtLoginInvalid)
			return
		}
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(friendlyErr(err))))
		return
	}

	if err := a.st.SetUserConfig(ac.UserID, store.KeyCookie, strings.TrimSpace(raw)); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}
	a.markUserLoginOK(ac.UserID, info)
	a.send(ac.ChatID, fmt.Sprintf("%s\n\n账号：<b>%s</b>（UID %d）", TxtCookieOK, escapeHTML(info.UserName), info.UserID))
}

// applyUA 校验并保存 UA。UA 不影响登录态本身，因此不做远端验证，
// 但会在回复里说明它是否被识别为已知档位（自定义 UA 无法享受后续档位联动）。
func (a *App) applyUA(ac actor, raw string) {
	ua := strings.TrimSpace(raw)
	if err := p115.ValidateUA(ua); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtUABadFmt, escapeHTML(err.Error())))
		return
	}
	if err := a.st.SetUserConfig(ac.UserID, store.KeyUserAgent, ua); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}
	if p, ok := p115.MatchUAProfile(ua); ok {
		a.send(ac.ChatID, fmt.Sprintf(TxtUAOKProfile, escapeHTML(p.Name), escapeHTML(ua)))
		return
	}
	a.send(ac.ChatID, fmt.Sprintf(TxtUAOKCustom, escapeHTML(ua)))
}

// applyCID 校验 cid 格式并尽量校验目录存在性后落库。
func (a *App) applyCID(ctx context.Context, ac actor, key, raw string) {
	cid, err := p115.NormalizeCID(raw)
	if err != nil {
		a.send(ac.ChatID, TxtCIDBadFmt)
		return
	}
	if cid == "0" {
		a.send(ac.ChatID, TxtCIDRootDenied)
		return
	}

	// 目录存在性校验：失败不阻塞保存，但要给出提示
	warn := ""
	if a.userLoggedIn(ac.UserID) {
		c := a.userClient(ac.UserID)
		vctx, cancel := context.WithTimeout(ctx, a.cfg.HTTPTimeout)
		cat, cerr := c.CategoryGet(vctx, cid)
		cancel()
		switch {
		case cerr == nil:
			if name := cat.CategoryPath(); name != "" {
				warn = fmt.Sprintf("\n目录：<code>%s</code>", escapeHTML(name))
			}
		case errors.Is(cerr, p115.ErrLoginRequired):
			warn = "\n⚠️ " + TxtLoginInvalid + "，目录未校验"
		default:
			// 只有 115 明确判失败（目录不存在、参数错误）才拒绝保存。
			// 网络抖动、响应解析异常这类临时问题不应挡住用户，保存后给出警告即可
			// ——否则一次抖动就等于「这个目录设不了」，排查方向还会被带偏。
			var apiErr *p115.APIError
			if errors.As(cerr, &apiErr) {
				a.send(ac.ChatID, fmt.Sprintf(TxtCIDNotFound, cid)+"\n原因："+escapeHTML(apiErr.Message))
				return
			}
			warn = fmt.Sprintf(TxtCIDCheckWarn, escapeHTML(friendlyErr(cerr)))
		}
	}

	if err := a.st.SetUserConfig(ac.UserID, key, cid); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}

	tpl := TxtTransferCIDOK
	if key == store.KeyOfflineCID {
		tpl = TxtOfflineCIDOK
	}
	a.send(ac.ChatID, fmt.Sprintf(tpl, cid)+warn)
}

// cmdCheckLogin 主动检查登录状态并更新缓存。
func (a *App) cmdCheckLogin(ctx context.Context, ac actor) {
	c, ok := a.requireClient(ac)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, a.cfg.HTTPTimeout)
	defer cancel()

	info, err := c.CheckLogin(ctx)
	if err != nil {
		if errors.Is(err, p115.ErrLoginRequired) {
			a.markUserLoginInvalid(ac.UserID)
			a.send(ac.ChatID, TxtLoginInvalid)
			return
		}
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(friendlyErr(err))))
		return
	}
	a.markUserLoginOK(ac.UserID, info)
	a.send(ac.ChatID, fmt.Sprintf(TxtLoginOK, escapeHTML(info.UserName), info.UserID))
}

// cmdInitAccount 账号初始化：校验登录、确认根目录可访问、校验已配置的 cid。
func (a *App) cmdInitAccount(ctx context.Context, ac actor) {
	c, ok := a.requireClient(ac)
	if !ok {
		return
	}

	ctx, cancel := context.WithTimeout(ctx, a.cfg.HTTPTimeout*2)
	defer cancel()

	var b strings.Builder
	b.WriteString("<b>账号初始化</b>\n\n")

	// 1. 登录态
	info, err := c.CheckLogin(ctx)
	if err != nil {
		if errors.Is(err, p115.ErrLoginRequired) {
			a.markUserLoginInvalid(ac.UserID)
			b.WriteString("① 登录态：❌ " + TxtLoginInvalid + "\n")
			b.WriteString("\n请发送 /cookie 重新设置")
			a.send(ac.ChatID, b.String())
			return
		}
		b.WriteString("① 登录态：❌ " + escapeHTML(friendlyErr(err)) + "\n")
		a.send(ac.ChatID, b.String())
		return
	}
	a.markUserLoginOK(ac.UserID, info)
	fmt.Fprintf(&b, "① 登录态：✅ <b>%s</b>（UID %d）\n", escapeHTML(info.UserName), info.UserID)

	// 2. 根目录访问
	if _, err := c.CategoryGet(ctx, "0"); err != nil {
		fmt.Fprintf(&b, "② 根目录访问：❌ %s\n", escapeHTML(friendlyErr(err)))
	} else {
		b.WriteString("② 根目录访问：✅\n")
	}

	// 3. 已配置目录校验
	checkCID := func(label, cid string) {
		if cid == "" {
			fmt.Fprintf(&b, "%s：未设置 ⚠️\n", label)
			return
		}
		cat, err := c.CategoryGet(ctx, cid)
		if err != nil {
			fmt.Fprintf(&b, "%s：<code>%s</code> 校验失败 ❌（%s）\n", label, cid, escapeHTML(friendlyErr(err)))
			return
		}
		name := cat.CategoryPath()
		if name == "" {
			name = "未知目录"
		}
		fmt.Fprintf(&b, "%s：<code>%s</code> ✅ %s\n", label, cid, escapeHTML(name))
	}
	checkCID("③ 转存目录", a.userTransferCID(ac.UserID))
	checkCID("④ 离线目录", a.userOfflineCID(ac.UserID))

	// 4. 转存通道
	up := a.userUploader(ac.UserID)
	b.WriteString("⑤ 转存通道：" + up.Name())
	if up.Available() {
		b.WriteString(" ✅\n")
	} else {
		b.WriteString(" ⛔（/d 转存不可用）\n")
	}

	b.WriteString("\n初始化完成。")
	a.send(ac.ChatID, b.String())
}
