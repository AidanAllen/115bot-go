package config

import (
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/ghs/115bot-go/internal/p115"
)

// Config 保存运行期全部配置。所有字段均来自环境变量，启动时一次性校验，
// 校验不通过直接返回错误，由 main 快速失败退出。
type Config struct {
	// BotToken Telegram Bot Token，必填。
	BotToken string

	// APIEndpoint 自定义 Bot API 端点（用于对接 telegram-bot-api 本地服务）。
	// 留空则使用官方 https://api.telegram.org。格式必须含两个 %s 占位符：
	//   http://127.0.0.1:8081/bot%s/%s
	APIEndpoint string

	// OwnerID 初始管理员 Telegram 用户 ID。为 0 时首次 /setid 的人即成为管理员。
	OwnerID int64

	// DBPath SQLite 数据文件路径。
	DBPath string

	// TempDir 文件下载临时目录。
	TempDir string

	// DefaultUA 默认 User-Agent，写入 115 账号配置时作为初始值。
	DefaultUA string

	// OfflineCID 默认离线保存目录 cid（可被运行时配置覆盖）。
	OfflineCID string

	// TransferCID 默认转存保存目录 cid（可被运行时配置覆盖）。
	TransferCID string

	// UploadChannel 默认转存通道（网页 / App / 支付宝小程序 / 微信小程序），留空走默认通道。
	UploadChannel string

	// PollTimeout Telegram 长轮询超时。
	PollTimeout time.Duration

	// HTTPTimeout 访问 115 的超时。
	HTTPTimeout time.Duration

	// MaxDownloadBytes 单个文件下载上限（0 表示不限制）。
	MaxDownloadBytes int64

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
