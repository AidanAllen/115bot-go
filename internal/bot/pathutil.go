package bot

import (
	"path/filepath"
	"strings"
)

// isAbsPath 判断路径是否为绝对路径（POSIX 与 Windows 两种写法都认）。
//
// 不能直接上 filepath.IsAbs：它按**当前运行平台**的规则判断。
// 本地 Bot API（telegram-bot-api --local）返回的是它自己容器里的 POSIX 绝对路径
// （形如 /var/lib/telegram-bot-api/909/videos/file_1.mp4）。
// bot 跑在 Windows 上时，filepath.IsAbs("/var/...") 返回 false（Windows 要求盘符），
// 于是明明可以直接读盘的文件被误判成相对路径，白白多走一遍 HTTP 下载——
// 而那条 HTTP 链路在 --local 模式下根本拼不出可用地址。
//
// 所以这里「先认 POSIX 绝对路径，再交给 filepath.IsAbs 兜住 Windows 盘符/UNC 写法」，
// 与运行平台无关。
func isAbsPath(p string) bool {
	if p == "" {
		return false
	}
	// POSIX 绝对路径，以及 Windows 上常见的 "\var\lib\..." 写法
	if p[0] == '/' || p[0] == '\\' {
		return true
	}
	// Windows：C:\foo、\\server\share
	return filepath.IsAbs(p)
}

// trimLeadingSlash 去掉路径前导斜杠，用于拼 URL。
//
// 本地 Bot API 给的是绝对路径，直接拼进 URL 会出现双斜杠；
// 官方 API 给的是相对路径（photos/file_1.jpg），本来就没有斜杠。
// Windows 盘符路径（C:\...）不参与 URL 拼接，原样返回。
func trimLeadingSlash(p string) string {
	return strings.TrimLeft(p, "/")
}
