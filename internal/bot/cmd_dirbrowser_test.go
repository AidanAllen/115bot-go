package bot

import (
	"testing"

	"github.com/AidanAllen/115bot-go/internal/store"
)

// TestDirCallbackRoundTrip 浏览器按钮生成的回调必须能被自己解析回来，
// 否则就是「点了没反应」。同时守住 TG 的 64 字节上限。
func TestDirCallbackRoundTrip(t *testing.T) {
	longCID := "3550559509531329963" // 线上真实 cid 长度
	cases := []struct {
		op, target, cid string
		page            int
	}{
		{"e", dirTargetTransfer, longCID, 0},
		{"s", dirTargetTransfer, longCID, 0},
		{"p", dirTargetTransfer, longCID, 3},
		{"e", dirTargetOffline, longCID, 0},
		{"m", dirTargetOffline, "", 0},
		{"e", dirTargetTransfer, "0", 0},
	}
	for _, c := range cases {
		data := dirData(c.op, c.target, c.cid, c.page)
		if len(data) > 64 {
			t.Fatalf("回调数据超过 TG 的 64 字节上限（%d）：%s", len(data), data)
		}
		got, ok := parseDirCallback(data)
		if !ok {
			t.Fatalf("无法解析自己生成的回调：%s", data)
		}
		if got.Op != c.op || got.Target != c.target || got.CID != c.cid || got.Page != c.page {
			t.Fatalf("往返失真：%+v → %+v", c, got)
		}
	}
}

// TestParseDirCallbackRejects 非法数据要拒绝，不能落到默认分支被当成别的操作。
func TestParseDirCallbackRejects(t *testing.T) {
	bad := []string{
		"",
		"d:",
		"d:x:t:123",
		"d:e:z:123",        // 非法目标
		"d:p:t:123:notnum", // 页码非数字
		"d:p:t:123:-1",     // 负页码
		"act:transfercid",  // 其它前缀不应被吃掉
		"ua:alipaymini",
	}
	for _, s := range bad {
		if _, ok := parseDirCallback(s); ok {
			t.Fatalf("本应拒绝，却解析成功：%q", s)
		}
	}
}

// TestDirTargetMapping 目标标识与配置键、输入动作的对应关系不能错位，
// 错一个就会把转存目录写进离线 cid。
func TestDirTargetMapping(t *testing.T) {
	if dirTargetKey(dirTargetTransfer) != store.KeyTransferCID {
		t.Fatal("转存目标应映射到转存 cid 键")
	}
	if dirTargetKey(dirTargetOffline) != store.KeyOfflineCID {
		t.Fatal("离线目标应映射到离线 cid 键")
	}
	if dirAction(dirTargetTransfer) != ActionTransferCID {
		t.Fatal("转存目标应进入转存输入态")
	}
	if dirAction(dirTargetOffline) != ActionOfflineCID {
		t.Fatal("离线目标应进入离线输入态")
	}
	// 未知目标要回落到转存，而不是空值
	if dirTargetKey("zzz") != store.KeyTransferCID {
		t.Fatal("未知目标应回落为转存")
	}
}
