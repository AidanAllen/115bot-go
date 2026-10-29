package bot

import tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

// 面板导航：所有二级窗口底部统一挂一个「返回上一层」。
//
// 此前子面板（UA 档位、转存通道、登录设备、目录浏览器、全部命令）只能靠重新敲命令退出，
// 点进去容易、退出来要记命令，越点越深就困在里面了。
//
// 回调形如 back:<dest>，dest 是目标面板标识；返回动作是就地刷新（editMenu），不刷屏。
// 路径不保存在服务端：隔很久再点、重启后再点都不会串。
const cbBackPrefix = "back:"

// backToSettings 返回 115 设置主菜单（/start、/set115 打开的那张面板）。
const backToSettings = "set"

// backButton 生成「返回上一层」按钮。
func backButton(dest string) tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardButtonData("⬅️ 返回上一层", cbBackPrefix+dest)
}

// backRow 生成独占一行的返回按钮，直接 append 到面板底部即可。
func backRow(dest string) []tgbotapi.InlineKeyboardButton {
	return tgbotapi.NewInlineKeyboardRow(backButton(dest))
}

// isKnownPanel 判断返回目标是否有对应面板。
func isKnownPanel(dest string) bool {
	return dest == backToSettings
}

// renderPanel 按标识渲染目标面板。
//
// 返回时复用进入面板时的同一套渲染函数，保证「返回看到的」与「直接点进去看到的」一致，
// 不会出现两条各写一份、改了这边漏了那边。
func (a *App) renderPanel(userID int64, dest string) (string, tgbotapi.InlineKeyboardMarkup, bool) {
	if !isKnownPanel(dest) {
		return "", tgbotapi.InlineKeyboardMarkup{}, false
	}
	return renderMenuText(a.userSnapshot(userID)), mainMenu(), true
}
