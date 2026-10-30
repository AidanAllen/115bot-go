package config

import (
	"os"
	"strings"
	"testing"
	"time"
)

// setenv 设置环境变量并返回恢复函数，保证用例之间互不串味。
// 传入空字符串表示 unset（清空），非空表示设置。
func setenv(t *testing.T, kv map[string]string) func() {
	t.Helper()
	var old = make(map[string]string, len(kv))
	for k, v := range kv {
		if cur, ok := os.LookupEnv(k); ok {
			old[k] = cur
		} else {
			old[k] = "" // 空串标记「原本不存在」，恢复时 unset
		}
		if v == "" {
			_ = os.Unsetenv(k)
		} else {
			_ = os.Setenv(k, v)
		}
	}
	return func() {
		for k, v := range old {
			// 无法区分「原本就是空串」与「原本不存在」，二者恢复为空效果一致
			if v == "" {
				_ = os.Unsetenv(k)
			} else {
				_ = os.Setenv(k, v)
			}
		}
	}
}

func TestLoad(t *testing.T) {
	// 每个用例前把本包关心的环境变量清干净，避免继承宿主环境。
	cleanAll := setenv(t, map[string]string{
		"BOT_TOKEN": "", "TG_API_ENDPOINT": "", "DB_PATH": "", "TEMP_DIR": "",
		"DEFAULT_UA": "", "OFFLINE_CID": "", "TRANSFER_CID": "", "UPLOAD_CHANNEL": "",
		"POLL_TIMEOUT": "", "HTTP_TIMEOUT": "", "MAX_DOWNLOAD_BYTES": "",
		"TG_API_ID": "", "TG_API_HASH": "", "TG_MTPROTO_SESSION": "", "OWNER_ID": "",
		"LOG_LEVEL": "",
	})
	defer cleanAll()

	tests := []struct {
		name    string
		env     map[string]string
		wantErr bool
		errSub  string // 错误信息里应包含的子串
		check   func(t *testing.T, c *Config)
	}{
		{
			name:    "缺少 BOT_TOKEN 直接失败",
			env:     map[string]string{},
			wantErr: true,
			errSub:  "BOT_TOKEN",
		},
		{
			name: "最小可用配置走默认值",
			env:  map[string]string{"BOT_TOKEN": "123:abc"},
			check: func(t *testing.T, c *Config) {
				if c.APIEndpoint != defaultAPIEndpoint {
					t.Errorf("APIEndpoint 默认值错误：%q", c.APIEndpoint)
				}
				if c.DBPath != "./data/115bot.db" {
					t.Errorf("DBPath 默认值错误：%q", c.DBPath)
				}
				if c.PollTimeout != 60*time.Second {
					t.Errorf("PollTimeout 默认值错误：%v", c.PollTimeout)
				}
				if c.HTTPTimeout != 30*time.Second {
					t.Errorf("HTTPTimeout 默认值错误：%v", c.HTTPTimeout)
				}
				if c.MaxDownloadBytes != 0 {
					t.Errorf("MaxDownloadBytes 默认值应为 0，得到 %d", c.MaxDownloadBytes)
				}
				if c.TGAPIID != 0 || c.TGAPIHash != "" {
					t.Errorf("未配置 TG_API_ID/HASH 时应为空，得到 %d / %q", c.TGAPIID, c.TGAPIHash)
				}
			},
		},
		{
			name: "自定义端点占位符数量错误",
			env: map[string]string{
				"BOT_TOKEN":       "123:abc",
				"TG_API_ENDPOINT": "http://127.0.0.1:8081/bot%s",
			},
			wantErr: true,
			errSub:  "占位符",
		},
		{
			name: "自定义端点合法（两个占位符）",
			env: map[string]string{
				"BOT_TOKEN":       "123:abc",
				"TG_API_ENDPOINT": "http://127.0.0.1:8081/bot%s/%s",
			},
			check: func(t *testing.T, c *Config) {
				if c.APIEndpoint != "http://127.0.0.1:8081/bot%s/%s" {
					t.Errorf("APIEndpoint 未保留：%q", c.APIEndpoint)
				}
			},
		},
		{
			name: "OWNER_ID 非数字失败",
			env: map[string]string{
				"BOT_TOKEN": "123:abc",
				"OWNER_ID":  "abc",
			},
			wantErr: true,
			errSub:  "OWNER_ID",
		},
		{
			name: "OWNER_ID 非正整数失败",
			env: map[string]string{
				"BOT_TOKEN": "123:abc",
				"OWNER_ID":  "-5",
			},
			wantErr: true,
			errSub:  "OWNER_ID",
		},
		{
			name: "OWNER_ID 合法解析",
			env: map[string]string{
				"BOT_TOKEN": "123:abc",
				"OWNER_ID":  "5251918892",
			},
			check: func(t *testing.T, c *Config) {
				if c.OwnerID != 5251918892 {
					t.Errorf("OwnerID = %d，期望 5251918892", c.OwnerID)
				}
			},
		},
		{
			name: "OFFLINE_CID 非纯数字失败",
			env: map[string]string{
				"BOT_TOKEN":   "123:abc",
				"OFFLINE_CID": "12a",
			},
			wantErr: true,
			errSub:  "OFFLINE_CID",
		},
		{
			name: "TRANSFER_CID 非纯数字失败",
			env: map[string]string{
				"BOT_TOKEN":    "123:abc",
				"TRANSFER_CID": "abc",
			},
			wantErr: true,
			errSub:  "TRANSFER_CID",
		},
		{
			name: "UPLOAD_CHANNEL 非法值失败",
			env: map[string]string{
				"BOT_TOKEN":      "123:abc",
				"UPLOAD_CHANNEL": "not-a-channel",
			},
			wantErr: true,
			errSub:  "UPLOAD_CHANNEL",
		},
		{
			name: "MAX_DOWNLOAD_BYTES 负数失败",
			env: map[string]string{
				"BOT_TOKEN":          "123:abc",
				"MAX_DOWNLOAD_BYTES": "-1",
			},
			wantErr: true,
			errSub:  "MAX_DOWNLOAD_BYTES",
		},
		{
			name: "TG_API_ID 与 TG_API_HASH 只配一半失败",
			env: map[string]string{
				"BOT_TOKEN": "123:abc",
				"TG_API_ID": "12345",
			},
			wantErr: true,
			errSub:  "TG_API_ID",
		},
		{
			name: "TG_API_ID 与 TG_API_HASH 成对配置成功",
			env: map[string]string{
				"BOT_TOKEN":   "123:abc",
				"TG_API_ID":   "12345",
				"TG_API_HASH": "deadbeef",
			},
			check: func(t *testing.T, c *Config) {
				if c.TGAPIID != 12345 || c.TGAPIHash != "deadbeef" {
					t.Errorf("TG 凭据解析错误：%d / %q", c.TGAPIID, c.TGAPIHash)
				}
			},
		},
		{
			name: "时长与字节数解析",
			env: map[string]string{
				"BOT_TOKEN":          "123:abc",
				"POLL_TIMEOUT":       "90s",
				"HTTP_TIMEOUT":       "45s",
				"MAX_DOWNLOAD_BYTES": "20971520",
			},
			check: func(t *testing.T, c *Config) {
				if c.PollTimeout != 90*time.Second {
					t.Errorf("PollTimeout = %v，期望 90s", c.PollTimeout)
				}
				if c.HTTPTimeout != 45*time.Second {
					t.Errorf("HTTPTimeout = %v，期望 45s", c.HTTPTimeout)
				}
				if c.MaxDownloadBytes != 20971520 {
					t.Errorf("MaxDownloadBytes = %d，期望 20971520", c.MaxDownloadBytes)
				}
			},
		},
		{
			name: "非法时长回退默认值",
			env: map[string]string{
				"BOT_TOKEN":    "123:abc",
				"POLL_TIMEOUT": "not-a-duration",
			},
			check: func(t *testing.T, c *Config) {
				if c.PollTimeout != 60*time.Second {
					t.Errorf("非法 POLL_TIMEOUT 应回退默认 60s，得到 %v", c.PollTimeout)
				}
			},
		},
		{
			name: "UPLOAD_CHANNEL 大小写归一化",
			env: map[string]string{
				"BOT_TOKEN":      "123:abc",
				"UPLOAD_CHANNEL": "WEB",
			},
			check: func(t *testing.T, c *Config) {
				if c.UploadChannel != "web" {
					t.Errorf("UPLOAD_CHANNEL 应归一化为小写，得到 %q", c.UploadChannel)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			restore := setenv(t, tt.env)
			defer restore()

			c, err := Load()
			if tt.wantErr {
				if err == nil {
					t.Fatalf("期望报错，却成功返回 %+v", c)
				}
				if tt.errSub != "" && !strings.Contains(err.Error(), tt.errSub) {
					t.Fatalf("错误信息 %q 未包含 %q", err.Error(), tt.errSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("不期望报错，却返回 %v", err)
			}
			if tt.check != nil {
				tt.check(t, c)
			}
		})
	}
}

func TestIsDigits(t *testing.T) {
	tests := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"0", true},
		{"12345", true},
		{"12a", false},
		{" 12", false}, // 前后空白不算
		{"12 ", false},
		{"-1", false},
		{"1.5", false},
		{"１", false}, // 全角数字不是 ASCII 数字
	}
	for _, tt := range tests {
		if got := IsDigits(tt.in); got != tt.want {
			t.Errorf("IsDigits(%q) = %v，期望 %v", tt.in, got, tt.want)
		}
	}
}
