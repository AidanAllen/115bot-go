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

	"github.com/ghs/115bot-go/internal/p115"
	"github.com/ghs/115bot-go/internal/store"
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
	if !a.userEnabled(ac.UserID) {
		a.send(ac.ChatID, TxtTransferDisabled)
		return
	}
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

	pos := a.queueTransfer(transferJob{ac: ac, fileID: fileID, fileName: fileName, size: size, replyTo: replyTo, cid: cid})
	if pos == 0 {
		// 入队失败要把去重标记与名字占位一起撤掉，否则之后永远排不进来
		a.inflight.Delete(fileID)
		a.releaseName(ac.UserID, cid, fileName)
		a.log.Warn("转存队列已满，拒绝新任务", "user_id", ac.UserID, "file", fileName, "上限", transferQueueSize)
		a.sendReply(ac.ChatID, replyTo, fmt.Sprintf(TxtTransferQueueFull, transferQueueSize, a.queueDepth()))
		return
	}
	a.log.Info("转存入队", "user_id", ac.UserID, "file", fileName, "位置", pos, "待处理", a.queueDepth())
	a.sendReply(ac.ChatID, replyTo, fmt.Sprintf(TxtTransferQueued, escapeHTML(fileName), humanSize(size), pos))
}

// runTransfer 真正执行一次转存：下载 → 上传 → 回报。
// 由队列 worker 串行调用，同一时刻只会有一个在跑。
func (a *App) runTransfer(ctx context.Context, job transferJob) {
	ac := job.ac
	chatID := ac.ChatID
	fileName, fileID, size := job.fileName, job.fileID, job.size
	// 无论成败都归还这个名字：失败的文件并不存在，名字可以再用
	defer a.releaseName(ac.UserID, job.cid, fileName)

	up := a.userUploader(ac.UserID)
	if !up.Available() {
		a.sendReply(chatID, job.replyTo, TxtTransferNoUploader)
		return
	}
	cid := a.userTransferCID(ac.UserID)
	if cid == "" {
		a.sendReply(chatID, job.replyTo, TxtNeedTransferCID)
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

	a.sendReply(chatID, job.replyTo, fmt.Sprintf(TxtTransferDownloading, escapeHTML(fileName), humanSize(size)))

	localPath, fromLocal, err := a.obtainLocalFile(ctx, fileID, fileName)
	if err != nil {
		if taskID > 0 {
			_ = a.st.UpdateTask(taskID, store.StatusFailed, err.Error())
		}
		a.sendReply(chatID, job.replyTo, fmt.Sprintf(TxtInternalErr, escapeHTML(err.Error())))
		return
	}
	// 两种来源都要清理：临时副本不删会堆积，本地 API 的缓存不删会撑爆磁盘
	defer func() {
		if rerr := os.Remove(localPath); rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
			a.log.Warn("清理转存临时文件失败", "path", localPath, "err", rerr)
		}
	}()

	if fi, statErr := os.Stat(localPath); statErr == nil {
		size = fi.Size()
	}

	if fromLocal {
		a.sendReply(chatID, job.replyTo, TxtTransferLocalReady)
	} else {
		a.sendReply(chatID, job.replyTo, TxtTransferDownloaded)
	}
	// 上传可能很久（受机房上行带宽限制），提前告知预估耗时，避免用户以为卡死
	a.sendReply(chatID, job.replyTo, fmt.Sprintf(TxtTransferUploading, humanSize(size), estimateUploadHuman(size)))

	// 上传超时按文件大小估算：跨境链路实测低至 30KB/s，
	// 固定 10 分钟会让几十 MB 的视频必定超时。
	uctx, cancel := context.WithTimeout(ctx, uploadTimeoutFor(size))
	defer cancel()

	pickCode, err := up.Upload(uctx, localPath, fileName, size, cid)
	if err != nil {
		if taskID > 0 {
			_ = a.st.UpdateTask(taskID, store.StatusFailed, err.Error())
		}
		a.sendReply(chatID, job.replyTo, fmt.Sprintf(TxtInternalErr, escapeHTML(friendlyErr(err))))
		return
	}

	if taskID > 0 {
		_ = a.st.UpdateTask(taskID, store.StatusSuccess, "")
	}
	// 报出实测平均速度：慢的时候用户能一眼看出是链路限速还是卡住了
	avg := humanSize(int64(float64(size) / math.Max(time.Since(start).Seconds(), 0.001)))
	a.sendReply(chatID, job.replyTo, fmt.Sprintf(TxtTransferSuccess,
		escapeHTML(fileName), up.Name(), int(time.Since(start).Seconds()), avg, pickCode))
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
func nameFromCaption(caption, ext string) string {
	c := strings.TrimSpace(caption)
	if c == "" {
		return ""
	}
	// 只取第一行：多行说明通常只有第一行是标题，后面是正文、标签
	if i := strings.IndexAny(c, "\r\n"); i >= 0 {
		c = c[:i]
	}
	c = strings.TrimSpace(c)
	if c == "" || looksLikeLink(c) {
		return ""
	}
	// 说明里已经带了扩展名就不要再补一个，避免出现「xx.mp4.mp4」
	if hasFileExt(c) {
		return c
	}
	return c + ext
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

// obtainLocalFile 取到文件的本地路径。
//
// 两条路子：
//  1. 本地 Bot API（telegram-bot-api --local）：getFile 直接返回 telegram-bot-api
//     容器内的**绝对路径**，文件已经被 tdlib 落到磁盘上了。bot 容器只要把同一个卷
//     挂到同一个路径，就能直接读它，跨境下载这一步全省了——旧 Java bot 靠这个扛住 1.4GB 视频。
//  2. 官方 Bot API：只能走 HTTP 下载到临时目录，有 20MB 硬限制。
//
// fromLocal 表示路径是不是直接来自本地 Bot API 的缓存目录。
// 两种来源用完都必须删：临时副本不删会堆积，本地 API 的缓存不删会撑爆磁盘
// （旧 bot 日志里的「删除上传完成，本地视频：…/videos/file_388.mp4」就是这一步）。
func (a *App) obtainLocalFile(ctx context.Context, fileID, fileName string) (path string, fromLocal bool, err error) {
	f, err := a.api.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		return "", false, fmt.Errorf("获取文件信息失败（超过官方 Bot API 20MB 限制的文件需启用本地 Bot API 服务）: %w", err)
	}

	// 本地 Bot API 直接给绝对路径；官方 API 给的是相对路径（photos/file_1.jpg 这种）。
	if filepath.IsAbs(f.FilePath) {
		st, serr := os.Stat(f.FilePath)
		if serr == nil && st.Mode().IsRegular() {
			if a.cfg.MaxDownloadBytes > 0 && st.Size() > a.cfg.MaxDownloadBytes {
				return "", false, fmt.Errorf("文件超过大小限制 %s", humanSize(a.cfg.MaxDownloadBytes))
			}
			return f.FilePath, true, nil
		}
		// 路径不可读就退回 HTTP 下载，不让整个转存卡死在这一步
		a.log.Warn("本地 Bot API 路径不可读，退回 HTTP 下载", "path", f.FilePath)
	}

	dst, err := a.downloadTelegramFile(ctx, f, fileName)
	return dst, false, err
}

// fileDirectURL 拼出文件的下载地址。
//
// 不能用库自带的 GetFileDirectURL：它把 api.telegram.org 写死在代码里，
// 接本地 Bot API 时会拼出 https://api.telegram.org/file/bot<token>//var/lib/.../file_1.mp4
// 这种永远下不到的死链。这里从 TG_API_ENDPOINT 反推同一个 server 的 /file 端点。
func (a *App) fileDirectURL(filePath string) string {
	base := strings.TrimSuffix(a.cfg.APIEndpoint, "/bot%s/%s")
	if base == "" || base == a.cfg.APIEndpoint {
		base = "https://api.telegram.org"
	}
	// --local 模式给的是绝对路径，去掉前导斜杠避免拼出双斜杠
	return fmt.Sprintf("%s/file/bot%s/%s", base, a.api.Token, strings.TrimPrefix(filePath, "/"))
}

// downloadTelegramFile 从 Telegram 下载文件到临时目录。
func (a *App) downloadTelegramFile(ctx context.Context, f tgbotapi.File, fileName string) (string, error) {
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
		// 截断到 180 个字符：扩展名保留在截断后的前缀里即可，
		// 不必再单独拼接（否则会与已截断的前缀重复、超长）。
		name = string(r[:180])
	}
	if name == "" || name == "." || name == ".." {
		return ""
	}
	return name
}
