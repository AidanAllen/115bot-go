package bot

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/AidanAllen/115bot-go/internal/p115"
	"github.com/AidanAllen/115bot-go/internal/store"
)

// cmdChannel 处理 /channel：转存通道可选可切换。
//
//	/channel        打开通道面板（点按钮即切换）
//	/channel web    直接切换，标识见面板上的按钮
//
// 通道决定「上传时以哪个端的身份发起请求」：
// 一套 appversion + UA 的组合，两者必须成对（appversion 会参与签名计算）。
func (a *App) cmdChannel(ac actor, args string) {
	arg := strings.TrimSpace(args)
	current := a.userChannelID(ac.UserID)
	if arg == "" {
		a.sendWithMenu(ac.ChatID, channelPickerText(current), channelPickerMenu(current))
		return
	}
	ch, ok := p115.LookupUploadChannel(arg)
	if !ok {
		a.sendWithMenu(ac.ChatID,
			fmt.Sprintf(TxtChannelUnknownArg, escapeHTML(arg)),
			channelPickerMenu(current))
		return
	}
	a.applyChannel(ac, ch)
}

// applyChannel 保存并回复。
func (a *App) applyChannel(ac actor, ch p115.UploadChannel) {
	if err := a.st.SetUserConfig(ac.UserID, store.KeyUploadChannel, ch.ID); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}
	a.send(ac.ChatID, fmt.Sprintf(TxtChannelOK, escapeHTML(ch.Name), escapeHTML(ch.AppVersion), escapeHTML(ch.UA)))
}

// selectChannel 落库一个通道，返回该通道。供面板按钮复用（不自带交互输出）。
func (a *App) selectChannel(userID int64, id string) (p115.UploadChannel, error) {
	ch, ok := p115.LookupUploadChannel(id)
	if !ok {
		return p115.UploadChannel{}, fmt.Errorf("未知转存通道：%s", id)
	}
	if err := a.st.SetUserConfig(userID, store.KeyUploadChannel, ch.ID); err != nil {
		return ch, err
	}
	return ch, nil
}

// channelPickerText 生成通道面板说明。
func channelPickerText(current string) string {
	var b strings.Builder
	b.WriteString("<b>选择转存通道</b>\n\n")
	b.WriteString("通道 = 上传时向 115 声明的<b>客户端身份</b>（appversion + UA），")
	b.WriteString("两者必须成对，因为版本串会参与签名计算。\n")
	if p, ok := p115.LookupUploadChannel(current); ok {
		fmt.Fprintf(&b, "\n当前：<b>%s</b>\nappversion：<code>%s</code>", escapeHTML(p.Name), escapeHTML(p.AppVersion))
	} else {
		b.WriteString("\n当前：未知通道 ⚠️")
	}
	b.WriteString("\n\n不确定就选「115 网页通道」（默认，也是验证最充分的一条）。")
	return b.String()
}

// channelPickerMenu 生成通道按钮：当前通道打勾，另给恢复默认与取消。
func channelPickerMenu(current string) tgbotapi.InlineKeyboardMarkup {
	btn := tgbotapi.NewInlineKeyboardButtonData
	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton

	for _, ch := range p115.UploadChannels() {
		label := ch.Name
		if ch.ID == current {
			label = "✅ " + label
		}
		row = append(row, btn(label, cbChannelPrefix+ch.ID))
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}

	rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn("↩️ 恢复默认", cbChannelDefault), backButton(backToSettings)))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}
