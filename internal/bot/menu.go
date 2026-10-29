package bot

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// callback data 常量。
const (
	cbMain        = "m:main"
	cbLogin       = "act:login"
	cbLoginApps   = "act:loginapps"
	cbLoginAll    = "act:loginall"
	cbCookie      = "act:cookie"
	cbUA          = "act:ua"
	cbTransferCID = "act:transfercid"
	cbOfflineCID  = "act:offlinecid"
	cbCheckLogin  = "act:checklogin"
	cbInitAccount = "act:init"
	cbToggle      = "act:toggle"
	// cbAutoTransfer 切换「收到文件自动转存」，与 /d 等价
	cbAutoTransfer = "act:autotransfer"
	cbState        = "act:state"
	cbHelp         = "act:help"
	cbCancel       = "act:cancel"
	cbBindMe       = "act:bindme"
	// UA 的两个附加操作：手动输入（走两步式输入）、恢复默认档位
	cbUACustom  = "act:uacustom"
	cbUADefault = "act:uadefault"
	// 转存通道：打开面板、恢复默认
	cbChannel        = "act:channel"
	cbChannelDefault = "act:channeldefault"
)

// cbLoginAppPrefix 是扫码登录设备选择的回调前缀，形如 login:alipaymini。
const cbLoginAppPrefix = "login:"

// cbUAPrefix 是 UA 档位选择的回调前缀，形如 ua:alipaymini。
const cbUAPrefix = "ua:"

// cbChannelPrefix 是转存通道选择的回调前缀，形如 chan:app。
const cbChannelPrefix = "chan:"

// accountSnapshot 用于渲染设置菜单顶部的状态摘要。
type accountSnapshot struct {
	Enabled       bool
	HasCookie     bool
	UALabel       string // 当前 UA 的展示名（档位名 / 自定义）；空表示完全没配 UA
	UAExplicit    bool   // 是否由用户显式写入过（false = 走内置默认）
	LoginOK       bool
	Nickname      string
	TransferCID   string
	OfflineCID    string
	UploaderName  string
	UploaderReady bool
	AutoTransfer  bool // 收到文件是否自动转存（/d 开关）
}

// renderMenuText 生成设置菜单的正文。
func renderMenuText(s accountSnapshot) string {
	var b strings.Builder
	b.WriteString("<b>115 设置</b>\n\n")

	b.WriteString("功能开关：")
	if s.Enabled {
		b.WriteString("已开启 ✅\n")
	} else {
		b.WriteString("已关闭 ⛔\n")
	}

	b.WriteString("cookie：")
	if s.HasCookie {
		if s.LoginOK {
			if s.Nickname != "" {
				fmt.Fprintf(&b, "有效 ✅（%s）\n", escapeHTML(s.Nickname))
			} else {
				b.WriteString("有效 ✅\n")
			}
		} else {
			b.WriteString("已设置，待验证 ⚠️\n")
		}
	} else {
		b.WriteString("未设置 ❌\n")
	}

	b.WriteString("ua：")
	switch {
	case s.UALabel == "":
		b.WriteString("未设置 ❌\n")
	case !s.UAExplicit:
		fmt.Fprintf(&b, "%s（内置默认）\n", escapeHTML(s.UALabel))
	default:
		fmt.Fprintf(&b, "%s ✅\n", escapeHTML(s.UALabel))
	}

	b.WriteString("转存 cid：")
	if s.TransferCID != "" {
		fmt.Fprintf(&b, "<code>%s</code>\n", s.TransferCID)
	} else {
		b.WriteString("未设置 ❌\n")
	}

	b.WriteString("离线 cid：")
	if s.OfflineCID != "" {
		fmt.Fprintf(&b, "<code>%s</code>\n", s.OfflineCID)
	} else {
		b.WriteString("未设置 ❌\n")
	}

	b.WriteString("转存通道：")
	b.WriteString(s.UploaderName)
	if s.UploaderReady {
		b.WriteString(" ✅\n")
	} else {
		b.WriteString(" ⛔\n")
	}

	b.WriteString("自动转存：")
	if s.AutoTransfer {
		b.WriteString("已开启 ✅（发文件即自动进 115）\n")
	} else {
		b.WriteString("已关闭（回复文件发 /d 单转）\n")
	}
	return b.String()
}

// mainMenu 生成 115 设置主菜单。
func mainMenu() tgbotapi.InlineKeyboardMarkup {
	btn := tgbotapi.NewInlineKeyboardButtonData
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(btn("🔑 扫码登录 115", cbLogin)),
		tgbotapi.NewInlineKeyboardRow(btn("设置 cookie", cbCookie), btn("设置 ua", cbUA)),
		tgbotapi.NewInlineKeyboardRow(btn("🚚 转存通道", cbChannel), btn("🔁 自动转存 开/关", cbAutoTransfer)),
		tgbotapi.NewInlineKeyboardRow(btn("设置转存 cid", cbTransferCID), btn("设置离线 cid", cbOfflineCID)),
		tgbotapi.NewInlineKeyboardRow(btn("检查登录状态", cbCheckLogin), btn("账号初始化", cbInitAccount)),
		tgbotapi.NewInlineKeyboardRow(btn("开启 / 关闭 115 功能", cbToggle), btn("运行状态", cbState)),
		tgbotapi.NewInlineKeyboardRow(btn("📋 全部命令", cbHelp)),
	)
}

// cancelMenu 是两步式输入时挂在提示语下方的按钮，免去记忆 /cancel。
// 另给「返回上一层」：放弃这次输入并退回设置菜单。
func cancelMenu() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("取消输入", cbCancel),
			backButton(backToSettings),
		),
	)
}

// bindMeMenu 是 /id 下方的「绑定为我」按钮。
func bindMeMenu() tgbotapi.InlineKeyboardMarkup {
	return tgbotapi.NewInlineKeyboardMarkup(
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("绑定为我（授权我自己操作）", cbBindMe),
		),
	)
}
