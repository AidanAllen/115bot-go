package bot

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/AidanAllen/115bot-go/internal/p115"
	"github.com/AidanAllen/115bot-go/internal/store"
)

// handleAutoLinks 自动识别消息中的磁力链 / ed2k 并下发到**发起者自己**的 115。
func (a *App) handleAutoLinks(ctx context.Context, m *tgbotapi.Message) {
	text := m.Text
	if text == "" {
		text = m.Caption
	}
	if text == "" {
		return
	}

	links := p115.ExtractLinks(text)
	if len(links) == 0 {
		return
	}

	ac := actorOf(m)
	if !a.userEnabled(ac.UserID) {
		a.send(ac.ChatID, TxtOfflineDisabled)
		return
	}

	cid := a.userOfflineCID(ac.UserID)
	if cid == "" {
		a.send(ac.ChatID, TxtOfflineNeedCID)
		return
	}
	// 没登录 115 的人不能下发：离线任务会落到别人的网盘或干脆失败
	c, ok := a.requireClient(ac)
	if !ok {
		return
	}
	urls := make([]string, 0, len(links))
	for _, l := range links {
		urls = append(urls, l.URL)
	}

	a.send(ac.ChatID, fmt.Sprintf(TxtOfflineSubmitting, len(urls)))

	taskID, err := a.st.CreateTask(ac.UserID, store.KindOffline, cut(strings.Join(urls, "\n"), 500), "", cid)
	if err != nil {
		a.log.Warn("写入离线任务记录失败", "err", err)
	}
	if taskID > 0 {
		_ = a.st.UpdateTask(taskID, store.StatusRunning, "")
	}

	ctx, cancel := context.WithTimeout(ctx, a.cfg.HTTPTimeout*2)
	defer cancel()

	items, err := c.AddOfflineURLs(ctx, urls, cid)
	if err != nil {
		if taskID > 0 {
			_ = a.st.UpdateTask(taskID, store.StatusFailed, err.Error())
		}
		a.send(ac.ChatID, fmt.Sprintf(TxtInternalErr, escapeHTML(friendlyErr(err))))
		return
	}

	// 汇总明细
	var detail strings.Builder
	success := 0
	for _, it := range items {
		if it.State {
			success++
			fmt.Fprintf(&detail, "✅ <code>%s</code>\n", escapeHTML(cut(it.URL, 60)))
			continue
		}
		fmt.Fprintf(&detail, "❌ <code>%s</code> %s\n",
			escapeHTML(cut(it.URL, 60)), escapeHTML(orDefault(it.Message(), TxtOfflineUnknown)))
	}

	if taskID > 0 {
		if success > 0 {
			_ = a.st.UpdateTask(taskID, store.StatusSuccess, "")
		} else {
			_ = a.st.UpdateTask(taskID, store.StatusFailed, TxtOfflineAllFailed)
		}
	}

	body := orDefault(detail.String(), TxtOfflineNoDetail)
	a.send(ac.ChatID, fmt.Sprintf(TxtOfflineResult, success, len(items)-success, body))
}

// enqueueTransfer 把一个媒体消息排进转存队列。
//
// target 是携带媒体的那条消息；rename 非空时用作落盘文件名。
// 只做「能不能转」的检查与解析，真正上传由队列 worker 串行执行——
// 自动转存开启后可能一次来好几个文件，逐个同步执行会互相抢带宽。
func (a *App) enqueueTransfer(ctx context.Context, ac actor, target *tgbotapi.Message, rename string) {
	a.enqueueTransferIn(ctx, ac, target, rename, nil, 0)
}

// enqueueTransferIn 排一个转存任务。batch 非空表示它属于某一批，
// 进度会汇总到 batch 那一条消息里（index 是行号），不再单独发消息。
func (a *App) enqueueTransferIn(ctx context.Context, ac actor, target *tgbotapi.Message, rename string, batch *transferBatch, index int) {
	if !a.userEnabled(ac.UserID) {
		a.send(ac.ChatID, TxtTransferDisabled)
		return
	}
	// 手动对相册里某个文件 /d 时，说明可能挂在同组另一条消息上，先补回来
	target = a.withGroupCaption(target)
	fileID, fileName, size := pickMedia(target)
	if fileID == "" {
		a.send(ac.ChatID, TxtTransferNoFile)
		return
	}

	replyTo := 0
	if target != nil {
		replyTo = target.MessageID
	}

	// 未登录 115 直接挡住：不登录就转存，文件没有落点
	if !a.userLoggedIn(ac.UserID) {
		a.send(ac.ChatID, TxtNeed115Login)
		return
	}
	if !a.userUploader(ac.UserID).Available() {
		a.send(ac.ChatID, TxtTransferNoUploader)
		return
	}
	cid := a.userTransferCID(ac.UserID)
	if cid == "" {
		a.send(ac.ChatID, TxtNeedTransferCID)
		return
	}

	// 同一个文件已在队列里就不再重复排队。
	//
	// Telegram 偶尔重投同一条 update，自动转存又不需要用户确认，
	// 不去重的话同一个文件会被转存两遍（用户侧看到两轮进度消息，网盘里两份同名文件）。
	if _, dup := a.inflight.LoadOrStore(fileID, struct{}{}); dup {
		a.sendReply(ac.ChatID, replyTo, TxtTransferDuplicate)
		return
	}

	if rename != "" {
		fileName = sanitizeFileName(rename)
	} else {
		fileName = sanitizeFileName(fileName)
	}
	if fileName == "" {
		fileName = fmt.Sprintf("tg_%d.bin", time.Now().Unix())
	}
	// 同名自动排开：一批文件同名时，后面的加 (1)(2)，
	// 否则网盘里要么互相顶掉、要么被 115 自己改名，用户找不到自己发的那个。
	fileName = uniqueName(fileName, a.takenNames(ac.UserID, cid))
	a.reserveName(ac.UserID, cid, fileName)

	// 批量时整批共用 batch 那一条消息，不需要再单发「已加入队列」；
	// 单文件则先发一条并记住 id，之后下载、上传、成功全部就地改写它。
	progressMsgID := 0
	if batch == nil {
		progressMsgID = a.sendReplyID(ac.ChatID, replyTo,
			fmt.Sprintf(TxtTransferQueued, escapeHTML(fileName), humanSize(size), a.queueDepth()+1))
	}

	pos := a.queueTransfer(transferJob{
		ac: ac, fileID: fileID, fileName: fileName, size: size,
		replyTo: replyTo, cid: cid, progressMsgID: progressMsgID,
		batch: batch, index: index,
	})
	if pos == 0 {
		// 入队失败要把去重标记与名字占位一起撤掉，否则之后永远排不进来
		a.inflight.Delete(fileID)
		a.releaseName(ac.UserID, cid, fileName)
		a.log.Warn("转存队列已满，拒绝新任务", "user_id", ac.UserID, "file", fileName, "上限", transferQueueSize)
		if batch != nil {
			batch.set(index, stateFailed, "队列已满", "")
			a.editMessage(batch.chatID, batch.msgID, batch.render())
		} else {
			// 单文件已经发过「已加入队列」，就地改写那一条：
			// 再补一条新消息会让用户同时看到「已加入队列」和「队列已满」两句互相矛盾的话。
			a.report(transferJob{ac: ac, replyTo: replyTo, progressMsgID: progressMsgID},
				fmt.Sprintf(TxtTransferQueueFull, transferQueueSize, a.queueDepth()))
		}
		return
	}
	a.log.Info("转存入队", "user_id", ac.UserID, "file", fileName, "位置", pos, "待处理", a.queueDepth())
}

// runTransfer 真正执行一次转存：下载 → 上传 → 回报。
// 由队列 worker 串行调用，同一时刻只会有一个在跑。
func (a *App) runTransfer(ctx context.Context, job transferJob) {
	ac := job.ac
	fileName, fileID, size := job.fileName, job.fileID, job.size
	// 无论成败都归还这个名字：失败的文件并不存在，名字可以再用
	defer a.releaseName(ac.UserID, job.cid, fileName)

	up := a.userUploader(ac.UserID)
	if !up.Available() {
		a.notify(job, stateFailed, TxtTransferNoUploader, "")
		return
	}
	cid := a.userTransferCID(ac.UserID)
	if cid == "" {
		a.notify(job, stateFailed, TxtNeedTransferCID, "")
		return
	}

	start := time.Now()
	taskID, err := a.st.CreateTask(ac.UserID, store.KindTransfer, fileID, fileName, cid)
	if err != nil {
		a.log.Warn("写入转存任务失败", "err", err)
	}
	if taskID > 0 {
		_ = a.st.UpdateTask(taskID, store.StatusRunning, "")
	}

	localPath, err := a.fetchToLocal(ctx, job)
	if err != nil {
		if taskID > 0 {
			_ = a.st.UpdateTask(taskID, store.StatusFailed, err.Error())
		}
		a.notify(job, stateFailed, err.Error(), "")
		return
	}
	// 用完即删：临时副本不删会堆积
	defer func() {
		if rerr := os.Remove(localPath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			a.log.Warn("清理转存临时文件失败", "path", localPath, "err", rerr)
		}
	}()

	if fi, statErr := os.Stat(localPath); statErr == nil {
		size = fi.Size()
	}

	// 上传可能很久（受机房上行带宽限制），提前告知预估耗时，避免用户以为卡死
	a.notify(job, stateUploading, estimateUploadHuman(size), "")

	// 上传超时按文件大小估算：跨境链路实测低至 30KB/s，
	// 固定 10 分钟会让几十 MB 的视频必定超时。
	uctx, cancel := context.WithTimeout(ctx, uploadTimeoutFor(size))
	defer cancel()

	pickCode, err := up.Upload(uctx, localPath, fileName, size, cid)
	if err != nil {
		if taskID > 0 {
			_ = a.st.UpdateTask(taskID, store.StatusFailed, err.Error())
		}
		a.notify(job, stateFailed, friendlyErr(err), "")
		return
	}

	if taskID > 0 {
		_ = a.st.UpdateTask(taskID, store.StatusSuccess, "")
	}
	// 报出实测平均速度：慢的时候用户能一眼看出是链路限速还是卡住了
	elapsed := int(time.Since(start).Seconds())
	avg := humanSize(int64(float64(size) / math.Max(float64(elapsed), 0.001)))
	// 单条消息放得下完整信息；批量时那一行只放「耗时 · 速度」，避免行太长
	detail := fmt.Sprintf("方式：%s，耗时 %ds，平均 %s/s", up.Name(), elapsed, avg)
	if job.batch != nil {
		detail = fmt.Sprintf("%ds · %s/s", elapsed, avg)
	}
	a.notify(job, stateDone, detail, pickCode)
}

// estimateUploadHuman 给出人类可读的上传耗时预估。
//
// 按 100KB/s 估中位数（实测 30~170KB/s 波动），只用于安抚预期，不作为超时依据。
func estimateUploadHuman(size int64) string {
	const typicalBps = 100 * 1024
	sec := float64(size) / typicalBps
	switch {
	case sec < 60:
		return fmt.Sprintf("%.0f 秒", sec)
	case sec < 3600:
		return fmt.Sprintf("%.0f 分钟", sec/60)
	default:
		return fmt.Sprintf("%.1f 小时", sec/3600)
	}
}

// uploadTimeoutFor 按文件大小估算上传超时。
//
// 取 30KB/s 作为悲观速率（实测跨境上行在 30~170KB/s 之间波动），
// 再留 5 分钟用于 init / 取凭据 / 重试的固定开销，上限 24 小时防止极端值。
//
// 上限必须够大：启用本地 Bot API 后能收 2GB 的文件，
// 2GB / 30KB/s ≈ 19 小时，原来 12 小时的上限会把大文件活活掐断。
func uploadTimeoutFor(size int64) time.Duration {
	const pessimisticBps = 30 * 1024
	est := time.Duration(float64(size)/pessimisticBps) * time.Second
	est += 5 * time.Minute
	if est < 10*time.Minute {
		est = 10 * time.Minute
	}
	if est > 24*time.Hour {
		est = 24 * time.Hour
	}
	return est
}

// pickMedia 从消息中取出可下载的媒体信息。
func pickMedia(m *tgbotapi.Message) (fileID, fileName string, size int64) {
	if m == nil {
		return "", "", 0
	}
	switch {
	case m.Video != nil:
		return m.Video.FileID,
			mediaName(m.Caption, m.Video.FileName, ".mp4", "video_"+m.Video.FileUniqueID+".mp4"),
			int64(m.Video.FileSize)
	case m.Document != nil:
		return m.Document.FileID,
			mediaName(m.Caption, m.Document.FileName, extOrDefault(m.Document.FileName, ".bin"),
				"tg_"+m.Document.FileUniqueID+extOrDefault(m.Document.FileName, ".bin")),
			int64(m.Document.FileSize)
	case m.Audio != nil:
		return m.Audio.FileID,
			mediaName(m.Caption, m.Audio.FileName, extOrDefault(m.Audio.FileName, ".mp3"),
				"audio_"+m.Audio.FileUniqueID+extOrDefault(m.Audio.FileName, ".mp3")),
			int64(m.Audio.FileSize)
	case m.VideoNote != nil:
		return m.VideoNote.FileID,
			mediaName(m.Caption, "", ".mp4", "videonote_"+m.VideoNote.FileUniqueID+".mp4"),
			int64(m.VideoNote.FileSize)
	case m.Animation != nil:
		return m.Animation.FileID,
			mediaName(m.Caption, m.Animation.FileName, extOrDefault(m.Animation.FileName, ".mp4"),
				"animation_"+m.Animation.FileUniqueID+".mp4"),
			int64(m.Animation.FileSize)
	case len(m.Photo) > 0:
		// 照片是一组缩略图，取最大的那张才不浪费画质。
		// TG 不给照片文件名，用 unique_id 兜底：唯一、可追溯，不会互相覆盖。
		best := m.Photo[0]
		for _, p := range m.Photo[1:] {
			if p.FileSize > best.FileSize {
				best = p
			}
		}
		return best.FileID,
			mediaName(m.Caption, "", ".jpg", "photo_"+best.FileUniqueID+".jpg"),
			int64(best.FileSize)
	default:
		return "", "", 0
	}
}

// ---- 命名 ----

// mediaName 决定落盘文件名。
//
// 优先级：**消息说明文字 > TG 原文件名 > 类型兜底名**。
//
// 说明文字优先是有原因的：TG 传上来的文件名经常是手机相册那一串
// 「1月12日 (5)(1).mp4」，用户真正想叫的名字写在说明里；
// 照片更是完全没有文件名，只有说明能用。
func mediaName(caption, rawName, ext, fallback string) string {
	if n := nameFromCaption(caption, ext); n != "" {
		return n
	}
	if rawName != "" {
		return rawName
	}
	return fallback
}

// nameFromCaption 把说明文字当文件名用；不可用时返回空串。
//
// 逐行找第一行「去掉 #标签后还有内容」的文字：
// 频道转发里经常是一行「#合集 #分享」占首位、真正的标题在后面，
// 只取第一行的话文件就会被叫成「#合集 #分享.jpg」。
func nameFromCaption(caption, ext string) string {
	if strings.TrimSpace(caption) == "" {
		return ""
	}
	for _, line := range strings.Split(caption, "\n") {
		c := stripHashtags(line)
		if c == "" || looksLikeLink(c) {
			continue
		}
		// 说明里已经带了扩展名就不要再补一个，避免出现「xx.mp4.mp4」
		if hasFileExt(c) {
			return c
		}
		return c + ext
	}
	return ""
}

// stripHashtags 去掉 #标签，剩下的是标题本体。
// 整行都是标签时返回空串，交给调用方继续看下一行。
func stripHashtags(s string) string {
	kept := make([]string, 0, 4)
	for _, w := range strings.Fields(s) {
		if strings.HasPrefix(w, "#") {
			continue
		}
		kept = append(kept, w)
	}
	return strings.TrimSpace(strings.Join(kept, " "))
}

// looksLikeLink 判断文本是不是链接或命令，避免把磁力链、URL 当成文件名。
func looksLikeLink(s string) bool {
	if strings.Contains(s, "://") {
		return true
	}
	low := strings.ToLower(s)
	for _, p := range []string{"magnet:", "ed2k:", "www.", "t.me/"} {
		if strings.HasPrefix(low, p) {
			return true
		}
	}
	return strings.HasPrefix(s, "/")
}

// hasFileExt 判断名字末尾是否已经有扩展名（点后 2~5 个字母数字）。
func hasFileExt(s string) bool {
	dot := strings.LastIndex(s, ".")
	if dot <= 0 || dot >= len(s)-1 {
		return false
	}
	ext := s[dot+1:]
	if len(ext) < 2 || len(ext) > 5 {
		return false
	}
	for _, r := range ext {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

// extOrDefault 取原文件名的扩展名，没有就用默认扩展名。
func extOrDefault(rawName, def string) string {
	if ext := filepath.Ext(rawName); ext != "" {
		return ext
	}
	return def
}

// uniqueName 在名字已占用时追加 (1)、(2)…（保留扩展名），避免同名文件互相顶掉。
func uniqueName(name string, taken map[string]bool) string {
	if !taken[name] {
		return name
	}
	ext := filepath.Ext(name)
	base := strings.TrimSuffix(name, ext)
	for i := 1; i <= 999; i++ {
		cand := fmt.Sprintf("%s(%d)%s", base, i, ext)
		if !taken[cand] {
			return cand
		}
	}
	return name
}

// nameKey 是「用户 + 目录 + 文件名」的唯一键。
func nameKey(userID int64, cid, name string) string {
	return strconv.FormatInt(userID, 10) + "|" + cid + "|" + name
}

// reserveName / releaseName 标记一个名字正在被用。
func (a *App) reserveName(userID int64, cid, name string) {
	a.reserved.Store(nameKey(userID, cid, name), struct{}{})
}

func (a *App) releaseName(userID int64, cid, name string) {
	a.reserved.Delete(nameKey(userID, cid, name))
}

// takenNames 汇总某个「用户 + 目录」下已经用掉的文件名：
// 历史任务里出现过的 + 当前排队中的。
func (a *App) takenNames(userID int64, cid string) map[string]bool {
	out := make(map[string]bool)
	if a.st != nil {
		names, err := a.st.TaskNames(userID, cid, 500)
		if err != nil && a.log != nil {
			a.log.Debug("读取历史文件名失败", "err", err)
		}
		for _, n := range names {
			if n != "" {
				out[n] = true
			}
		}
	}
	prefix := strconv.FormatInt(userID, 10) + "|" + cid + "|"
	a.reserved.Range(func(k, _ any) bool {
		if s, ok := k.(string); ok && strings.HasPrefix(s, prefix) {
			out[strings.TrimPrefix(s, prefix)] = true
		}
		return true
	})
	return out
}

// hasMedia 判断消息里是否有可转存的媒体。
// 自动转存靠它筛选消息，逻辑必须与 pickMedia 保持一致。
func hasMedia(m *tgbotapi.Message) bool {
	if m == nil {
		return false
	}
	return m.Video != nil || m.Document != nil || m.Audio != nil ||
		m.VideoNote != nil || m.Animation != nil || len(m.Photo) > 0
}

// fileDirectURL 拼出文件的下载地址。
//
// 不能用库自带的 GetFileDirectURL：它把 api.telegram.org 写死在代码里，
// 一旦配了自定义端点就会拼出 https://api.telegram.org/file/bot<token>/... 这种死链。
// 这里从 TG_API_ENDPOINT 反推同一个 server 的 /file 端点。
func (a *App) fileDirectURL(filePath string) string {
	base := strings.TrimSuffix(a.cfg.APIEndpoint, "/bot%s/%s")
	if base == "" || base == a.cfg.APIEndpoint {
		base = "https://api.telegram.org"
	}
	return fmt.Sprintf("%s/file/bot%s/%s", base, a.api.Token, filePath)
}

// officialMaxFileBytes 是官方 Bot API 对 getFile 的硬上限（20MB），
// 超了官方服务器会直接拒绝，不是我们代码能绕的。
const officialMaxFileBytes = 20 << 20

// fetchToLocal 把 Telegram 文件取到本地临时目录。
//
// 关键顺序：>20MB 的文件，官方 getFile 会**直接返回 400 "file is too big"**
// （不是只有下载那一步才失败）。如果像早期版本那样先无脑 getFile、失败就 return，
// MTProto 这条路会被 getFile 的错误提前堵死——配了 TG_API_ID/TG_API_HASH 也下不了大文件。
//
// 因此：
//  1. 明确超过 20MB 且 MTProto 可用 → 跳过 getFile，直接用 file_id 走 MTProto；
//  2. getFile 失败但 MTProto 可用 → 仍用 file_id 兜底（MTProto 不需要 file_path）；
//  3. 其余情况走官方 getFile + 下载（小文件零额外连接）。
func (a *App) fetchToLocal(ctx context.Context, job transferJob) (string, error) {
	useMTProto := a.mtproto != nil && a.mtproto.Enabled()
	if useMTProto && job.size > officialMaxFileBytes {
		a.notify(job, stateDownloading, "", "")
		return a.mtprotoDownload(ctx, job.fileID, job.fileName)
	}

	f, err := a.api.GetFile(tgbotapi.FileConfig{FileID: job.fileID})
	if err != nil {
		if useMTProto {
			a.notify(job, stateDownloading, "", "")
			if p, derr := a.mtprotoDownload(ctx, job.fileID, job.fileName); derr == nil {
				return p, nil
			} else {
				a.log.Warn("getFile 失败后 MTProto 兜底下载同样失败", "err", derr)
			}
		}
		return "", fmt.Errorf("获取文件信息失败（超过 20MB 的文件需配置 TG_API_ID / TG_API_HASH 走 MTProto 下载）: %w", err)
	}

	a.notify(job, stateDownloading, "", "")
	return a.downloadTelegramFile(ctx, f, job.fileName, job.size)
}

// downloadTelegramFile 把文件拉到本地临时目录，返回本地路径。
//
// 两条路：
//  1. 官方 HTTP 下载——零额外连接，但 20MB 封顶；
//  2. MTProto 直连下载——需要 TG_API_ID/TG_API_HASH，能下任意大小。
//
// 所以：明确超过 20MB 的，官方那条路必然失败，直接走 MTProto，别浪费一趟往返；
// 其余先走官方，失败了再拿 MTProto 兜一次。
func (a *App) downloadTelegramFile(ctx context.Context, f tgbotapi.File, fileName string, size int64) (string, error) {
	useMTProto := a.mtproto != nil && a.mtproto.Enabled()
	if useMTProto && size > officialMaxFileBytes {
		if p, err := a.mtprotoDownload(ctx, f.FileID, fileName); err != nil {
			a.log.Warn("MTProto 下载失败，退回官方 Bot API", "err", err)
		} else {
			return p, nil
		}
	}

	p, err := a.downloadViaHTTP(ctx, f, fileName)
	if err == nil {
		return p, nil
	}
	if !useMTProto {
		return "", err
	}
	// 官方这条路失败的原因多半就是「文件太大」，再给 MTProto 一次机会。
	// 仍然失败时返回的是官方那条路的错误：它更贴近用户的实际处境
	//（比如没配 API_ID 时的提示就在那边）。
	p2, err2 := a.mtprotoDownload(ctx, f.FileID, fileName)
	if err2 == nil {
		return p2, nil
	}
	a.log.Warn("MTProto 回退下载同样失败", "err", err2)
	return "", err
}

// mtprotoDownload 用 MTProto 把 file_id 对应的文件下到临时目录。
func (a *App) mtprotoDownload(ctx context.Context, fileID, fileName string) (string, error) {
	if err := os.MkdirAll(a.cfg.TempDir, 0o755); err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	dst := filepath.Join(a.cfg.TempDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), fileName))
	n, err := a.mtproto.Download(ctx, fileID, dst)
	if err != nil {
		return "", fmt.Errorf("MTProto 下载失败: %w", err)
	}
	// 与 HTTP 那条路保持一致：限制是全局的，不能因为换了通道就绕过
	if a.cfg.MaxDownloadBytes > 0 && n > a.cfg.MaxDownloadBytes {
		_ = os.Remove(dst)
		return "", fmt.Errorf("文件超过大小限制 %s", humanSize(a.cfg.MaxDownloadBytes))
	}
	return dst, nil
}

// downloadViaHTTP 从 Telegram 下载文件到临时目录。
func (a *App) downloadViaHTTP(ctx context.Context, f tgbotapi.File, fileName string) (string, error) {
	url := a.fileDirectURL(f.FilePath)

	if err := os.MkdirAll(a.cfg.TempDir, 0o755); err != nil {
		return "", fmt.Errorf("创建临时目录失败: %w", err)
	}
	dst := filepath.Join(a.cfg.TempDir, fmt.Sprintf("%d_%s", time.Now().UnixNano(), fileName))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := a.dlClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("下载失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("下载失败，HTTP %d", resp.StatusCode)
	}

	out, err := os.Create(dst)
	if err != nil {
		return "", fmt.Errorf("创建本地文件失败: %w", err)
	}
	written, copyErr := io.Copy(out, resp.Body)
	closeErr := out.Close()
	if copyErr != nil {
		_ = os.Remove(dst)
		return "", fmt.Errorf("写入本地文件失败: %w", copyErr)
	}
	if closeErr != nil {
		_ = os.Remove(dst)
		return "", fmt.Errorf("关闭本地文件失败: %w", closeErr)
	}
	if a.cfg.MaxDownloadBytes > 0 && written > a.cfg.MaxDownloadBytes {
		_ = os.Remove(dst)
		return "", fmt.Errorf("文件超过大小限制 %s", humanSize(a.cfg.MaxDownloadBytes))
	}
	return dst, nil
}

// dlClient 下载用 HTTP 客户端（大文件不设整体超时，仅限制连接与首字节）。
func (a *App) dlClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			ResponseHeaderTimeout: 60 * time.Second,
		},
	}
}

// sanitizeFileName 去掉路径分隔符与危险字符，防止目录穿越。
func sanitizeFileName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.ReplaceAll(name, "\x00", "")
	name = strings.ReplaceAll(name, "/", "_")
	name = strings.ReplaceAll(name, "\\", "_")
	name = strings.Trim(name, ".")
	if r := []rune(name); len(r) > 180 {
		// 截断必须保住扩展名：名字一长，直接切前 180 字会把末尾的 .mp4 一起切掉，
		// 115 那边就认不出文件类型了。先留足扩展名的位置，再截前缀。
		ext := filepath.Ext(name)
		base := strings.TrimSuffix(name, ext)
		keep := 180 - len([]rune(ext))
		if keep < 1 {
			keep = 1
		}
		if br := []rune(base); len(br) > keep {
			base = string(br[:keep])
		}
		name = base + ext
	}
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}
