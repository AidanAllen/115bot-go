package bot

import (
	"context"
	"errors"
	"fmt"
	"html"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/AidanAllen/115bot-go/internal/config"
	"github.com/AidanAllen/115bot-go/internal/p115"
	"github.com/AidanAllen/115bot-go/internal/store"
)

// Version 由构建时通过 -ldflags 注入。
var Version = "0.1.0"

// BuildTime 构建时间，同样由 -ldflags 注入。
var BuildTime = "unknown"

// maxConcurrentUpdates 是同时处理的更新数上限。
//
// 必须并发：一次 /d 上传可能持续数小时，若在长轮询循环里同步处理，
// 期间 /state、/cancel 等所有命令都会失去响应。
const maxConcurrentUpdates = 4

// App 是机器人主体，持有全部依赖。
type App struct {
	cfg      *config.Config
	st       *store.Store
	api      *tgbotapi.BotAPI
	log      *slog.Logger
	sessions *sessionManager
	started  time.Time

	// loginMu 保护 logins；每个 chat 同时只允许一条扫码登录流程。
	loginMu sync.Mutex
	logins  map[int64]*qrLoginState

	// slots 既是并发上限也是背压：满了之后暂停收取新更新。
	slots chan struct{}
	// transferQ 是转存队列。跨境上行只有一条链路，必须串行；
	// 自动转存开启后可能一次涌进多个文件，用队列保证不丢也不并发。
	transferQ chan transferJob
	// queued 是队列深度计数器。
	//
	// 不能直接用 len(a.transferQ) 代替：入队后 worker 可能立刻把任务取走，
	// len 变回 0，会被误判成「队列已满」（v0.2.1 的 bug —— 队列明明是空的，
	// 用户却收到「队列已满」，而同一个文件紧接着又开始下载了）。
	// 计数在入队时 +1、worker 取出时 -1，入队返回值因此恒 >= 1。
	queued atomic.Int64
	// inflight 记录还在队列里或正在转存的 file_id，用于拦截重复入队。
	inflight sync.Map
	// reserved 记录本次运行已排给某个「用户+目录」的文件名，用于给重名文件加 (1)(2)。
	reserved sync.Map

	handled atomic.Int64
}

// transferJob 是一个待执行的转存任务。
//
// 存的是已经解析好的 fileID / 文件名，而不是整条 Telegram 消息：
// 队列里的任务可能要等几个小时才轮到，只留必要字段更省内存也更明确。
type transferJob struct {
	ac       actor
	fileID   string
	fileName string
	size     int64
	// replyTo 是用户发来文件的那条消息 id，进度消息回复到它下面。
	replyTo int
	// cid 是入队时选定的目标目录，用于转存结束后归还占用的文件名。
	cid string
}

// transferQueueSize 队列容量。满了直接拒绝并提示，避免内存被待转存的大文件信息撑爆。
const transferQueueSize = 64

// New 构建 App。会立即校验 Token 并完成一次 getMe，失败即返回错误。
func New(cfg *config.Config, st *store.Store, log *slog.Logger) (*App, error) {
	api, err := tgbotapi.NewBotAPIWithAPIEndpoint(cfg.BotToken, cfg.APIEndpoint)
	if err != nil {
		return nil, fmt.Errorf("初始化 Telegram Bot 失败（请检查 BOT_TOKEN 与 TG_API_ENDPOINT）: %w", err)
	}

	// 环境变量里预置了 OWNER_ID 就直接落库，省掉一次手动绑定。
	// 没有预置也不影响使用：任何用户都能用，只是各自用自己的 115 账号。
	if cfg.OwnerID > 0 && st.GetInt64(store.KeyBotOwnerID, 0) == 0 {
		if err := st.SetInt64(store.KeyBotOwnerID, cfg.OwnerID); err != nil {
			return nil, fmt.Errorf("写入机器人所有者失败: %w", err)
		}
	}

	app := &App{
		cfg:       cfg,
		st:        st,
		api:       api,
		log:       log,
		sessions:  newSessionManager(5 * time.Minute),
		started:   time.Now(),
		logins:    make(map[int64]*qrLoginState),
		slots:     make(chan struct{}, maxConcurrentUpdates),
		transferQ: make(chan transferJob, transferQueueSize),
	}
	app.migrateLegacyAccount()
	go app.transferWorker()
	return app, nil
}

// migrateLegacyAccount 把单用户时期的全局 115 配置挂到 owner 名下。
//
// 不做这一步，升级后所有人的凭据都会「消失」——配置还在 app_config 里，
// 但运行时已经改读 user_config，用户会被迫重新扫码。
func (a *App) migrateLegacyAccount() {
	owner := a.st.GetInt64(store.KeyBotOwnerID, 0)
	if owner == 0 {
		owner = a.st.GetInt64(store.KeyBotAdminID, 0)
		if owner > 0 {
			if err := a.st.SetInt64(store.KeyBotOwnerID, owner); err != nil {
				a.log.Warn("迁移 owner 失败", "err", err)
				return
			}
			a.log.Info("已将旧管理员 id 转为机器人所有者", "owner_id", owner)
		}
	}
	if owner == 0 {
		return
	}
	n, err := a.st.MigrateLegacyGlobalAccount(owner)
	if err != nil {
		a.log.Warn("迁移旧版 115 配置失败", "owner_id", owner, "err", err)
		return
	}
	if n > 0 {
		a.log.Info("已将旧版全局 115 配置迁移到所有者名下", "owner_id", owner, "keys", n)
	}
}

// Self 返回 bot 自身信息，用于启动日志。
func (a *App) Self() tgbotapi.User { return a.api.Self }

// Run 启动长轮询循环，直到 ctx 取消。
func (a *App) Run(ctx context.Context) error {
	u := tgbotapi.NewUpdate(0)
	u.Timeout = int(a.cfg.PollTimeout.Seconds())
	u.AllowedUpdates = []string{"message", "callback_query"}

	updates := a.api.GetUpdatesChan(u)

	a.registerCommands()

	a.log.Info("机器人已启动",
		"username", a.api.Self.UserName,
		"id", a.api.Self.ID,
		"version", Version,
	)

	for {
		select {
		case <-ctx.Done():
			a.api.StopReceivingUpdates()
			return nil
		case update, ok := <-updates:
			if !ok {
				return nil
			}
			// 名额满时在这里形成背压；关停时不能继续等，否则会卡死
			select {
			case a.slots <- struct{}{}:
			case <-ctx.Done():
				a.api.StopReceivingUpdates()
				return nil
			}
			a.handled.Add(1)
			go func(u tgbotapi.Update) {
				defer func() { <-a.slots }()
				a.dispatch(ctx, u)
			}(update)
		}
	}
}

// dispatch 统一入口：区分回调与消息。
func (a *App) dispatch(ctx context.Context, update tgbotapi.Update) {
	defer func() {
		if r := recover(); r != nil {
			a.log.Error("处理更新时发生 panic", "panic", fmt.Sprint(r))
		}
	}()

	switch {
	case update.CallbackQuery != nil:
		a.handleCallback(ctx, update.CallbackQuery)
	case update.Message != nil:
		// debug 级别：出问题时能快速看清「消息到了、但为什么没被当命令处理」
		a.log.Debug("收到消息",
			"chat_id", update.Message.Chat.ID,
			"from", update.Message.From.ID,
			"text", update.Message.Text,
			"is_command", update.Message.IsCommand(),
			"command", update.Message.Command(),
		)
		a.handleMessage(ctx, update.Message)
	default:
		a.log.Debug("收到未知类型的更新", "update_id", update.UpdateID)
	}
}

// transferWorker 串行消费转存队列。
//
// 用 Background 上下文而不是某次 update 的上下文：上传可能持续数小时，
// 绑在处理单条消息的那个 ctx 上会被提前取消。
func (a *App) transferWorker() {
	for job := range a.transferQ {
		a.queued.Add(-1)
		a.log.Info("转存开始", "user_id", job.ac.UserID, "file", job.fileName, "待处理", a.queued.Load())
		a.runTransfer(context.Background(), job)
		a.inflight.Delete(job.fileID)
		a.log.Info("转存结束", "user_id", job.ac.UserID, "file", job.fileName, "待处理", a.queued.Load())
	}
}

// queueTransfer 把任务放进队列，返回排队位置（从 1 开始）。
// 队列满时返回 0。
func (a *App) queueTransfer(job transferJob) int {
	select {
	case a.transferQ <- job:
		return int(a.queued.Add(1))
	default:
		return 0
	}
}

// queueDepth 返回当前排队（含正在转存）的任务数，供 /state 与「队列已满」提示使用。
func (a *App) queueDepth() int { return int(a.queued.Load()) }

// ---- 归属关系 ----

// ownerID 返回机器人所有者 id，0 表示尚未绑定。
//
// owner 不是「唯一能用的人」：任何用户都能用，只是各自用自己的 115 账号。
// owner 是后续做 web 控制台 / 用户管理时的凭证，因此首次绑定要用口令保护。
func (a *App) ownerID() int64 { return a.st.GetInt64(store.KeyBotOwnerID, 0) }

// isOwner 判断是否为机器人所有者。
func (a *App) isOwner(userID int64) bool {
	id := a.ownerID()
	return id != 0 && id == userID
}

// ---- 发送辅助 ----

// send 发送 HTML 消息。
func (a *App) send(chatID int64, text string) { a.sendMsg(a.newMessage(chatID, text)) }

// sendReply 以「回复某条消息」的形式发送 HTML 消息。
//
// 转存进度必须用回复：一次来好几个文件时，普通消息和进度消息会交错，
// 用户分不清哪条进度对应哪个文件。
func (a *App) sendReply(chatID int64, replyToMessageID int, text string) {
	msg := a.newMessage(chatID, text)
	if replyToMessageID > 0 {
		msg.ReplyToMessageID = replyToMessageID
	}
	a.sendMsg(msg)
}

// sendWithMenu 发送带内联键盘的 HTML 消息。
func (a *App) sendWithMenu(chatID int64, text string, kb tgbotapi.InlineKeyboardMarkup) {
	msg := a.newMessage(chatID, text)
	msg.ReplyMarkup = kb
	a.sendMsg(msg)
}

// newMessage 统一消息格式，避免各处重复设置 ParseMode。
func (a *App) newMessage(chatID int64, text string) tgbotapi.MessageConfig {
	msg := tgbotapi.NewMessage(chatID, text)
	msg.ParseMode = "HTML"
	msg.DisableWebPagePreview = true
	return msg
}

func (a *App) sendMsg(msg tgbotapi.MessageConfig) {
	if _, err := a.api.Send(msg); err != nil {
		a.log.Warn("发送消息失败", "chat_id", msg.ChatID, "err", err)
	}
}

// editMenu 就地刷新消息正文与键盘，避免刷屏。
func (a *App) editMenu(chatID int64, messageID int, text string, kb tgbotapi.InlineKeyboardMarkup) {
	edit := tgbotapi.NewEditMessageTextAndMarkup(chatID, messageID, text, kb)
	edit.ParseMode = "HTML"
	if _, err := a.api.Request(edit); err != nil {
		a.log.Debug("刷新菜单失败", "err", err)
	}
}

// clearMenu 移除消息上的内联键盘，避免留下点了没反应的旧按钮。
func (a *App) clearMenu(chatID int64, messageID int) {
	edit := tgbotapi.NewEditMessageReplyMarkup(chatID, messageID, tgbotapi.InlineKeyboardMarkup{})
	if _, err := a.api.Request(edit); err != nil {
		a.log.Debug("移除键盘失败", "err", err)
	}
}

// answerCallback 回应回调查询，消除客户端 loading。
func (a *App) answerCallback(id, text string) {
	cb := tgbotapi.NewCallback(id, text)
	if _, err := a.api.Request(cb); err != nil {
		a.log.Debug("回应回调失败", "err", err)
	}
}

// ---- 业务读取辅助（全部按用户隔离，见 user.go）----

// uaLabel 把一段 UA 转成用户看得懂的档位名；非预置档位统一显示为「自定义」。
func uaLabel(ua string) string {
	if strings.TrimSpace(ua) == "" {
		return ""
	}
	if p, ok := p115.MatchUAProfile(ua); ok {
		return p.Name
	}
	return "自定义"
}

// friendlyErr 把内部错误翻译成对用户友好的中文。
func friendlyErr(err error) string {
	switch {
	case errors.Is(err, p115.ErrLoginRequired):
		return TxtLoginInvalid
	case errors.Is(err, context.DeadlineExceeded):
		return "请求超时，请稍后重试"
	case errors.Is(err, p115.ErrUploaderNotConfigured):
		return TxtTransferNoUploader
	default:
		return err.Error()
	}
}

// escapeHTML 转义用户可控文本，避免破坏 HTML 解析。
func escapeHTML(s string) string { return html.EscapeString(s) }

// cut 截断长文本。
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}

// humanSize 把字节数格式化为可读大小。
func humanSize(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	units := []string{"KB", "MB", "GB", "TB"}
	v := float64(n)
	for _, u := range units {
		v /= unit
		if v < unit {
			return fmt.Sprintf("%.2f %s", v, u)
		}
	}
	return fmt.Sprintf("%.2f PB", v/unit)
}

// orDefault 为空时返回默认值。
func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
