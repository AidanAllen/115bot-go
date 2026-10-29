//go:build windows

package main

import "os"

// shutdownSignals 需要优雅退出的信号。
//
// Windows 没有 Unix 那套 SIGTERM 语义：signal 包只可靠地转发 os.Interrupt
// （Ctrl+C 与控制台关闭）。syscall.SIGTERM 在 Windows 上虽然也有常量定义，
// 但系统不会真的投递，接了也没用（CTRL_CLOSE_EVENT 不经过 signal 包）。
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt}
}
