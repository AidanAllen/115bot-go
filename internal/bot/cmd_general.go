package bot

import (
	"context"
	"fmt"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/ghs/115bot-go/internal/config"
	"github.com/ghs/115bot-go/internal/store"
)

// cmdID 返回调用者的 Telegram id 与当前会话 id。
// 尚未绑定 owner 时给出「绑定为我」按钮，用户不必手抄这串数字。
func (a *App) cmdID(ac actor) {
	owner := a.ownerID()
	var b strings.Builder
	fmt.Fprintf(&b, "<b>你的 id</b>：<code>%d</code>\n", ac.UserID)
	fmt.Fprintf(&b, "<b>会话 id</b>：<code>%d</code>\n", ac.ChatID)
	if owner == 0 {
		b.WriteString("\n尚未绑定机器人所有者，点下方按钮即可绑定为你自己")
		a.sendWithMenu(ac.ChatID, b.String(), bindMeMenu())
		return
	}
	if owner == ac.UserID {
		b.WriteString("\n你是机器人所有者 ✅")
	} else {
		fmt.Fprintf(&b, "\n机器人所有者 id 为 <code>%d</code>（不是你）", owner)
	}
	a.send(ac.ChatID, b.String())
}

// cmdSetID 处理 /setid。
//
// owner 只是「机器人归谁」的标记，用来做之后的 web 控制台与用户管理；
// 它不决定谁能用——任何用户都能用，只是各用各的 115 账号。
func (a *App) cmdSetID(ctx context.Context, ac actor, args string) {
	owner := a.ownerID()

	// 已绑定且不是本人 → 拒绝
	if owner != 0 && owner != ac.UserID {
		a.send(ac.ChatID, TxtNotOwner)
		return
	}
	if args == "" {
		a.startInput(ac, ActionSetID)
		return
	}
	a.applySetID(ac, args)
}

// applySetID 校验并写入 owner id。
func (a *App) applySetID(ac actor, raw string) {
	s := strings.TrimSpace(raw)
	if !config.IsDigits(s) {
		a.send(ac.ChatID, TxtSetIDBadFmt)
		return
	}
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil || id <= 0 {
		a.send(ac.ChatID, TxtSetIDBadFmt)
		return
	}
	if cur := a.ownerID(); cur == id {
		a.send(ac.ChatID, fmt.Sprintf(TxtSetIDSame, id))
		return
	}
	if err := a.st.SetInt64(store.KeyBotOwnerID, id); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}
	a.log.Info("机器人所有者已绑定", "owner_id", id)
	a.send(ac.ChatID, fmt.Sprintf(TxtSetIDOK, id))
}

// cmdToggle 切换该用户自己的 115 功能开关。
func (a *App) cmdToggle(ac actor) {
	next := !a.userEnabled(ac.UserID)
	if err := a.st.SetUserBool(ac.UserID, store.KeyBotEnabled, next); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}
	if next {
		a.send(ac.ChatID, TxtBotEnabledOn)
	} else {
		a.send(ac.ChatID, TxtBotEnabledOff)
	}
}

// cmdToggleAutoTransfer 切换「自动转存」：开启后发给机器人的每个文件自动进 115。
//
// 开启时就把凭据、上传通道、转存目录全部校验一遍。
// 不做这一步，开完才发现转不了，之后每来一个文件都会回一条报错，等于刷屏。
func (a *App) cmdToggleAutoTransfer(ac actor) {
	if a.userAutoTransfer(ac.UserID) {
		if err := a.st.SetUserBool(ac.UserID, store.KeyAutoTransfer, false); err != nil {
			a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
			return
		}
		a.log.Info("自动转存已关闭", "user_id", ac.UserID)
		a.send(ac.ChatID, TxtAutoTransferOff)
		return
	}

	switch {
	case !a.userEnabled(ac.UserID):
		a.send(ac.ChatID, TxtTransferDisabled)
		return
	case !a.userLoggedIn(ac.UserID):
		a.send(ac.ChatID, TxtNeed115Login)
		return
	case !a.userUploader(ac.UserID).Available():
		a.send(ac.ChatID, TxtTransferNoUploader)
		return
	case a.userTransferCID(ac.UserID) == "":
		a.send(ac.ChatID, TxtNeedTransferCID)
		return
	}

	if err := a.st.SetUserBool(ac.UserID, store.KeyAutoTransfer, true); err != nil {
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}
	a.log.Info("自动转存已开启", "user_id", ac.UserID)
	a.send(ac.ChatID, TxtAutoTransferOn)
}

// cmdVersion 输出版本信息。
func (a *App) cmdVersion(chatID int64) {
	text := fmt.Sprintf("<b>版本</b>：<code>%s</code>\n<b>构建时间</b>：<code>%s</code>\n<b>运行时</b>：<code>%s</code>\n<b>启动于</b>：<code>%s</code>",
		Version, BuildTime, runtime.Version(), a.started.Format("2006-01-02 15:04:05"))
	a.send(chatID, text)
}

// cmdState 输出运行状态与自己名下任务的统计。
func (a *App) cmdState(ac actor) {
	var ms runtime.MemStats
	runtime.ReadMemStats(&ms)

	up := time.Since(a.started).Truncate(time.Second)

	// 任务按用户隔离：别人的离线/转存记录不该出现在这里
	offline, _ := a.st.CountByStatus(ac.UserID, store.KindOffline)
	transfer, _ := a.st.CountByStatus(ac.UserID, store.KindTransfer)
	total, _ := a.st.CountAll(ac.UserID)

	s := a.userSnapshot(ac.UserID)

	var b strings.Builder
	b.WriteString("<b>运行状态</b>\n\n")

	b.WriteString("<b>进程</b>\n")
	fmt.Fprintf(&b, "运行时长：%s\n", up)
	fmt.Fprintf(&b, "常驻内存：%s\n", humanSize(int64(ms.Sys)))
	fmt.Fprintf(&b, "堆占用：%s\n", humanSize(int64(ms.HeapAlloc)))
	fmt.Fprintf(&b, "协程数：%d\n", runtime.NumGoroutine())
	fmt.Fprintf(&b, "已处理更新：%d\n\n", a.handled.Load())

	b.WriteString("<b>115 账号</b>\n")
	fmt.Fprintf(&b, "功能开关：%s\n", onOff(s.Enabled))
	fmt.Fprintf(&b, "cookie：%s\n", setOrNot(s.HasCookie))
	uaLine := "未设置"
	if s.UALabel != "" {
		uaLine = s.UALabel
		if !s.UAExplicit {
			uaLine += "（内置默认）"
		}
	}
	fmt.Fprintf(&b, "ua：%s\n", uaLine)
	fmt.Fprintf(&b, "登录状态：%s\n", loginState(s.LoginOK, s.Nickname))
	fmt.Fprintf(&b, "转存 cid：%s\n", orDash(s.TransferCID))
	fmt.Fprintf(&b, "离线 cid：%s\n", orDash(s.OfflineCID))
	fmt.Fprintf(&b, "转存通道：%s\n\n", a.userUploader(ac.UserID).Name())

	b.WriteString("<b>任务（仅你自己的）</b>\n")
	fmt.Fprintf(&b, "转存队列：%d 个待处理（上限 %d）\n", a.queueDepth(), transferQueueSize)
	fmt.Fprintf(&b, "离线：等待 %d / 进行 %d / 成功 %d / 失败 %d\n",
		offline[store.StatusPending], offline[store.StatusRunning], offline[store.StatusSuccess], offline[store.StatusFailed])
	fmt.Fprintf(&b, "转存：等待 %d / 进行 %d / 成功 %d / 失败 %d\n",
		transfer[store.StatusPending], transfer[store.StatusRunning], transfer[store.StatusSuccess], transfer[store.StatusFailed])
	fmt.Fprintf(&b, "累计：%d 条", total)

	a.send(ac.ChatID, b.String())
}

func onOff(v bool) string {
	if v {
		return "开启 ✅"
	}
	return "关闭 ⛔"
}

func setOrNot(v bool) string {
	if v {
		return "已设置 ✅"
	}
	return "未设置 ❌"
}

func loginState(ok bool, nick string) string {
	if ok {
		if nick != "" {
			return "正常 ✅（" + escapeHTML(nick) + "）"
		}
		return "正常 ✅"
	}
	return "待验证 ⚠️"
}

func orDash(s string) string {
	if s == "" {
		return "未设置"
	}
	return s
}
