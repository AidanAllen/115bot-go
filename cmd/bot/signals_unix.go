//go:build unix

package main

import (
	"os"
	"syscall"
)

// shutdownSignals 需要优雅退出的信号。
//
// Unix 下容器/进程管理器靠 SIGTERM 终止进程（docker stop、systemd 都发它），
// 必须接住它才能关掉数据库、取消进行中的上传。
func shutdownSignals() []os.Signal {
	return []os.Signal{os.Interrupt, syscall.SIGTERM}
}
