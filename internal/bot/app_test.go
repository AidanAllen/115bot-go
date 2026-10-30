package bot

import (
	"regexp"
	"testing"
)

// semverRE 匹配形如 1.0.3 的语义化版本号（不含 v 前缀，也不含预发布后缀）。
// 版本号是 ldflags 注入的目标与各构建脚本回读的唯一来源，格式错了会
// 让 build-all.sh / Makefile 的 grep 提取逻辑失效，所以用测试钉死。
var semverRE = regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+$`)

// TestVersionIsValidSemver 确保默认版本号符合预期格式。
func TestVersionIsValidSemver(t *testing.T) {
	if Version == "" {
		t.Fatal("Version 不能为空")
	}
	if !semverRE.MatchString(Version) {
		t.Fatalf("Version = %q 不符合 x.y.z 语义化版本格式", Version)
	}
}

// TestBuildTimeDefault 确保未注入时 BuildTime 有占位值而非空串，
// 这样运行期日志 / /v 命令不会打出空字段。
func TestBuildTimeDefault(t *testing.T) {
	if BuildTime == "" {
		t.Fatal("BuildTime 未注入时应为占位值，不能为空")
	}
}
