package bot

import (
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/ghs/115bot-go/internal/p115"
	"github.com/ghs/115bot-go/internal/store"
)

// actor 一次操作的发起者。
//
// UserID 决定用哪一份 115 凭据——每个用户有自己的 115 账号，cookie、UA、
// 通道、落盘目录全部按用户隔离，A 的文件不可能传到 B 的网盘。
// ChatID 决定回复发到哪个会话：私聊时两者相同，群聊时不同。
type actor struct {
	UserID int64
	ChatID int64
}

// actorOf 从一条消息取出发起者。
func actorOf(m *tgbotapi.Message) actor {
	return actor{UserID: m.From.ID, ChatID: m.Chat.ID}
}

// ---- 用户级凭据读取 ----

// userUA 返回该用户生效的 UA：用户配置 → 环境变量默认。
func (a *App) userUA(userID int64) string {
	return a.st.GetUserConfigDefault(userID, store.KeyUserAgent, a.cfg.DefaultUA)
}

// userClient 按该用户的 cookie 构造 115 客户端。
//
// 每次现构造：cookie / UA 都可以在运行时通过 /login、/cookie、/ua 改动，
// 缓存下来会让用户刚登录完仍然用旧凭据。
func (a *App) userClient(userID int64) *p115.Client {
	return p115.New(a.st.GetUserConfigDefault(userID, store.KeyCookie, ""), a.userUA(userID), a.cfg.HTTPTimeout)
}

// userUploader 按该用户的凭据与通道构造上传器。
func (a *App) userUploader(userID int64) p115.Uploader {
	return p115.NewUploader(a.userClient(userID), a.userChannelID(userID))
}

// userChannelID 该用户的转存通道：用户配置 → 环境变量 → 内置默认。
func (a *App) userChannelID(userID int64) string {
	if v := strings.TrimSpace(a.st.GetUserConfigDefault(userID, store.KeyUploadChannel, "")); v != "" {
		return v
	}
	if v := strings.TrimSpace(a.cfg.UploadChannel); v != "" {
		return v
	}
	return p115.DefaultUploadChannelID
}

// userTransferCID 该用户的转存目录：用户配置 → 环境变量默认。
func (a *App) userTransferCID(userID int64) string {
	return a.st.GetUserConfigDefault(userID, store.KeyTransferCID, a.cfg.TransferCID)
}

// userOfflineCID 该用户的离线目录：用户配置 → 其转存目录 → 环境变量默认。
func (a *App) userOfflineCID(userID int64) string {
	if v := a.st.GetUserConfigDefault(userID, store.KeyOfflineCID, ""); v != "" {
		return v
	}
	return orDefault(a.userTransferCID(userID), a.cfg.OfflineCID)
}

// userEnabled 该用户是否开启了 115 功能。开关按用户独立，
// 否则一个人关掉会让所有人都用不了。
func (a *App) userEnabled(userID int64) bool {
	return a.st.GetUserBool(userID, store.KeyBotEnabled, true)
}

// userAutoTransfer 该用户是否开启了「收到文件自动转存」。默认关闭：
// 陌生人一上来就自动上传会瞬间占满唯一的那条上行链路。
func (a *App) userAutoTransfer(userID int64) bool {
	return a.st.GetUserBool(userID, store.KeyAutoTransfer, false)
}

// userLoggedIn 该用户是否已配置 115 凭据（cookie 非空）。
func (a *App) userLoggedIn(userID int64) bool {
	return strings.TrimSpace(a.st.GetUserConfigDefault(userID, store.KeyCookie, "")) != ""
}

// userSnapshot 汇总该用户的账号状态，用于设置菜单展示。
func (a *App) userSnapshot(userID int64) accountSnapshot {
	up := a.userUploader(userID)
	savedUA := strings.TrimSpace(a.st.GetUserConfigDefault(userID, store.KeyUserAgent, ""))
	effective := savedUA
	if effective == "" {
		effective = a.cfg.DefaultUA
	}
	return accountSnapshot{
		Enabled:       a.userEnabled(userID),
		HasCookie:     a.userLoggedIn(userID),
		UALabel:       uaLabel(effective),
		UAExplicit:    savedUA != "",
		LoginOK:       a.st.GetUserConfigDefault(userID, store.KeyAccountStatus, "") == "ok",
		Nickname:      a.st.GetUserConfigDefault(userID, store.KeyAccountNick, ""),
		TransferCID:   a.userTransferCID(userID),
		OfflineCID:    a.userOfflineCID(userID),
		UploaderName:  up.Name(),
		UploaderReady: up.Available(),
		AutoTransfer:  a.userAutoTransfer(userID),
	}
}

// userUAProfileID 该用户当前 UA 所属的档位标识；自定义 UA 返回空串。
func (a *App) userUAProfileID(userID int64) string {
	if p, ok := p115.MatchUAProfile(a.userUA(userID)); ok {
		return p.ID
	}
	return ""
}

// saveUserUA 校验并写入该用户的 UA。
func (a *App) saveUserUA(userID int64, ua string) error {
	ua = strings.TrimSpace(ua)
	if err := p115.ValidateUA(ua); err != nil {
		return err
	}
	return a.st.SetUserConfig(userID, store.KeyUserAgent, ua)
}

// markUserLoginOK 记录该用户登录成功。
func (a *App) markUserLoginOK(userID int64, info *p115.UserInfo) {
	_ = a.st.SetUserConfig(userID, store.KeyAccountStatus, "ok")
	_ = a.st.SetUserConfig(userID, store.KeyAccountNick, info.UserName)
	_ = a.st.SetUserConfig(userID, store.KeyCheckedAt, time.Now().Format(time.RFC3339))
}

// markUserLoginInvalid 记录该用户登录失效。
func (a *App) markUserLoginInvalid(userID int64) {
	_ = a.st.SetUserConfig(userID, store.KeyAccountStatus, "invalid")
	_ = a.st.SetUserConfig(userID, store.KeyCheckedAt, time.Now().Format(time.RFC3339))
}

// requireClient 返回该用户可用的 115 客户端；凭据缺失时已回复提示。
func (a *App) requireClient(ac actor) (*p115.Client, bool) {
	if !a.userLoggedIn(ac.UserID) {
		a.send(ac.ChatID, TxtNeed115Login)
		return nil, false
	}
	c := a.userClient(ac.UserID)
	if !c.Ready() {
		a.send(ac.ChatID, TxtNeed115Login)
		return nil, false
	}
	return c, true
}
