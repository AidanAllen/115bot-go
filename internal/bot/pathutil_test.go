package bot

import (
	"path/filepath"
	"testing"
)

// TestIsAbsPath 绝对路径判断必须与运行平台无关。
//
// 本地 Bot API 给的是 POSIX 绝对路径；bot 可能跑在 Windows 上，
// 此时 filepath.IsAbs("/var/...") 为 false，会把本可直接读盘的文件
// 误判成相对路径（进而走一条拼不出地址的 HTTP 下载）。
func TestIsAbsPath(t *testing.T) {
	posix := []string{
		"/var/lib/telegram-bot-api/909/videos/file_1.mp4",
		"/tmp/x.jpg",
		"\\var\\lib\\telegram-bot-api\\file_1.mp4",
	}
	for _, p := range posix {
		if !isAbsPath(p) {
			t.Errorf("isAbsPath(%q) = false，应为 true（POSIX 绝对路径必须被认出）", p)
		}
	}

	rel := []string{
		"",
		"photos/file_1.jpg",
		"documents/file_2.pdf",
	}
	for _, p := range rel {
		if isAbsPath(p) {
			t.Errorf("isAbsPath(%q) = true，应为 false（相对路径）", p)
		}
	}

	// Windows 盘符路径：在 Windows 上 filepath.IsAbs 认得，
	// 在 Unix 上认不得 —— 断言「不报错且等于平台判断」，不强求跨平台一致。
	win := `C:\tgdata\file_1.mp4`
	if got, want := isAbsPath(win), filepath.IsAbs(win); got != want {
		t.Errorf("isAbsPath(%q) = %v，filepath.IsAbs = %v，两者应一致（盘符路径交给标准库）", win, got, want)
	}
}

// TestTrimLeadingSlash URL 拼接不能出现双斜杠，相对路径要保持原样。
func TestTrimLeadingSlash(t *testing.T) {
	cases := []struct{ in, want string }{
		{"/var/lib/tg/file_1.mp4", "var/lib/tg/file_1.mp4"},
		{"photos/file_1.jpg", "photos/file_1.jpg"},
		{"", ""},
	}
	for _, c := range cases {
		if got := trimLeadingSlash(c.in); got != c.want {
			t.Errorf("trimLeadingSlash(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}
