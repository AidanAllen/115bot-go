package bot

import (
	"context"
	"sort"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// 媒体组（相册）说明文字缓冲。
//
// Telegram 会把一次「多图/多视频」发送拆成**多条独立消息**，
// 而说明文字（caption）只挂在其中一条上。这次的照片全被叫成
// photo_AQADxxx.jpg 就是这么来的：一个 5 文件的相册，说明挂在最后那条视频上，
// 前 4 张照片的 caption 是空的，只能落回兜底名。
//
// 所以带 media_group_id 的消息不立刻入队，先攒一小会儿，等同组消息到齐、
// 拿到说明文字后，再按「共享说明 + 同名排号」统一定名入队。
const (
	// mediaGroupWait 是收到组内新消息后的等待窗口。
	//
	// 实测一个相册的多条消息通常在 1 秒内到齐；2.5 秒足够，
	// 又短到用户几乎感觉不到「已加入队列」提示被推迟了。
	mediaGroupWait = 2500 * time.Millisecond
	// mediaGroupMaxWait 是从组内第一条消息算起的硬上限。
	// 超大相册（上限 10 个文件）会持续追加，不能无限续期，否则永远不入队。
	mediaGroupMaxWait = 10 * time.Second
	// groupCaptionTTL 是说明文字缓存的保留时间。
	// 留着是给「事后对组内某张图手动 /d」用的：那时组早已散掉，只能查缓存。
	groupCaptionTTL = 10 * time.Minute
)

// pendingGroup 是一个正在攒的媒体组。
type pendingGroup struct {
	msgs     []*tgbotapi.Message
	timer    *time.Timer
	deadline time.Time
}

// enqueueMediaGroup 把媒体组的一条消息放进缓冲区，攒齐后统一入队。
//
// 每条新消息都会把等待窗口续期，这样「先到照片、后到带说明的视频」
// 和「先到带说明的第一条」两种顺序都能覆盖。
func (a *App) enqueueMediaGroup(ac actor, m *tgbotapi.Message) {
	if m == nil || m.MediaGroupID == "" {
		return
	}
	key := m.MediaGroupID
	now := time.Now()

	a.groupMu.Lock()
	if a.groups == nil {
		a.groups = make(map[string]*pendingGroup)
	}
	g, ok := a.groups[key]
	if !ok {
		g = &pendingGroup{deadline: now.Add(mediaGroupMaxWait)}
		a.groups[key] = g
	}
	g.msgs = append(g.msgs, m)
	if g.timer != nil {
		g.timer.Stop()
	}
	wait := mediaGroupWait
	if remain := time.Until(g.deadline); remain < wait {
		wait = remain
	}
	if wait < 0 {
		wait = 0
	}
	g.timer = time.AfterFunc(wait, func() { a.flushMediaGroup(ac, key) })
	a.groupMu.Unlock()
}

// flushMediaGroup 把一个攒好的媒体组按消息顺序定名并逐个入队。
func (a *App) flushMediaGroup(ac actor, key string) {
	a.groupMu.Lock()
	g := a.groups[key]
	delete(a.groups, key)
	a.groupMu.Unlock()

	if g == nil || len(g.msgs) == 0 {
		return
	}
	caption := groupCaption(g.msgs)
	if caption != "" {
		a.rememberGroupCaption(key, caption)
	}

	// 整批按 message_id 升序排好，汇总消息里的行号才和用户在 Telegram 里看到的一致
	sorted := sortedByMessageID(g.msgs)
	names := groupFileNames(sorted)
	items := make([]batchItem, 0, len(sorted))
	for _, m := range sorted {
		_, _, sz := pickMedia(m)
		items = append(items, batchItem{name: names[m.MessageID], size: sz})
	}

	// 整批只发这一条消息：回复到相册第一条，之后每个文件的状态就地改写自己的那一行
	replyTo := 0
	if len(sorted) > 0 && sorted[0] != nil {
		replyTo = sorted[0].MessageID
	}
	batch := newTransferBatch(ac.ChatID, replyTo, items)
	batch.msgID = a.sendReplyID(ac.ChatID, replyTo, batch.render())
	if batch.msgID == 0 && a.log != nil {
		a.log.Warn("批量转存汇总消息发送失败，进度改为逐条发送")
		batch = nil
	}

	for i, m := range sorted {
		// rename 给算好的名字：组内排号已经在 groupFileNames 里定妥，
		// 这里再走一次 uniqueName 只是为了防止和历史任务里的文件撞名。
		a.enqueueTransferIn(context.Background(), ac, m, names[m.MessageID], batch, i)
	}
}

// sortedByMessageID 按消息 id 升序拷贝一份。
func sortedByMessageID(msgs []*tgbotapi.Message) []*tgbotapi.Message {
	out := make([]*tgbotapi.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			out = append(out, m)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].MessageID < out[j].MessageID })
	return out
}

// groupFileNames 给媒体组内每条消息定文件名：共享说明文字 + 同名自动排号。
//
// 返回 message_id → 文件名。按 message_id 升序排号，网盘里的顺序
// 才和用户在 Telegram 里看到的一致。
func groupFileNames(msgs []*tgbotapi.Message) map[int]string {
	sorted := make([]*tgbotapi.Message, 0, len(msgs))
	for _, m := range msgs {
		if m != nil {
			sorted = append(sorted, m)
		}
	}
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].MessageID < sorted[j].MessageID })

	caption := groupCaption(sorted)
	out := make(map[int]string, len(sorted))
	taken := make(map[string]bool, len(sorted))
	for _, m := range sorted {
		_, name, _ := pickMedia(withCaption(m, caption))
		name = sanitizeFileName(name)
		if name == "" {
			continue
		}
		name = uniqueName(name, taken)
		taken[name] = true
		out[m.MessageID] = name
	}
	return out
}

// groupCaption 取组内第一条非空说明文字（保留完整多行文本）。
//
// 组内只有一条带说明（Telegram 的行为），取第一条即唯一那条；
// 万一有多条，以顺序最靠前的为准。
// 不在这里截首行：挑哪一行当文件名由 nameFromCaption 决定（它要跳过纯 #标签行）。
func groupCaption(msgs []*tgbotapi.Message) string {
	for _, m := range msgs {
		if m == nil {
			continue
		}
		if c := strings.TrimSpace(m.Caption); c != "" {
			return c
		}
	}
	return ""
}

// withCaption 返回一条带指定说明的消息副本，原消息不动。
//
// 不直接改 m.Caption：同一条消息对象可能正被别的 goroutine 读着。
func withCaption(m *tgbotapi.Message, caption string) *tgbotapi.Message {
	if m == nil || caption == "" || m.Caption == caption {
		return m
	}
	cp := *m
	cp.Caption = caption
	return &cp
}

// rememberGroupCaption 缓存组内说明，供事后手动 /d 复用，到期自动丢弃。
func (a *App) rememberGroupCaption(groupID, caption string) {
	a.groupCaptions.Store(groupID, caption)
	time.AfterFunc(groupCaptionTTL, func() { a.groupCaptions.Delete(groupID) })
}

// withGroupCaption 给缺说明的组内消息补上缓存里的说明。
//
// 手动 /d 回复相册里某张图时，组早已 flush 完，只有缓存里还有说明。
func (a *App) withGroupCaption(m *tgbotapi.Message) *tgbotapi.Message {
	if m == nil || m.Caption != "" || m.MediaGroupID == "" {
		return m
	}
	v, ok := a.groupCaptions.Load(m.MediaGroupID)
	if !ok {
		return m
	}
	c, _ := v.(string)
	return withCaption(m, c)
}
