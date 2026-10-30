package tgfile

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"
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

// memWriterAt 是最小的 io.WriterAt 实现：不碰磁盘、不碰网络。
type memWriterAt struct {
	mu sync.Mutex
	b  []byte
}

func (m *memWriterAt) WriteAt(p []byte, off int64) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	need := int(off) + len(p)
	if need > len(m.b) {
		grown := make([]byte, need)
		copy(grown, m.b)
		m.b = grown
	}
	copy(m.b[off:], p)
	return len(p), nil
}

func (m *memWriterAt) size() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.b)
}

// withFastStallCheck 把检查间隔调小，避免每个用例跑 15 秒。
func withFastStallCheck(t *testing.T) {
	t.Helper()
	old := stallCheckInterval
	stallCheckInterval = 5 * time.Millisecond
	t.Cleanup(func() { stallCheckInterval = old })
}

// TestCountingWriterAtCounts 验证写入字节数与最后写入时间都被记录。
func TestCountingWriterAtCounts(t *testing.T) {
	m := &memWriterAt{}
	pw := &countingWriterAt{w: m, last: time.Now().Add(-time.Hour)}

	before := pw.lastWrite()
	if _, err := pw.WriteAt([]byte("hello"), 0); err != nil {
		t.Fatalf("WriteAt 失败: %v", err)
	}
	if _, err := pw.WriteAt([]byte("world"), 5); err != nil {
		t.Fatalf("WriteAt 失败: %v", err)
	}

	if got := pw.total(); got != 10 {
		t.Errorf("累计字节 = %d，期望 10", got)
	}
	if got := m.size(); got != 10 {
		t.Errorf("底层写入 = %d 字节，期望 10", got)
	}
	if !pw.lastWrite().After(before) {
		t.Error("最后写入时间未更新")
	}
}

// TestWatchStallCancelsWhenIdle 验证「一直没有新字节」会被判定为卡死并取消。
//
// 这是 2026-10-30 线上事故的回归测试：一个 mp4 走了 MTProto 后一个字节都收不到，
// 下载不返回错误，串行 worker 被占死，60 个任务在队列里干等。
func TestWatchStallCancelsWhenIdle(t *testing.T) {
	withFastStallCheck(t)

	ctx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()

	pw := &countingWriterAt{w: &memWriterAt{}, last: time.Now()}
	stalled := make(chan struct{})
	canceled := make(chan struct{})
	var once sync.Once
	cancel := func() { once.Do(func() { close(canceled) }); cancelAll() }

	go watchStall(ctx, pw, 20*time.Millisecond, stalled, cancel)

	select {
	case <-canceled:
		// 期望：无数据写入 → 取消
	case <-time.After(2 * time.Second):
		t.Fatal("长时间无数据却没有取消下载，卡死会拖住整条队列")
	}

	select {
	case <-stalled:
	default:
		t.Error("取消时应同时关闭 stalled，便于上层给出「停滞」而非普通错误")
	}
}

// TestWatchStallKeepsAliveWhileProgressing 验证有进度时不会被误杀。
func TestWatchStallKeepsAliveWhileProgressing(t *testing.T) {
	withFastStallCheck(t)

	ctx, cancelAll := context.WithCancel(context.Background())
	defer cancelAll()

	pw := &countingWriterAt{w: &memWriterAt{}, last: time.Now()}
	stalled := make(chan struct{})
	canceled := make(chan struct{})
	var once sync.Once
	cancel := func() { once.Do(func() { close(canceled) }); cancelAll() }

	go watchStall(ctx, pw, 50*time.Millisecond, stalled, cancel)

	// 持续写入 300ms，远超 50ms 的停滞阈值，但不该被判卡死
	deadline := time.Now().Add(300 * time.Millisecond)
	for time.Now().Before(deadline) {
		if _, err := pw.WriteAt([]byte("x"), 0); err != nil {
			t.Fatalf("WriteAt 失败: %v", err)
		}
		time.Sleep(10 * time.Millisecond)
	}

	select {
	case <-canceled:
		t.Fatal("有持续进度却取消了下载，会在慢链路上误杀正常传输")
	default:
	}
}
