package tgfile

import (
	"context"
	"path/filepath"
	"testing"
)

func TestConfigEnabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  Config
		want bool
	}{
		{"凭据齐全", Config{AppID: 123, AppHash: "hash", BotToken: "token"}, true},
		{"缺 app_hash", Config{AppID: 123, BotToken: "token"}, false},
		{"缺 app_id", Config{AppHash: "hash", BotToken: "token"}, false},
		{"缺 bot token", Config{AppID: 123, AppHash: "hash"}, false},
		{"全空", Config{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := c.cfg.Enabled(); got != c.want {
				t.Fatalf("Enabled() = %v, want %v", got, c.want)
			}
		})
	}
}

func TestDownloadWithoutCredentials(t *testing.T) {
	c := New(Config{})
	if c.Enabled() {
		t.Fatal("空配置不应启用")
	}
	if _, err := c.Download(context.Background(), "whatever", filepath.Join(t.TempDir(), "a.bin")); err == nil {
		t.Fatal("未启用时 Download 必须返回错误")
	}
}

// TestDownloadRejectsBadFileID 验证 file_id 解析是「本地就失败、不碰网络」的。
//
// 这一点很重要：解析失败说明这一步纯本地解码（gotd 的 fileid.DecodeFileID），
// 所以这个用例既能测到错误路径，又不会真的去连 Telegram。
func TestDownloadRejectsBadFileID(t *testing.T) {
	c := New(Config{AppID: 123, AppHash: "hash", BotToken: "token"})
	_, err := c.Download(context.Background(), "not-a-file-id", filepath.Join(t.TempDir(), "a.bin"))
	if err == nil {
		t.Fatal("非法 file_id 必须报错")
	}
	if got := err.Error(); got == "" {
		t.Fatal("错误信息不应为空")
	}
}
