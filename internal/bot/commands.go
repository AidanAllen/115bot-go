package bot

import (
	"fmt"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// cmdPrefix 是「点按钮即执行命令」的回调数据前缀。
const cmdPrefix = "cmd:"

// cmdSpec 描述一条斜杠命令。
//
// 这张表是命令的**唯一来源**：Telegram 的输入框「/」命令菜单、/help 正文、
// /help 下方的可点按钮，三者都由它生成。
// 此前三者各写一份，加一条命令要改三处，漏改就会出现「菜单里有、点了没反应」。
type cmdSpec struct {
	Name string // 不含斜杠
	Desc string // Telegram 命令菜单中的说明（3-256 字符）
	Tip  string // /help 正文中的补充用法，可为空
}

// commandSpecs 是全部命令的声明顺序，同时也是 /help 的展示顺序。
var commandSpecs = []cmdSpec{
	{Name: "start", Desc: "打开 115 设置菜单"},
	{Name: "set115", Desc: "115 设置菜单：登录、cookie、ua、目录"},
	{Name: "login", Desc: "扫码登录 115（可点按钮选设备）"},
	{Name: "d", Desc: "自动转存开关：开启后发文件即自动进 115", Tip: "单独发 = 开关自动转存；回复某条消息 = 只转那一条（/d 新名字.mp4 可重命名）"},
	{Name: "checklogin", Desc: "检查 115 登录状态"},
	{Name: "initaccount", Desc: "账号初始化：校验凭据并写入账号信息"},
	{Name: "cookie", Desc: "手工设置 cookie"},
	{Name: "ua", Desc: "设置 UA：可选网页 / App / 小程序档位", Tip: "不带参数打开档位面板，点按钮即可切换；也可 /ua 粘贴自己的 UA"},
	{Name: "channel", Desc: "选择转存通道：网页 / App / 小程序", Tip: "决定上传时以哪个端的身份发起请求；不带参数打开面板，点按钮即可切换"},
	{Name: "transfercid", Desc: "设置转存目录：可点选或直接输入 cid"},
	{Name: "setcid", Desc: "设置离线保存目录：可点选或直接输入 cid"},
	{Name: "s", Desc: "开启或关闭 115 功能"},
	{Name: "state", Desc: "查看运行状态与任务统计"},
	{Name: "id", Desc: "查看我的 id / 绑定机器人所有者"},
	{Name: "setid", Desc: "绑定机器人所有者（点 /id 可一键绑定为自己）"},
	{Name: "v", Desc: "版本与运行时信息"},
	{Name: "help", Desc: "查看全部命令"},
	{Name: "cancel", Desc: "取消当前输入或扫码登录"},
}

// botCommands 生成提交给 Telegram 的命令列表。
func botCommands() []tgbotapi.BotCommand {
	out := make([]tgbotapi.BotCommand, 0, len(commandSpecs))
	for _, c := range commandSpecs {
		out = append(out, tgbotapi.BotCommand{Command: c.Name, Description: c.Desc})
	}
	return out
}

// registerCommands 把命令注册到 Telegram。
//
// 注册后用户在输入框打「/」就会弹出这条列表（带说明），无需记忆命令名。
// 失败只告警不中断：命令菜单是便利功能，不影响机器人本体。
func (a *App) registerCommands() {
	if _, err := a.api.Request(tgbotapi.NewSetMyCommands(botCommands()...)); err != nil {
		a.log.Warn("注册命令菜单失败（不影响命令使用）", "err", err)
		return
	}
	a.log.Info("命令菜单已注册", "count", len(commandSpecs))
}

// helpText 生成 /help 正文。
func helpText() string {
	var b strings.Builder
	b.WriteString("<b>可用命令</b>\n\n")
	b.WriteString("下方按钮点一下即可执行；在输入框打 <code>/</code> 也会弹出这份列表。\n")

	for _, c := range commandSpecs {
		fmt.Fprintf(&b, "\n<code>/%s</code> —— %s", c.Name, escapeHTML(c.Desc))
		if c.Tip != "" {
			fmt.Fprintf(&b, "\n    <i>%s</i>", escapeHTML(c.Tip))
		}
	}

	b.WriteString("\n\n<b>不用命令也能用</b>\n")
	b.WriteString("直接发送磁力链 / ed2k 链接 —— 自动下发离线下载\n")
	b.WriteString("回复一条视频或文件消息 + <code>/d</code> —— 转存到 115")
	return b.String()
}

// helpMenu 生成 /help 下方的可点按钮，每行两个。
func helpMenu() tgbotapi.InlineKeyboardMarkup {
	var rows [][]tgbotapi.InlineKeyboardButton
	var row []tgbotapi.InlineKeyboardButton
	for _, c := range commandSpecs {
		row = append(row, tgbotapi.NewInlineKeyboardButtonData("/"+c.Name, cmdPrefix+c.Name))
		if len(row) == 2 {
			rows = append(rows, row)
			row = nil
		}
	}
	if len(row) > 0 {
		rows = append(rows, row)
	}
	// 命令面板是从设置菜单点进来的，底部给一条退路，不必再敲 /start
	rows = append(rows, backRow(backToSettings))
	return tgbotapi.NewInlineKeyboardMarkup(rows...)
}
