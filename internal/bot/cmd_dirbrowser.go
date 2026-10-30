package bot

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/AidanAllen/115bot-go/internal/store"
)

// 目录浏览器的回调数据格式。
//
// 所有上下文都编码进 callback_data，服务端不保存浏览状态：
// 这样并发点击、隔很久再点、重启之后点都不会串，也不需要清理过期会话。
//
//	d:e:<t>:<cid>          进入目录
//	d:s:<t>:<cid>          把该目录设为落盘目录
//	d:p:<t>:<cid>:<page>   翻页
//	d:m:<t>                改为手工输入 cid
//
// <t> 是目标：t=转存目录，o=离线目录。
const cbDirPrefix = "d:"

const (
	dirTargetTransfer = "t"
	dirTargetOffline  = "o"
)

// dirPageSize 每页展示的目录数。目录多了会挤成长消息，反而难点。
const dirPageSize = 8

// dirMaxDirs 单次最多收集的目录数，避免大目录翻太多页。
const dirMaxDirs = 200

// dirTargetKey 目标标识 → 配置键。
func dirTargetKey(t string) string {
	if t == dirTargetOffline {
		return store.KeyOfflineCID
	}
	return store.KeyTransferCID
}

// dirTargetName 目标标识 → 可读名。
func dirTargetName(t string) string {
	if t == dirTargetOffline {
		return "离线"
	}
	return "转存"
}

// dirAction 目标标识 → 手工输入时对应的输入动作。
func dirAction(t string) Action {
	if t == dirTargetOffline {
		return ActionOfflineCID
	}
	return ActionTransferCID
}

// dirCallback 是解析后的目录浏览器回调。
type dirCallback struct {
	Op     string // e 进入 / s 选中 / p 翻页 / m 手工输入
	Target string
	CID    string
	Page   int
}

// parseDirCallback 解析回调数据；格式不合法时返回 false，交由后续分支处理。
func parseDirCallback(data string) (dirCallback, bool) {
	rest, ok := strings.CutPrefix(data, cbDirPrefix)
	if !ok {
		return dirCallback{}, false
	}
	parts := strings.Split(rest, ":")
	if len(parts) < 2 {
		return dirCallback{}, false
	}
	c := dirCallback{Op: parts[0], Target: parts[1]}
	if c.Target != dirTargetTransfer && c.Target != dirTargetOffline {
		return dirCallback{}, false
	}
	switch c.Op {
	case "e", "s", "p", "m":
	default:
		return dirCallback{}, false
	}
	if len(parts) > 2 {
		c.CID = parts[2]
	}
	if len(parts) > 3 {
		n, err := strconv.Atoi(parts[3])
		if err != nil || n < 0 {
			return dirCallback{}, false
		}
		c.Page = n
	}
	return c, true
}

// dirData 拼装回调数据。页码为 0 时省略，保持数据尽量短（TG 上限 64 字节）。
func dirData(op, target, cid string, page int) string {
	s := cbDirPrefix + op + ":" + target
	if cid != "" {
		s += ":" + cid
	}
	if page > 0 {
		s += ":" + strconv.Itoa(page)
	}
	return s
}

// openDirBrowser 打开目录浏览器（从根目录开始）。
func (a *App) openDirBrowser(ctx context.Context, ac actor, target string) {
	a.showDirBrowser(ctx, ac, 0, target, "0", 0)
}

// showDirBrowser 渲染目录浏览器。msgID 为 0 时发新消息，否则就地刷新原消息。
//
// 列的是发起者自己 115 账号里的目录，所以必须先登录。
//
// ⚠️ msgID != 0 的调用必须来自 handleDirCallback（回调路径已在入口处
// answerCallback(cbID) 应答过），这里不再重复应答。
func (a *App) showDirBrowser(ctx context.Context, ac actor, msgID int, target, cid string, page int) {
	btn := tgbotapi.NewInlineKeyboardButtonData

	if !a.userLoggedIn(ac.UserID) {
		a.send(ac.ChatID, TxtNeed115Login)
		return
	}
	c := a.userClient(ac.UserID)
	if !c.Ready() {
		a.send(ac.ChatID, TxtNeed115Login)
		return
	}
	cid = strings.TrimSpace(cid)
	if cid == "" {
		cid = "0"
	}

	ctx, cancel := context.WithTimeout(ctx, a.cfg.HTTPTimeout)
	defer cancel()

	// 当前目录的路径与父级来自同一次请求：少一次往返，也保证两者一致
	path := "根目录"
	parent := ""
	if cat, err := c.CategoryGet(ctx, cid); err == nil && cat != nil {
		if p := cat.CategoryPath(); p != "" {
			path = p
		}
		if n := len(cat.Paths); n > 0 {
			if v := strings.TrimSpace(cat.Paths[n-1].FileID.String()); v != "" {
				parent = v
			}
		}
	} else if err != nil && cid != "0" {
		a.log.Debug("读取目录信息失败", "cid", cid, "err", err)
	}

	dirs, err := c.ListDirs(ctx, cid, dirMaxDirs)
	if err != nil {
		a.log.Warn("列举目录失败", "user_id", ac.UserID, "cid", cid, "err", err)
		msg := fmt.Sprintf(TxtDirListFailed, escapeHTML(friendlyErr(err)))
		if msgID == 0 {
			a.send(ac.ChatID, msg)
		} else {
			a.editMenu(ac.ChatID, msgID, msg, tgbotapi.NewInlineKeyboardMarkup(
				tgbotapi.NewInlineKeyboardRow(btn("↩️ 重试", dirData("e", target, cid, 0)), btn("✍️ 手动输入 cid", dirData("m", target, "", 0))),
				backRow(backToSettings),
			))
		}
		return
	}

	total := len(dirs)
	pages := (total + dirPageSize - 1) / dirPageSize
	if pages == 0 {
		pages = 1
	}
	if page >= pages {
		page = pages - 1
	}
	if page < 0 {
		page = 0
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf(TxtDirBrowserTitle, dirTargetName(target), escapeHTML(path)))
	if total == 0 {
		b.WriteString("\n\n该目录下没有子目录")
	} else {
		b.WriteString(fmt.Sprintf(TxtDirBrowserPage, page+1, pages, total))
	}
	text := b.String()

	rows := make([][]tgbotapi.InlineKeyboardButton, 0, dirPageSize+3)
	start := page * dirPageSize
	end := start + dirPageSize
	if end > total {
		end = total
	}
	for _, d := range dirs[start:end] {
		name := d.Name
		if name == "" {
			name = d.CID
		}
		rows = append(rows, tgbotapi.NewInlineKeyboardRow(btn("📁 "+name, dirData("e", target, d.CID, 0))))
	}

	// 分页：只有多页时才出现，避免无谓按钮
	if pages > 1 {
		var nav []tgbotapi.InlineKeyboardButton
		if page > 0 {
			nav = append(nav, btn("⬅️ 上一页", dirData("p", target, cid, page-1)))
		}
		if page < pages-1 {
			nav = append(nav, btn("下一页 ➡️", dirData("p", target, cid, page+1)))
		}
		rows = append(rows, nav)
	}

	// 操作行：根目录不能作为落盘目录，所以不给出「选中」按钮
	var ops []tgbotapi.InlineKeyboardButton
	if cid != "0" {
		ops = append(ops, btn("✅ 用这个目录", dirData("s", target, cid, 0)))
	}
	if parent != "" && cid != "0" {
		ops = append(ops, btn("⬆️ 上级目录", dirData("e", target, parent, 0)))
	}
	if len(ops) > 0 {
		rows = append(rows, ops)
	}
	rows = append(rows, dirFooterRows(target)...)

	if msgID == 0 {
		a.sendWithMenu(ac.ChatID, text, tgbotapi.NewInlineKeyboardMarkup(rows...))
		return
	}
	a.editMenu(ac.ChatID, msgID, text, tgbotapi.NewInlineKeyboardMarkup(rows...))
}

// dirFooterRows 浏览器底部固定操作区。
func dirFooterRows(target string) [][]tgbotapi.InlineKeyboardButton {
	return [][]tgbotapi.InlineKeyboardButton{
		tgbotapi.NewInlineKeyboardRow(
			tgbotapi.NewInlineKeyboardButtonData("✍️ 手动输入 cid", dirData("m", target, "", 0)),
			backButton(backToSettings),
		),
	}
}

// handleDirCallback 分发目录浏览器的四类回调。
func (a *App) handleDirCallback(ctx context.Context, ac actor, msgID int, cbID string, dc dirCallback) {
	switch dc.Op {
	case "e":
		a.answerCallback(cbID, "")
		a.showDirBrowser(ctx, ac, msgID, dc.Target, dc.CID, 0)
	case "p":
		a.answerCallback(cbID, "")
		a.showDirBrowser(ctx, ac, msgID, dc.Target, dc.CID, dc.Page)
	case "m":
		a.answerCallback(cbID, "")
		a.startInput(ac, dirAction(dc.Target))
	case "s":
		if dc.CID == "" || dc.CID == "0" {
			a.answerCallback(cbID, "根目录不可用")
			a.send(ac.ChatID, TxtCIDRootDenied)
			return
		}
		a.answerCallback(cbID, "正在保存…")
		// 目录归属点击者本人，不是消息所在会话的主人
		a.applyCID(ctx, ac, dirTargetKey(dc.Target), dc.CID)
		a.clearMenu(ac.ChatID, msgID)
	default:
		a.answerCallback(cbID, "")
	}
}
