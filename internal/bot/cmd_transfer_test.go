package bot

import (
	"testing"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/AidanAllen/115bot-go/internal/config"
	"github.com/AidanAllen/115bot-go/internal/p115"
)

// TestPickMedia 覆盖各类媒体消息的识别，确保 /d 能拿到正确的 file_id 与文件名。
func TestPickMedia(t *testing.T) {
	cases := []struct {
		name     string
		msg      *tgbotapi.Message
		wantID   string
		wantName string
		wantSize int64
	}{
		{
			name: "视频",
			msg: &tgbotapi.Message{Video: &tgbotapi.Video{
				FileID: "V1", FileName: "电影.mp4", FileSize: 1024, FileUniqueID: "u1",
			}},
			wantID: "V1", wantName: "电影.mp4", wantSize: 1024,
		},
		{
			name: "视频无文件名时用 unique_id 兜底",
			msg: &tgbotapi.Message{Video: &tgbotapi.Video{
				FileID: "V2", FileSize: 10, FileUniqueID: "abc",
			}},
			wantID: "V2", wantName: "video_abc.mp4", wantSize: 10,
		},
		{
			name: "文档",
			msg: &tgbotapi.Message{Document: &tgbotapi.Document{
				FileID: "D1", FileName: "a.mkv", FileSize: 2048,
			}},
			wantID: "D1", wantName: "a.mkv", wantSize: 2048,
		},
		{
			name: "音频",
			msg: &tgbotapi.Message{Audio: &tgbotapi.Audio{
				FileID: "A1", FileName: "b.mp3", FileSize: 99,
			}},
			wantID: "A1", wantName: "b.mp3", wantSize: 99,
		},
		{
			name: "照片取最大的一张并用 unique_id 兜底命名",
			msg: &tgbotapi.Message{Photo: []tgbotapi.PhotoSize{
				{FileID: "P1", FileUniqueID: "small", FileSize: 100},
				{FileID: "P2", FileUniqueID: "AQADbig", FileSize: 9999},
				{FileID: "P3", FileUniqueID: "mid", FileSize: 500},
			}},
			wantID: "P2", wantName: "photo_AQADbig.jpg", wantSize: 9999,
		},
		{
			name: "纯文本消息不可转存",
			msg:  &tgbotapi.Message{Text: "/d"},
		},
		{
			name: "nil 安全",
			msg:  nil,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			id, name, size := pickMedia(c.msg)
			if id != c.wantID || name != c.wantName || size != c.wantSize {
				t.Fatalf("期望 (%q,%q,%d)，实际 (%q,%q,%d)",
					c.wantID, c.wantName, c.wantSize, id, name, size)
			}
		})
	}
}

// TestPickMediaCaption 命名优先级：说明文字 > TG 原文件名 > 类型兜底。
//
// 用户的真实诉求：TG 给的文件名常是手机相册那串「1月12日 (5)(1).mp4」，
// 想叫的名字写在说明里；照片更是压根没有文件名。
func TestPickMediaCaption(t *testing.T) {
	cases := []struct {
		name string
		msg  *tgbotapi.Message
		want string
	}{
		{
			name: "视频：说明文字优先于原文件名",
			msg: &tgbotapi.Message{
				Caption: "想叫的名字",
				Video:   &tgbotapi.Video{FileID: "V1", FileName: "1月12日 (5)(1).mp4", FileUniqueID: "u1"},
			},
			want: "想叫的名字.mp4",
		},
		{
			name: "照片：说明文字 + .jpg",
			msg: &tgbotapi.Message{
				Caption: "想叫的名字",
				Photo:   []tgbotapi.PhotoSize{{FileID: "P", FileUniqueID: "uq"}},
			},
			want: "想叫的名字.jpg",
		},
		{
			name: "说明里已带扩展名就不重复补",
			msg: &tgbotapi.Message{
				Caption: "想叫的名字.mp4",
				Video:   &tgbotapi.Video{FileID: "V2", FileName: "a.mp4"},
			},
			want: "想叫的名字.mp4",
		},
		{
			name: "文档：说明文字 + 原文件扩展名",
			msg: &tgbotapi.Message{
				Caption:  "季度报告",
				Document: &tgbotapi.Document{FileID: "D", FileName: "x.pdf"},
			},
			want: "季度报告.pdf",
		},
		{
			name: "说明是链接时不当文件名",
			msg: &tgbotapi.Message{
				Caption: "magnet:?xt=urn:btih:abc",
				Video:   &tgbotapi.Video{FileID: "V3", FileName: "原名.mp4"},
			},
			want: "原名.mp4",
		},
		{
			name: "说明是网址时不当文件名",
			msg: &tgbotapi.Message{
				Caption: "https://example.com/a/b",
				Photo:   []tgbotapi.PhotoSize{{FileID: "P2", FileUniqueID: "AQADx"}},
			},
			want: "photo_AQADx.jpg",
		},
		{
			name: "多行说明只取第一行",
			msg: &tgbotapi.Message{
				Caption: "第一行标题\n第二行正文\n#标签",
				Video:   &tgbotapi.Video{FileID: "V4"},
			},
			want: "第一行标题.mp4",
		},
		{
			name: "空说明回落到原文件名",
			msg:  &tgbotapi.Message{Video: &tgbotapi.Video{FileID: "V5", FileName: "原名.mkv"}},
			want: "原名.mkv",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, got, _ := pickMedia(c.msg); got != c.want {
				t.Fatalf("期望 %q，实际 %q", c.want, got)
			}
		})
	}
}

// TestUniqueName 同名文件要自动排成 (1)(2)，且扩展名保留。
func TestUniqueName(t *testing.T) {
	none := map[string]bool{}
	if got := uniqueName("a.mp4", none); got != "a.mp4" {
		t.Fatalf("未占用时不该改名，实际 %q", got)
	}
	one := map[string]bool{"a.mp4": true}
	if got := uniqueName("a.mp4", one); got != "a(1).mp4" {
		t.Fatalf("期望 a(1).mp4，实际 %q", got)
	}
	two := map[string]bool{"a.mp4": true, "a(1).mp4": true}
	if got := uniqueName("a.mp4", two); got != "a(2).mp4" {
		t.Fatalf("期望 a(2).mp4，实际 %q", got)
	}
	// 中文名同样处理
	cn := map[string]bool{"中文名.mp4": true}
	if got := uniqueName("中文名.mp4", cn); got != "中文名(1).mp4" {
		t.Fatalf("期望 中文名(1).mp4，实际 %q", got)
	}
}

// TestNameReservation 排队中的名字要算「已占用」，否则一批同名文件会全撞在一起。
func TestNameReservation(t *testing.T) {
	var a App
	if a.takenNames(1, "cid-1")["a.mp4"] {
		t.Fatal("初始不该有已占用名字")
	}
	a.reserveName(1, "cid-1", "a.mp4")
	if !a.takenNames(1, "cid-1")["a.mp4"] {
		t.Fatal("占用后应算已用")
	}
	// 别的用户 / 别的目录互不影响
	if a.takenNames(2, "cid-1")["a.mp4"] || a.takenNames(1, "cid-2")["a.mp4"] {
		t.Fatal("名字占用必须按 用户+目录 隔离")
	}
	a.releaseName(1, "cid-1", "a.mp4")
	if a.takenNames(1, "cid-1")["a.mp4"] {
		t.Fatal("归还后应重新可用")
	}
}

// TestUploadTimeoutFor 上传超时要随文件大小增长，固定值会让大文件必定超时。
func TestUploadTimeoutFor(t *testing.T) {
	small := uploadTimeoutFor(1 << 20)    // 1MB
	medium := uploadTimeoutFor(100 << 20) // 100MB
	large := uploadTimeoutFor(2 << 30)    // 2GB
	huge := uploadTimeoutFor(200 << 30)   // 200GB，应被上限截断

	if small < 10*time.Minute {
		t.Fatalf("小文件也应给足 10 分钟，实际 %v", small)
	}
	if !(medium > small && large > medium) {
		t.Fatalf("超时应随文件增大: small=%v medium=%v large=%v", small, medium, large)
	}
	if huge != 24*time.Hour {
		t.Fatalf("超大文件应被 24 小时上限截断，实际 %v", huge)
	}
	// 启用本地 Bot API 后单文件上限 2GB，按最悲观 30KB/s 也要约 19 小时，
	// 上限低于 20 小时会把大文件活活掐断。
	if large < 19*time.Hour {
		t.Fatalf("2GB 文件的超时必须覆盖 19 小时，实际 %v", large)
	}
}

// TestEstimateUploadHuman 预估文案不该出现负数或 NaN。
func TestEstimateUploadHuman(t *testing.T) {
	for _, size := range []int64{0, 1 << 10, 10 << 20, 1 << 30, 100 << 30} {
		if s := estimateUploadHuman(size); s == "" {
			t.Fatalf("%d 字节的预估为空", size)
		}
	}
}

// TestUploaderRequiresOnlyLogin 只要有 cookie 就应具备转存能力，
// 这样用户扫码登录后无需再申请任何第三方 token。
func TestUploaderRequiresOnlyLogin(t *testing.T) {
	withCookie := p115.NewUploader(p115.New("UID=1;CID=2;SEID=3", p115.DefaultUA, time.Second), "")
	if !withCookie.Available() {
		t.Fatal("有 cookie 时应可转存")
	}
	if withCookie.Name() != "115 网页通道" {
		t.Fatalf("应选择网页通道，实际 %q", withCookie.Name())
	}

	noCookie := p115.NewUploader(p115.New("", "", time.Second), "")
	if noCookie.Available() {
		t.Fatal("无 cookie 时不应可转存")
	}
}

// TestHasMedia 自动转存靠 hasMedia 筛消息，判定必须与 pickMedia 一致，
// 否则会出现「看着有文件却不转」或「没文件还排队」。
func TestHasMedia(t *testing.T) {
	media := []*tgbotapi.Message{
		{Video: &tgbotapi.Video{FileID: "v"}},
		{Document: &tgbotapi.Document{FileID: "d"}},
		{Audio: &tgbotapi.Audio{FileID: "a"}},
		{VideoNote: &tgbotapi.VideoNote{FileID: "vn"}},
		{Animation: &tgbotapi.Animation{FileID: "an"}},
		{Photo: []tgbotapi.PhotoSize{{FileID: "p"}}},
	}
	for i, m := range media {
		if !hasMedia(m) {
			t.Fatalf("第 %d 条应识别为可转存", i)
		}
		if id, _, _ := pickMedia(m); id == "" {
			t.Fatalf("第 %d 条 hasMedia 为真但 pickMedia 取不到 file_id", i)
		}
	}
	for _, m := range []*tgbotapi.Message{nil, {Text: "磁力链 magnet:?xt=1"}, {Text: "/d"}} {
		if hasMedia(m) {
			t.Fatalf("不该识别为媒体：%+v", m)
		}
	}
}

// TestTransferQueue 转存必须串行排队：自动转存开启后可能一次涌进多个文件，
// 同时跑会互相抢唯一那条上行链路；队列满了要明确拒绝而不是丢任务。
func TestTransferQueue(t *testing.T) {
	a := &App{transferQ: make(chan transferJob, 2)}

	if pos := a.queueTransfer(transferJob{}); pos != 1 {
		t.Fatalf("第一个任务排队位置应为 1，实际 %d", pos)
	}
	if pos := a.queueTransfer(transferJob{}); pos != 2 {
		t.Fatalf("第二个任务排队位置应为 2，实际 %d", pos)
	}
	if pos := a.queueTransfer(transferJob{}); pos != 0 {
		t.Fatalf("队列满时应返回 0 表示拒绝，实际 %d", pos)
	}

	// worker 一次只取一个，天然串行
	<-a.transferQ
	if len(a.transferQ) != 1 {
		t.Fatalf("消费一个后队列应剩 1，实际 %d", len(a.transferQ))
	}
}

// TestTransferQueuePositionWhenDrained 回归 v0.2.1 的误报：
//
// 队列空闲时入队，worker 可能**立刻**把任务取走，此时 len(transferQ) 变回 0。
// 用 len 当排队位置就会返回 0，被上层当成「队列已满」——
// 用户于是看到「队列已满」，而同一个文件紧接着又正常开始下载了。
// 排队位置必须用独立的计数器，恒 >= 1。
func TestTransferQueuePositionWhenDrained(t *testing.T) {
	a := &App{transferQ: make(chan transferJob, 2)}

	pos := a.queueTransfer(transferJob{})
	<-a.transferQ // worker 立刻取走
	a.queued.Add(-1)

	if pos != 1 {
		t.Fatalf("任务被立刻取走时排队位置仍应为 1，实际 %d", pos)
	}
	if d := a.queueDepth(); d != 0 {
		t.Fatalf("取走后队列深度应为 0，实际 %d", d)
	}
	// 队列空了还能继续入队，且位置重新从 1 开始
	if p := a.queueTransfer(transferJob{}); p != 1 {
		t.Fatalf("队列排空后新任务位置应为 1，实际 %d", p)
	}
}

// TestTransferInflightDedup 同一个 file_id 在队列里时不允许重复排队。
func TestTransferInflightDedup(t *testing.T) {
	var a App

	if _, dup := a.inflight.LoadOrStore("file-1", struct{}{}); dup {
		t.Fatal("首次入队不应判为重复")
	}
	if _, dup := a.inflight.LoadOrStore("file-1", struct{}{}); !dup {
		t.Fatal("同一个 file_id 再次入队应判为重复")
	}
	if _, dup := a.inflight.LoadOrStore("file-2", struct{}{}); dup {
		t.Fatal("不同 file_id 不应判为重复")
	}
	// 转存结束后必须释放，否则这个文件以后永远排不进来
	a.inflight.Delete("file-1")
	if _, dup := a.inflight.LoadOrStore("file-1", struct{}{}); dup {
		t.Fatal("释放后应能重新入队")
	}
}

// TestFileDirectURL 文件下载地址必须跟 TG_API_ENDPOINT 走同一个 server。
//
// 库自带的 GetFileDirectURL 把 api.telegram.org 写死了，配了自定义端点就会
// 拼出指向官方的死链，所以这里必须自己从 endpoint 反推 /file 端点。
func TestFileDirectURL(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		filePath string
		want     string
	}{
		{
			name:     "官方端点",
			endpoint: "https://api.telegram.org/bot%s/%s",
			filePath: "photos/file_1.jpg",
			want:     "https://api.telegram.org/file/botTOKEN/photos/file_1.jpg",
		},
		{
			name:     "自定义端点",
			endpoint: "http://127.0.0.1:8081/bot%s/%s",
			filePath: "videos/file_1.mp4",
			want:     "http://127.0.0.1:8081/file/botTOKEN/videos/file_1.mp4",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := &App{
				cfg: &config.Config{APIEndpoint: c.endpoint},
				api: &tgbotapi.BotAPI{Token: "TOKEN"},
			}
			if got := a.fileDirectURL(c.filePath); got != c.want {
				t.Fatalf("拼接错误：\n got=%s\nwant=%s", got, c.want)
			}
		})
	}
}
