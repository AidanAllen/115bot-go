package bot

import (
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// 中性示例：真实用户的说明文字（含成人内容）不应进代码库
const albumCaption = "【测试相册】2026 年度合集"

// albumMsgs 造一个「4 张照片 + 1 个视频」的相册，说明文字挂在指定那条上。
// 这正是用户实际遇到的场景：说明只挂在最后一条视频上，前 4 张照片 caption 为空。
func albumMsgs(captionOnLast bool) []*tgbotapi.Message {
	msgs := make([]*tgbotapi.Message, 0, 5)
	ids := []int{101, 102, 103, 104, 105}
	for i, id := range ids {
		m := &tgbotapi.Message{MessageID: id, MediaGroupID: "album-1"}
		if i == 4 {
			m.Video = &tgbotapi.Video{FileID: "V1", FileUniqueID: "v1", FileSize: 1024}
		} else {
			m.Photo = []tgbotapi.PhotoSize{{FileID: "P" + string(rune('0'+i)), FileUniqueID: "AQAD00" + string(rune('0'+i)), FileSize: 14000}}
		}
		msgs = append(msgs, m)
	}
	if captionOnLast {
		msgs[4].Caption = albumCaption
	} else {
		msgs[0].Caption = albumCaption
	}
	return msgs
}

// TestGroupFileNamesShareCaption 相册里所有文件共用同一段说明文字，
// 同名按 message_id 顺序排 (1)(2)(3)。
//
// 这条测试钉死的是用户报的那个问题：说明挂在视频上，照片却叫 photo_AQADxxx.jpg。
func TestGroupFileNamesShareCaption(t *testing.T) {
	for _, captionOnLast := range []bool{true, false} {
		msgs := albumMsgs(captionOnLast)
		got := groupFileNames(msgs)

		want := map[int]string{
			101: albumCaption + ".jpg",
			102: albumCaption + "(1).jpg",
			103: albumCaption + "(2).jpg",
			104: albumCaption + "(3).jpg",
			105: albumCaption + ".mp4",
		}
		for id, w := range want {
			if got[id] != w {
				t.Fatalf("说明在最后一条=%v，消息 %d 期望 %q，实际 %q",
					captionOnLast, id, w, got[id])
			}
		}
		if len(got) != len(want) {
			t.Fatalf("期望 %d 个文件，实际 %d 个", len(want), len(got))
		}
	}
}

// TestGroupFileNamesWithoutCaption 整组都没有说明时保持兜底名，且互不重复。
func TestGroupFileNamesWithoutCaption(t *testing.T) {
	msgs := albumMsgs(true)
	for _, m := range msgs {
		m.Caption = ""
	}
	got := groupFileNames(msgs)

	seen := map[string]bool{}
	for id, name := range got {
		if name == "" {
			t.Fatalf("消息 %d 没算出文件名", id)
		}
		if seen[name] {
			t.Fatalf("兜底名重复：%q", name)
		}
		seen[name] = true
	}
	if got[101] != "photo_AQAD000.jpg" {
		t.Fatalf("照片兜底名不对：%q", got[101])
	}
	if got[105] != "video_v1.mp4" {
		t.Fatalf("视频兜底名不对：%q", got[105])
	}
}

// TestGroupCaption 组内说明取顺序最靠前的那条非空文字，保留完整多行文本
// （挑哪一行当文件名由 nameFromCaption 决定，它要跳过纯 #标签行）。
func TestGroupCaption(t *testing.T) {
	cases := []struct {
		name string
		msgs []*tgbotapi.Message
		want string
	}{
		{
			name: "多行说明整段保留",
			msgs: []*tgbotapi.Message{
				{MessageID: 1, Caption: "标题\n正文正文\n#标签"},
			},
			want: "标题\n正文正文\n#标签",
		},
		{
			name: "跳过空说明",
			msgs: []*tgbotapi.Message{
				{MessageID: 1},
				{MessageID: 2, Caption: "第二个才有"},
			},
			want: "第二个才有",
		},
		{
			name: "全部没有说明",
			msgs: []*tgbotapi.Message{{MessageID: 1}},
			want: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := groupCaption(c.msgs); got != c.want {
				t.Fatalf("期望 %q，实际 %q", c.want, got)
			}
		})
	}
}

// TestWithCaptionDoesNotMutate 补说明不能改到原消息：
// 同一条消息对象可能正被别的 goroutine 读取。
func TestWithCaptionDoesNotMutate(t *testing.T) {
	m := &tgbotapi.Message{MessageID: 7, Photo: []tgbotapi.PhotoSize{{FileID: "P"}}}
	got := withCaption(m, "新名字")
	if m.Caption != "" {
		t.Fatal("原消息被改了")
	}
	if got.Caption != "新名字" {
		t.Fatalf("副本没带上说明：%q", got.Caption)
	}
}

// TestWithGroupCaption 事后对相册里某张图手动 /d 时，说明要从缓存里补回来。
func TestWithGroupCaption(t *testing.T) {
	a := &App{}
	a.rememberGroupCaption("album-9", "相册说明")

	m := &tgbotapi.Message{MessageID: 3, MediaGroupID: "album-9"}
	if got := a.withGroupCaption(m); got.Caption != "相册说明" {
		t.Fatalf("组内消息没补上说明：%q", got.Caption)
	}
	// 自己带说明的、不在组里的、没缓存的，都原样返回
	own := &tgbotapi.Message{MessageID: 4, MediaGroupID: "album-9", Caption: "自带"}
	if got := a.withGroupCaption(own); got != own {
		t.Fatal("自带说明的消息不该被替换")
	}
	other := &tgbotapi.Message{MessageID: 5, MediaGroupID: "album-404"}
	if got := a.withGroupCaption(other); got != other {
		t.Fatal("没有缓存的组不该被替换")
	}
}

// TestEnqueueMediaGroupBuffers 相册消息要先攒着，攒齐前不入队。
//
// 攒齐前就入队的话，前面的文件永远等不到后面那条消息带来的说明文字。
func TestEnqueueMediaGroupBuffers(t *testing.T) {
	a := &App{groups: make(map[string]*pendingGroup)}
	t.Cleanup(func() { a.stopGroupTimers() })

	ac := actor{UserID: 1, ChatID: 1}
	for _, m := range albumMsgs(true) {
		a.enqueueMediaGroup(ac, m)
	}

	a.groupMu.Lock()
	g, ok := a.groups["album-1"]
	n := 0
	if ok {
		n = len(g.msgs)
	}
	a.groupMu.Unlock()

	if !ok {
		t.Fatal("组没有建立")
	}
	if n != 5 {
		t.Fatalf("期望攒下 5 条消息，实际 %d 条", n)
	}
	if a.queueDepth() != 0 {
		t.Fatalf("攒齐前不应入队，实际队列深度 %d", a.queueDepth())
	}
}

// TestEnqueueMediaGroupIgnoresSingle 单条消息不走缓冲，避免平白多等 2.5 秒。
func TestEnqueueMediaGroupIgnoresSingle(t *testing.T) {
	a := &App{groups: make(map[string]*pendingGroup)}
	a.enqueueMediaGroup(actor{UserID: 1}, &tgbotapi.Message{MessageID: 1, Video: &tgbotapi.Video{}})
	a.groupMu.Lock()
	n := len(a.groups)
	a.groupMu.Unlock()
	if n != 0 {
		t.Fatalf("没有 media_group_id 的消息不该进缓冲，实际 %d 组", n)
	}
}

// stopGroupTimers 停掉所有待触发的定时器，避免测试结束后还在跑。
func (a *App) stopGroupTimers() {
	a.groupMu.Lock()
	defer a.groupMu.Unlock()
	for _, g := range a.groups {
		if g.timer != nil {
			g.timer.Stop()
		}
	}
	a.groups = make(map[string]*pendingGroup)
}
