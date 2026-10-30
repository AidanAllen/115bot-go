// Package tgfile 通过 MTProto 直接下载 Telegram 文件。
//
// 存在的理由：官方 Bot API 的 getFile 只能下 20MB 以内的文件，官方给的解法是
// 跑一个本地 telegram-bot-api 服务——它是 C++ 写的、只有 Linux 容器镜像，
// Windows 上要装 Docker Desktop 才跑得起来。本项目希望 Windows 双击 exe 也能
// 转存大文件，所以把「下载」这一步换成纯 Go 的 MTProto 客户端（gotd/td）：
// 不需要 Docker，不需要额外进程，交叉编译照旧，仍然是单文件 exe。
//
// 只做下载这一件事：收发消息仍然走 Bot API，
// 命令、相册、批量进度那些已经调好的逻辑一行都不用动。
package tgfile

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/gotd/td/fileid"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/downloader"
	"go.uber.org/zap"
	"golang.org/x/net/proxy"
)

// Config 是 MTProto 下载器的配置。
type Config struct {
	// AppID / AppHash 来自 https://my.telegram.org/ ，MTProto 层强制要求。
	AppID   int
	AppHash string
	// BotToken 用于登录 MTProto（bot 用 token 登录，不需要手机号）。
	BotToken string
	// SessionPath 保存 MTProto 会话（含 auth key，属敏感数据）。
	// 留空则每次下载都重新握手，慢一些但不落盘。
	SessionPath string
	// StallTimeout 是「连续多久没有收到新数据就判定卡死」的阈值，留空用默认 3 分钟。
	StallTimeout time.Duration
	// Debug 打开 gotd 内部日志（连接、鉴权、取块重试都会打出来）。
	//
	// gotd 默认用静默 logger，出问题时只能看到「没反应」——排查 MTProto
	// 卡在建连还是卡在取块，必须靠这层日志。跟 LOG_LEVEL=debug 联动。
	Debug  bool
	Logger *slog.Logger
}

// defaultStallTimeout 是停滞判定阈值。
//
// 为什么必须有：转存队列是**单 worker 串行**的，而 MTProto 这条路在
// 「已连上 DC、但一个字节都收不到」时会一直重试且不返回错误
// （2026-10-30 线上实际发生过：一个 mp4 卡了十几分钟，同时 60 个任务在队列里等着）。
// 没有这个阈值，一次静默卡死就让所有人的转存全部停摆。
//
// 3 分钟足够跨过短时的 FLOOD_WAIT / 建连抖动，又不至于让用户干等。
const defaultStallTimeout = 3 * time.Minute

// stallCheckInterval 是检查目标文件是否增长的间隔。
//
// 用 var 而不是 const：单元测试需要把它调小，否则一个用例要跑 15 秒。
var stallCheckInterval = 15 * time.Second

// Enabled 判断配置是否完整到可以启用 MTProto 下载。
func (c Config) Enabled() bool {
	return c.AppID > 0 && c.AppHash != "" && c.BotToken != ""
}

// Client 是按「一次下载一次连接」方式工作的下载器。
type Client struct {
	cfg Config
	log *slog.Logger

	// mu 串行化下载。
	//
	// 转存队列本身是串行的，这里加锁主要是防止多个入口（自动转存 + 手动 /d）
	// 同时触发下载时抢占同一个 session 文件——会话文件被并发读写会写坏。
	mu sync.Mutex
}

// New 创建下载器。配置不完整时返回的对象仍可用，只是 Enabled() 为 false。
func New(cfg Config) *Client {
	log := cfg.Logger
	if log == nil {
		log = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	}
	return &Client{cfg: cfg, log: log}
}

// Enabled 报告该下载器是否可用。
func (c *Client) Enabled() bool { return c.cfg.Enabled() }

// Download 把 file_id 对应的文件下载到 dstPath，返回实际写入的字节数。
//
// fileID 是 Bot API 给的 file_id。它在 MTProto 里其实是 location 的一种编码，
// gotd 能就地解出来（不产生网络请求），解完就能直接向对应 DC 取数据。
//
// 带停滞检测：连续 StallTimeout 没有写入任何字节就主动取消并报错。
// 这一步是必需的——MTProto 在「连上了 DC 但收不到数据」时不会自己返回错误，
// 会一直重试；而转存队列只有一个 worker，放它一直挂着等于全站转存停摆。
func (c *Client) Download(ctx context.Context, fileID, dstPath string) (int64, error) {
	if !c.Enabled() {
		return 0, fmt.Errorf("MTProto 未启用（需要 TG_API_ID / TG_API_HASH）")
	}

	f, err := fileid.DecodeFileID(fileID)
	if err != nil {
		return 0, fmt.Errorf("解析 file_id 失败: %w", err)
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return 0, fmt.Errorf("创建下载目录失败: %w", err)
	}

	// 普通文件走 InputFileLocation；少数（如某些 web 资源）是 web location。
	fileLoc, isFile := f.AsInputFileLocation()
	webLoc, isWeb := f.AsInputWebFileLocation()
	if !isFile && !isWeb {
		return 0, fmt.Errorf("该 file_id 不可下载")
	}

	stall := c.cfg.StallTimeout
	if stall <= 0 {
		stall = defaultStallTimeout
	}

	// 下载上下文：停滞检测触发时由 watchdog 取消，
	// 这样 gotd 内部的重试循环才能被真正打断。
	dctx, cancel := context.WithCancel(ctx)
	defer cancel()

	out, err := os.Create(filepath.Clean(dstPath))
	if err != nil {
		return 0, fmt.Errorf("创建下载文件失败: %w", err)
	}
	pw := &countingWriterAt{w: out, last: time.Now()}

	stalled := make(chan struct{})
	go watchStall(dctx, pw, stall, stalled, cancel)

	client := telegram.NewClient(c.cfg.AppID, c.cfg.AppHash, c.options())
	runErr := client.Run(dctx, func(ctx context.Context) error {
		if _, err := client.Auth().Bot(ctx, c.cfg.BotToken); err != nil {
			return fmt.Errorf("MTProto 登录失败: %w", err)
		}
		api := client.API()
		dl := downloader.NewDownloader()

		var b *downloader.Builder
		if isFile {
			b = dl.Download(api, fileLoc)
		} else {
			b = dl.Web(api, webLoc)
		}
		// Parallel 保留多线程分块下载（跨境丢包链路上单线程会慢到不可用），
		// 进度由 countingWriterAt 统计。
		_, err := b.Parallel(ctx, pw)
		return err
	})
	if cerr := out.Close(); runErr == nil {
		runErr = cerr
	}

	if runErr != nil {
		// 半成品文件留着会占空间，也会让调用方误以为下载成功过
		_ = os.Remove(dstPath)
		select {
		case <-stalled:
			c.log.Warn("MTProto 下载停滞，已放弃",
				"path", dstPath, "已收字节", pw.total(), "停滞阈值", stall)
			return 0, fmt.Errorf("MTProto 下载停滞：连续 %s 没收到任何数据（网络到 Telegram 存储节点可能不通）", stall)
		default:
		}
		return 0, runErr
	}

	st, err := os.Stat(dstPath)
	if err != nil {
		return 0, fmt.Errorf("校验下载结果失败: %w", err)
	}
	c.log.Debug("MTProto 下载完成", "path", dstPath, "size", st.Size())
	return st.Size(), nil
}

// watchStall 盯着写入进度，连续 stall 没有任何新字节就取消下载。
func watchStall(ctx context.Context, pw *countingWriterAt, stall time.Duration, stalled chan<- struct{}, cancel context.CancelFunc) {
	t := time.NewTicker(stallCheckInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if time.Since(pw.lastWrite()) >= stall {
				close(stalled)
				cancel()
				return
			}
		}
	}
}

// countingWriterAt 统计实际写入的字节数与最后一次写入时间。
//
// gotd 的 Parallel 会从多个 goroutine 并发 WriteAt，所以计数必须加锁。
type countingWriterAt struct {
	w io.WriterAt

	mu   sync.Mutex
	n    int64
	last time.Time
}

func (c *countingWriterAt) WriteAt(p []byte, off int64) (int, error) {
	n, err := c.w.WriteAt(p, off)
	if n > 0 {
		c.mu.Lock()
		c.n += int64(n)
		c.last = time.Now()
		c.mu.Unlock()
	}
	return n, err
}

func (c *countingWriterAt) total() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.n
}

func (c *countingWriterAt) lastWrite() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

// options 组装 gotd 客户端选项。
//
// 两个关键点：
//   - NoUpdates：更新由 Bot API 那边在收，MTProto 这边再收一遍会重复，
//     也会让连接为了等 update 而不肯进入我们想要的稳定状态。
//   - Resolver 用 dcs.Plain + proxy.Dial：这样 ALL_PROXY / NO_PROXY 环境变量
//     生效（golang.org/x/net/proxy 的 FromEnvironment 语义），
//     需要走代理的环境只要设一下 ALL_PROXY 就行，不必改代码。
func (c *Client) options() telegram.Options {
	opts := telegram.Options{
		NoUpdates: true,
		Resolver: dcs.Plain(dcs.PlainOptions{
			Dial: proxy.Dial,
		}),
	}
	if c.cfg.Debug {
		// gotd 默认 zap.NewNop()，卡住时完全没有线索。
		// 只有显式开 debug（LOG_LEVEL=debug）才铺日志，正常运行不产生噪音。
		opts.Logger = zap.Must(zap.NewDevelopment())
	}
	if c.cfg.SessionPath != "" {
		_ = os.MkdirAll(filepath.Dir(c.cfg.SessionPath), 0o700)
		opts.SessionStorage = &session.FileStorage{Path: c.cfg.SessionPath}
	}
	return opts
}
