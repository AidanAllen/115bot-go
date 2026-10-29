package bot

import (
	"strings"
	"testing"
	"time"
)

// TestBatchRenderProgress 批量转存只发一条消息，每个文件在里面占一行。
//
// 用户要的就是这个：一次转存 6 图 + 2 视频只输出一条，而不是每个文件刷 4 条。
func TestBatchRenderProgress(t *testing.T) {
	b := newTransferBatch(1, 10, []batchItem{
		{name: "a.jpg", size: 1024},
		{name: "b.jpg", size: 2048},
		{name: "c.mp4", size: 1 << 30},
	})

	b.set(0, stateDone, "2s · 24.62 KB/s", "pick0")
	b.set(1, stateUploading, "预计 4 小时", "")

	got := b.render()
	for _, want := range []string{
		"正在转存", "3 个文件", "已完成 1",
		"1. ✅ a.jpg · 2s · 24.62 KB/s",
		"2. ⬆️ b.jpg · 预计 4 小时",
		"3. ⏳ c.mp4",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("渲染结果缺少 %q：\n%s", want, got)
		}
	}
}

// TestBatchHidesPickCode 批量消息不展示 pick_code：
// 8 个文件会在一条消息里堆 8 行提取码，把文件名和状态挤没了。
func TestBatchHidesPickCode(t *testing.T) {
	b := newTransferBatch(1, 0, []batchItem{{name: "a.jpg"}, {name: "b.jpg"}})
	b.set(0, stateDone, "1s · 10 KB/s", "pick000")
	b.set(1, stateDone, "2s · 20 KB/s", "pick111")

	got := b.render()
	if strings.Contains(got, "pick000") || strings.Contains(got, "pick111") || strings.Contains(got, "pick_code") {
		t.Fatalf("批量消息不该出现 pick_code：\n%s", got)
	}
	// 数据仍在，将来要展示随时能加回来
	if b.items[0].code != "pick000" {
		t.Fatal("pick_code 应从数据里丢掉，只是不展示")
	}
}

// TestBatchRenderDone 全部跑完后标题变成「转存完成」。
func TestBatchRenderDone(t *testing.T) {
	b := newTransferBatch(1, 0, []batchItem{{name: "a.jpg"}, {name: "b.mp4"}})
	b.set(0, stateDone, "1s · 10 KB/s", "p1")
	b.set(1, stateDone, "3s · 20 KB/s", "p2")

	got := b.render()
	if !strings.Contains(got, "转存完成") || !strings.Contains(got, "2 个文件") {
		t.Fatalf("完成态标题不对：\n%s", got)
	}
	if strings.Contains(got, "正在转存") {
		t.Fatalf("全部完成后不该还显示「正在转存」：\n%s", got)
	}
}

// TestBatchRenderFailed 有失败要在标题里报出来，别让用户以为全成了。
func TestBatchRenderFailed(t *testing.T) {
	b := newTransferBatch(1, 0, []batchItem{{name: "a.jpg"}, {name: "b.jpg"}})
	b.set(0, stateDone, "1s · 10 KB/s", "p1")
	b.set(1, stateFailed, "上传超时", "")

	got := b.render()
	if !strings.Contains(got, "1 个失败") || !strings.Contains(got, "❌ b.jpg · 上传超时") {
		t.Fatalf("失败态渲染不对：\n%s", got)
	}
}

// TestBatchSetIgnoresBadIndex 行号越界不能 panic：队列里混着不同批次的任务时容易发生。
func TestBatchSetIgnoresBadIndex(t *testing.T) {
	b := newTransferBatch(1, 0, []batchItem{{name: "a.jpg"}})
	b.set(-1, stateDone, "x", "")
	b.set(99, stateDone, "x", "")
	if b.items[0].state != stateQueued {
		t.Fatal("越界写入影响了正常行")
	}
}

// TestHumanDuration 耗时读数要像人话。
func TestHumanDuration(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{2 * time.Second, "2 秒"},
		{72 * time.Second, "1 分 12 秒"},
		{3700 * time.Second, "1 小时 1 分"},
	}
	for _, c := range cases {
		if got := humanDuration(c.d); got != c.want {
			t.Fatalf("%v 期望 %q，实际 %q", c.d, c.want, got)
		}
	}
}

// TestNameFromCaptionSkipsHashtags 纯 #标签行不能当文件名，要往下找真正的标题。
//
// 频道转发很常见：一行 #标签 占首位，真正的标题在后面一行。
func TestNameFromCaptionSkipsHashtags(t *testing.T) {
	cases := []struct {
		name    string
		caption string
		want    string
	}{
		{
			name:    "首行是标签就用下一行",
			caption: "#合集 #分享\n\n【测试专辑】2026 年度合辑",
			want:    "【测试专辑】2026 年度合辑.jpg",
		},
		{
			name:    "标题行尾部带标签时去掉标签",
			caption: "测试视频标题 #标签A #标签B",
			want:    "测试视频标题.jpg",
		},
		{
			name:    "整段只有标签则不当文件名",
			caption: "#a #b #c",
			want:    "",
		},
		{
			name:    "普通标题不受影响",
			caption: "想叫的名字",
			want:    "想叫的名字.jpg",
		},
		{
			name:    "磁力链仍不当文件名",
			caption: "magnet:?xt=urn:btih:abc\n好看的电影",
			want:    "好看的电影.jpg",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := nameFromCaption(c.caption, ".jpg"); got != c.want {
				t.Fatalf("期望 %q，实际 %q", c.want, got)
			}
		})
	}
}

// TestBatchEditThrottle 状态变化太密集时跳过刷新，但整批结束必须刷新。
func TestBatchEditThrottle(t *testing.T) {
	b := newTransferBatch(1, 0, []batchItem{{name: "a.jpg"}, {name: "b.jpg"}, {name: "c.jpg"}})

	if !b.claimRefresh() {
		t.Fatal("第一次刷新该放行")
	}
	if b.claimRefresh() {
		t.Fatal("紧接着的第二次应被限流拦下")
	}
	b.set(0, stateDone, "1s", "p")
	if b.claimRefresh() {
		t.Fatal("间隔不足仍应拦下")
	}
	b.lastEdit = time.Now().Add(-2 * time.Second)
	if !b.claimRefresh() {
		t.Fatal("间隔够了应放行")
	}
	// 全部结束：不管间隔多短都要刷新，否则用户看到的是过期中间态
	b.set(1, stateDone, "1s", "p")
	b.set(2, stateFailed, "失败", "")
	if !b.claimRefresh() {
		t.Fatal("整批结束时必须刷新")
	}
}
