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
	"log/slog"
	"os"
	"path/filepath"
	"sync"

	"github.com/gotd/td/fileid"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/dcs"
	"github.com/gotd/td/telegram/downloader"
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
	Logger      *slog.Logger
}

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

	client := telegram.NewClient(c.cfg.AppID, c.cfg.AppHash, c.options())
	err = client.Run(ctx, func(ctx context.Context) error {
		if _, err := client.Auth().Bot(ctx, c.cfg.BotToken); err != nil {
			return fmt.Errorf("MTProto 登录失败: %w", err)
		}
		api := client.API()
		dl := downloader.NewDownloader()
		// 普通文件走 InputFileLocation；少数（如某些 web 资源）是 web location。
		if loc, ok := f.AsInputFileLocation(); ok {
			if _, err := dl.Download(api, loc).ToPath(ctx, dstPath); err != nil {
				return err
			}
			return nil
		}
		if loc, ok := f.AsInputWebFileLocation(); ok {
			if _, err := dl.Web(api, loc).ToPath(ctx, dstPath); err != nil {
				return err
			}
			return nil
		}
		return fmt.Errorf("该 file_id 不可下载")
	})
	if err != nil {
		// 半成品文件留着会占空间，也会让调用方误以为下载成功过
		_ = os.Remove(dstPath)
		return 0, err
	}

	st, err := os.Stat(dstPath)
	if err != nil {
		return 0, fmt.Errorf("校验下载结果失败: %w", err)
	}
	c.log.Debug("MTProto 下载完成", "path", dstPath, "size", st.Size())
	return st.Size(), nil
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
	if c.cfg.SessionPath != "" {
		_ = os.MkdirAll(filepath.Dir(c.cfg.SessionPath), 0o700)
		opts.SessionStorage = &session.FileStorage{Path: c.cfg.SessionPath}
	}
	return opts
}
