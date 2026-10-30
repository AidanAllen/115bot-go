package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"github.com/AidanAllen/115bot-go/internal/bot"
	"github.com/AidanAllen/115bot-go/internal/config"
	"github.com/AidanAllen/115bot-go/internal/p115"
	"github.com/AidanAllen/115bot-go/internal/store"
	"github.com/AidanAllen/115bot-go/internal/tgfile"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		showVersion  = flag.Bool("version", false, "打印版本并退出")
		checkCookie  = flag.Bool("check-cookie", false, "仅校验 115 cookie 登录态后退出（不启动机器人）")
		cookieArg    = flag.String("cookie", "", "配合 --check-cookie 使用的 cookie 字符串；留空则读取数据库中的配置")
		checkMTProto = flag.String("check-mtproto", "", "用 MTProto 下载指定 file_id 验证大文件通道后退出")
	)
	flag.Parse()

	if *showVersion {
		fmt.Printf("115bot-go %s (built %s)\n", bot.Version, bot.BuildTime)
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	logger := newLogger(cfg.LogLevel)

	// CLI 模式：只验 cookie，方便部署前自查
	if *checkCookie {
		return runCheckCookie(cfg, *cookieArg, logger)
	}
	// CLI 模式：验证大文件下载链路是否通（不需要真跑机器人）
	if strings.TrimSpace(*checkMTProto) != "" {
		return runCheckMTProto(cfg, strings.TrimSpace(*checkMTProto), logger)
	}

	st, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer func() { _ = st.Close() }()

	// 上一次进程被杀时正在跑的任务不会有任何机会写回结果，会永远停在 running。
	// 启动时统一收敛成 failed，避免用户看到永不动弹的「进行中」。
	if n, cerr := st.FailStaleRunning("服务重启，任务已中断"); cerr != nil {
		logger.Warn("清理残留的进行中任务失败", "err", cerr)
	} else if n > 0 {
		logger.Info("已把残留的进行中任务标记为失败", "count", n)
	}

	app, err := bot.New(cfg, st, logger)
	if err != nil {
		return err
	}

	// 信号按平台分开：Unix 接 SIGTERM（docker stop），Windows 只有 Ctrl+C。
	// 见同目录 signals_unix.go / signals_windows.go。
	ctx, stop := signal.NotifyContext(context.Background(), shutdownSignals()...)
	defer stop()

	// 多用户模型下，115 凭据不再是全局的：这里报告的是 owner 视角与已接入的用户数。
	owner := st.GetInt64(store.KeyBotOwnerID, 0)
	if owner == 0 {
		owner = st.GetInt64(store.KeyBotAdminID, 0)
	}
	users, _ := st.ListUserIDs(store.KeyCookie)

	logger.Info("配置加载完成",
		"db", cfg.DBPath,
		"endpoint", cfg.APIEndpoint,
		"owner_id", owner,
		"users_with_115", len(users),
		"uploader", uploaderName(st, cfg, owner),
		"offline_cid", maskEmpty(effectiveOfflineCID(st, cfg, owner)),
		"transfer_cid", maskEmpty(effectiveTransferCID(st, cfg, owner)),
	)

	if err := app.Run(ctx); err != nil {
		return err
	}
	logger.Info("已优雅退出")
	return nil
}

// runCheckMTProto 用 MTProto 真下一个文件，验证大文件通道。
//
// 有这个开关是因为：MTProto 这链路成不成，取决于凭据、出口网络、DC 连通性，
// 光看编译通过不代表能下。用户拿一个 >20MB 文件的 file_id 跑一次就知道，
// 不用先等机器人把文件转一遍（也用不着我拿到他的凭据）。
func runCheckMTProto(cfg *config.Config, fileID string, logger *slog.Logger) error {
	if cfg.TGAPIID <= 0 || cfg.TGAPIHash == "" {
		return errors.New("未配置 TG_API_ID / TG_API_HASH，MTProto 未启用")
	}
	c := tgfile.New(tgfile.Config{
		AppID:       cfg.TGAPIID,
		AppHash:     cfg.TGAPIHash,
		BotToken:    cfg.BotToken,
		SessionPath: cfg.MTProtoSession,
		Logger:      logger,
	})
	dst := filepath.Join(cfg.TempDir, fmt.Sprintf("mtproto_check_%d.bin", time.Now().UnixNano()))
	// 大文件在慢链路上可能要很久，这里给足时间
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	start := time.Now()
	n, err := c.Download(ctx, fileID, dst)
	if err != nil {
		return err
	}
	defer func() { _ = os.Remove(dst) }()
	fmt.Printf("MTProto 下载成功：%d 字节（%.1f MB），用时 %s\n",
		n, float64(n)/(1<<20), time.Since(start).Round(time.Millisecond))
	return nil
}

// runCheckCookie 校验指定或已保存的 cookie 登录态。
func runCheckCookie(cfg *config.Config, cookie string, logger *slog.Logger) error {
	if strings.TrimSpace(cookie) == "" {
		st, err := store.Open(cfg.DBPath)
		if err != nil {
			return err
		}
		defer func() { _ = st.Close() }()
		owner := st.GetInt64(store.KeyBotOwnerID, 0)
		if owner == 0 {
			owner = st.GetInt64(store.KeyBotAdminID, 0)
		}
		cookie = savedCookie(st, owner)
	}
	if strings.TrimSpace(cookie) == "" {
		return errors.New("未提供 --cookie，且数据库中也没有已保存的 cookie")
	}
	if err := p115.ValidateCookie(cookie); err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.HTTPTimeout)
	defer cancel()

	c := p115.New(cookie, cfg.DefaultUA, cfg.HTTPTimeout)
	info, err := c.CheckLogin(ctx)
	if err != nil {
		if errors.Is(err, p115.ErrLoginRequired) {
			return errors.New("115 登录态无效（cookie 已失效或与当前出口 IP 不匹配）")
		}
		return err
	}
	fmt.Printf("登录状态正常：%s (UID %d)\n", info.UserName, info.UserID)
	return nil
}

// effectiveTransferCID 返回 owner 运行期真正生效的转存 cid。
//
// 必须读数据库而不是只看环境变量：cid 通常用 /transfercid 设置，存在库里。
// 只打印环境变量会让日志显示「未设置」而实际已配好，排查时极易被带偏。
func effectiveTransferCID(st *store.Store, cfg *config.Config, owner int64) string {
	if owner != 0 {
		if v := strings.TrimSpace(st.GetUserConfigDefault(owner, store.KeyTransferCID, "")); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(st.GetConfigDefault(store.KeyTransferCID, "")); v != "" {
		return v
	}
	return cfg.TransferCID
}

// effectiveOfflineCID 返回 owner 生效的离线 cid，回退顺序与 App 保持一致：
// 离线 cid → 转存 cid → 环境变量。
func effectiveOfflineCID(st *store.Store, cfg *config.Config, owner int64) string {
	if owner != 0 {
		if v := strings.TrimSpace(st.GetUserConfigDefault(owner, store.KeyOfflineCID, "")); v != "" {
			return v
		}
	}
	if v := strings.TrimSpace(st.GetConfigDefault(store.KeyOfflineCID, "")); v != "" {
		return v
	}
	if v := effectiveTransferCID(st, cfg, owner); v != "" {
		return v
	}
	return cfg.OfflineCID
}

// uploaderName 按 owner 已保存的凭据报告 /d 实际会走的通道。
func uploaderName(st *store.Store, cfg *config.Config, owner int64) string {
	cookie := savedCookie(st, owner)
	ua := st.GetUserConfigDefault(owner, store.KeyUserAgent, "")
	if ua == "" {
		ua = st.GetConfigDefault(store.KeyUserAgent, cfg.DefaultUA)
	}
	// 通道优先级与运行时一致：用户配置 → 环境变量 → 默认通道
	channelID := strings.TrimSpace(st.GetUserConfigDefault(owner, store.KeyUploadChannel, ""))
	if channelID == "" {
		channelID = strings.TrimSpace(cfg.UploadChannel)
	}
	if channelID == "" {
		channelID = p115.DefaultUploadChannelID
	}
	return p115.NewUploader(p115.New(cookie, ua, cfg.HTTPTimeout), channelID).Name()
}

// savedCookie 取已保存的 cookie：多用户后按 owner 存，老库则回落到全局键。
func savedCookie(st *store.Store, owner int64) string {
	if owner != 0 {
		if v := st.GetUserConfigDefault(owner, store.KeyCookie, ""); v != "" {
			return v
		}
	}
	v, _ := st.GetConfig(store.KeyCookie)
	return v
}

func maskEmpty(s string) string {
	if s == "" {
		return "(未设置)"
	}
	return s
}

// newLogger 构建结构化 JSON 日志。
func newLogger(level string) *slog.Logger {
	var lv slog.Level
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		lv = slog.LevelDebug
	case "warn":
		lv = slog.LevelWarn
	case "error":
		lv = slog.LevelError
	default:
		lv = slog.LevelInfo
	}
	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: lv,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			// 统一时间格式
			if a.Key == slog.TimeKey {
				if t, ok := a.Value.Any().(time.Time); ok {
					a.Value = slog.StringValue(t.Format(time.RFC3339))
				}
			}
			return a
		},
	})
	return slog.New(h)
}
