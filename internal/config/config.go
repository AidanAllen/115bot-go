package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/AidanAllen/115bot-go/internal/p115"
)

// Config 保存运行期全部配置。所有字段均来自环境变量，启动时一次性校验，
// 校验不通过直接返回错误，由 main 快速失败退出。
type Config struct {
	// BotToken Telegram Bot Token，必填。
	BotToken string

	// APIEndpoint 自定义 Bot API 端点（访问官方不便时可指向自建反代）。
	// 留空则使用官方 https://api.telegram.org。格式必须含两个 %s 占位符：
	//   http://127.0.0.1:8081/bot%s/%s
	// 注意：换端点不能突破 20MB 限制，那是官方服务器的硬限制。
	APIEndpoint string

	// OwnerID 机器人所有者 Telegram 用户 ID。它只是「机器人归谁」的标记
	// （供后续 web 控制台 / 用户管理用），不限制任何用户使用。
	// 为 0 时，首个在机器人里点「绑定为我」或发 /setid 的人即成为所有者。
	OwnerID int64

	// DBPath SQLite 数据文件路径。
	DBPath string

	// TempDir 文件下载临时目录。
	TempDir string

	// DefaultUA 默认 User-Agent。多用户模型下每位用户的 UA 按用户存数据库，
	// 此值仅作为「该用户尚未设置」时的兜底。
	DefaultUA string

	// OfflineCID 兜底离线保存目录 cid（用户未设置时生效，可被运行时配置覆盖）。
	OfflineCID string

	// TransferCID 兜底转存保存目录 cid（用户未设置时生效，可被运行时配置覆盖）。
	TransferCID string

	// UploadChannel 兜底转存通道（网页 / App / 支付宝小程序 / 微信小程序），
	// 留空走默认通道；每位用户仍可用 /channel 自选覆盖。
	UploadChannel string

	// PollTimeout Telegram 长轮询超时。
	PollTimeout time.Duration

	// HTTPTimeout 访问 115 的超时。
	HTTPTimeout time.Duration

	// MaxDownloadBytes 单个文件下载上限（0 表示不限制）。
	MaxDownloadBytes int64

	// TGAPIID / TGAPIHash 来自 https://my.telegram.org/ 的应用凭据。
	//
	// 配了这两个，超过官方 20MB 限制的文件会改走 MTProto 直连下载
	// （不需要 Docker，Windows 上跑 exe 也有效）；
	// 不配就退回官方 Bot API，超过 20MB 的文件无法转存。
	TGAPIID   int
	TGAPIHash string

	// MTProtoSession MTProto 会话保存路径（含 auth key，属敏感数据，别外传）。
	MTProtoSession string

	// LogLevel debug / info / warn / error
	LogLevel string
}

const defaultAPIEndpoint = "https://api.telegram.org/bot%s/%s"

// Load 从环境变量读取并校验配置。
func Load() (*Config, error) {
	c := &Config{
		BotToken:         strings.TrimSpace(os.Getenv("BOT_TOKEN")),
		APIEndpoint:      strings.TrimSpace(os.Getenv("TG_API_ENDPOINT")),
		DBPath:           envOr("DB_PATH", "./data/115bot.db"),
		TempDir:          envOr("TEMP_DIR", "./data/tmp"),
		DefaultUA:        envOr("DEFAULT_UA", p115.DefaultUA),
		OfflineCID:       strings.TrimSpace(os.Getenv("OFFLINE_CID")),
		TransferCID:      strings.TrimSpace(os.Getenv("TRANSFER_CID")),
		UploadChannel:    strings.ToLower(strings.TrimSpace(os.Getenv("UPLOAD_CHANNEL"))),
		PollTimeout:      envDuration("POLL_TIMEOUT", 60*time.Second),
		HTTPTimeout:      envDuration("HTTP_TIMEOUT", 30*time.Second),
		MaxDownloadBytes: envInt64("MAX_DOWNLOAD_BYTES", 0),
		TGAPIID:          int(envInt64("TG_API_ID", 0)),
		TGAPIHash:        strings.TrimSpace(os.Getenv("TG_API_HASH")),
		MTProtoSession:   envOr("TG_MTPROTO_SESSION", "./data/tg.session.json"),
		LogLevel:         envOr("LOG_LEVEL", "info"),
	}

	if c.BotToken == "" {
		return nil, errors.New("缺少必填环境变量 BOT_TOKEN（Telegram 机器人 Token）")
	}
	if c.APIEndpoint == "" {
		c.APIEndpoint = defaultAPIEndpoint
	}
	if n := strings.Count(c.APIEndpoint, "%s"); n != 2 {
		return nil, fmt.Errorf("TG_API_ENDPOINT 必须包含两个 %%s 占位符，当前为 %d 个：%s", n, c.APIEndpoint)
	}
	if v := strings.TrimSpace(os.Getenv("OWNER_ID")); v != "" {
		id, err := strconv.ParseInt(v, 10, 64)
		if err != nil || id <= 0 {
			return nil, fmt.Errorf("OWNER_ID 必须是正整数，当前值：%s", v)
		}
		c.OwnerID = id
	}
	if c.OfflineCID != "" && !IsDigits(c.OfflineCID) {
		return nil, fmt.Errorf("OFFLINE_CID 必须是纯数字 cid，当前值：%s", c.OfflineCID)
	}
	if c.TransferCID != "" && !IsDigits(c.TransferCID) {
		return nil, fmt.Errorf("TRANSFER_CID 必须是纯数字 cid，当前值：%s", c.TransferCID)
	}
	if c.UploadChannel != "" {
		if _, ok := p115.LookupUploadChannel(c.UploadChannel); !ok {
			ids := make([]string, 0, len(p115.UploadChannels()))
			for _, ch := range p115.UploadChannels() {
				ids = append(ids, ch.ID)
			}
			return nil, fmt.Errorf("UPLOAD_CHANNEL 必须是以下之一：%s，当前值：%s",
				strings.Join(ids, " / "), c.UploadChannel)
		}
	}
	if c.MaxDownloadBytes < 0 {
		return nil, errors.New("MAX_DOWNLOAD_BYTES 不能为负数")
	}
	// 只填一半等于没配，而且比不配更难排查（用户以为开了大文件下载，其实没生效），
	// 所以这里直接快速失败。
	if (c.TGAPIID > 0) != (c.TGAPIHash != "") {
		return nil, errors.New("TG_API_ID 与 TG_API_HASH 必须同时配置（到 https://my.telegram.org/ 申请）")
	}
	return c, nil
}

// IsDigits 判断字符串是否为非空纯数字。
func IsDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func envOr(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func envDuration(key string, def time.Duration) time.Duration {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		return def
	}
	return d
}

func envInt64(key string, def int64) int64 {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}
