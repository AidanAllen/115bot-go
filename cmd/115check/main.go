// 命令 115check 用账号文件里的凭据对 115 做一轮完整接口体检。
//
// 用途：扫码/粘贴 cookie 后确认凭据真的可用，并在上线前排掉「接口字段对不上」
// 这类只有真实数据才能暴露的问题。
//
// 默认只读；加 -write-test 会额外做一次「下发 → 确认 → 删除」的可逆写操作，
// 使用随机 infohash 构造磁力，不会与账号里已有任务冲突。
//
// 用法：
//
//	115check -account /tmp/115-account.json
//	115check -account /tmp/115-account.json -write-test
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/ghs/115bot-go/internal/p115"
)

// accountFile 与 qrlogin 产出的账号文件结构一致。
type accountFile struct {
	Cookie    string `json:"cookie"`
	UserAgent string `json:"user_agent"`
	App       string `json:"app"`
	Slot      string `json:"ssoent_slot"`
	UserID    int64  `json:"user_id"`
	UserName  string `json:"user_name"`
}

var ( // 统计各检查项结果
	passCnt int
	failCnt int
)

func check(ok bool, format string, args ...any) bool {
	tag := "[OK]"
	if ok {
		passCnt++
	} else {
		tag = "[!!]"
		failCnt++
	}
	fmt.Printf("%s %s\n", tag, fmt.Sprintf(format, args...))
	return ok
}

func main() {
	account := flag.String("account", "/tmp/115-account.json", "账号文件路径")
	pages := flag.Int("pages", 1, "拉取多少页离线任务用于校验")
	timeout := flag.Duration("timeout", 25*time.Second, "单次请求超时")
	writeTest := flag.Bool("write-test", false, "额外执行可逆写测试（下发→确认→删除）")
	testLink := flag.String("test-link", "", "写测试使用的链接；留空则用随机 infohash 构造磁力（115 会判为无效种子）")
	flag.Parse()

	if err := run(*account, *pages, *timeout, *writeTest, *testLink); err != nil {
		fmt.Fprintf(os.Stderr, "中断: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("\n汇总: 通过 %d 项，失败 %d 项\n", passCnt, failCnt)
	if failCnt > 0 {
		os.Exit(2)
	}
}

func run(accountPath string, pages int, timeout time.Duration, writeTest bool, testLink string) error {
	blob, err := os.ReadFile(accountPath)
	if err != nil {
		return fmt.Errorf("读取账号文件失败: %w", err)
	}
	var acc accountFile
	if err := json.Unmarshal(blob, &acc); err != nil {
		return fmt.Errorf("解析账号文件失败: %w", err)
	}
	if acc.Cookie == "" {
		return fmt.Errorf("账号文件里没有 cookie")
	}
	fmt.Printf("凭据: 设备=%s 槽位=%s 用户=%s(%d)\n\n", acc.App, acc.Slot, acc.UserName, acc.UserID)

	ua := acc.UserAgent
	if ua == "" {
		ua = p115.DefaultUA
	}
	c := p115.New(acc.Cookie, ua, timeout)
	ctx := context.Background()

	// ---- A. 登录态 ----
	info, err := c.CheckLogin(ctx)
	if err != nil {
		check(false, "登录态校验: %v", err)
		return nil
	}
	check(true, "登录态: %s (uid=%d)", info.UserName, info.UserID)

	// ---- B. 列根目录 ----
	fmt.Println("\n--- 目录接口 ---")
	root, err := c.ListFiles(ctx, "0", 0, 20)
	if err != nil {
		check(false, "列出根目录: %v", err)
		return nil
	}
	check(true, "列出根目录: %d 项（总数 %d）", len(root.Data), root.Count)

	// 找一个真实子目录，作为后续 category/get 的样本
	sampleCID, sampleName := "", ""
	for _, it := range root.Data {
		if it.IsDir() && it.Name != "最近接收" {
			sampleCID, sampleName = it.CID, it.Name
			break
		}
	}
	preview := 0
	for _, it := range root.Data {
		if preview >= 3 {
			break
		}
		kind := "文件"
		if it.IsDir() {
			kind = "目录"
		}
		fmt.Printf("       %s %s (%s)\n", it.ID(), cut(it.Name, 26), kind)
		preview++
	}

	// ---- C. 目录信息校验 ----
	if cat, err := c.CategoryGet(ctx, "0"); err != nil {
		check(false, "根目录信息(本地返回): %v", err)
	} else {
		check(cat.FileName == "根目录", "根目录信息(本地返回): name=%q（不发请求，115 对 cid=0 会报参数错误）", cat.FileName)
	}

	if sampleCID != "" {
		cat, err := c.CategoryGet(ctx, sampleCID)
		if err != nil {
			check(false, "目录信息(%s): %v", sampleName, err)
		} else {
			path := cat.CategoryPath()
			check(cat.FileName == sampleName && path != "",
				"目录信息(%s): name=%q path=%q size=%s 子目录=%s",
				sampleName, cat.FileName, path, cat.Size, cat.FolderCount)
		}
	}

	if _, err := c.CategoryGet(ctx, "999999999999999"); err == nil {
		check(false, "不存在的 cid 应报错，但未报错")
	} else {
		check(true, "不存在的 cid 正确报错: %v", err)
	}

	// ---- D. 路径 → cid 解析 ----
	if sampleName != "" {
		cid, err := c.FindDirByPath(ctx, sampleName)
		switch {
		case err != nil:
			check(false, "路径解析(%s): %v", sampleName, err)
		case cid == sampleCID:
			check(true, "路径解析(%s) → cid=%s", sampleName, cid)
		case cid == "":
			check(false, "路径解析(%s) 未找到，但该目录确实存在", sampleName)
		default:
			check(false, "路径解析(%s) 得到 cid=%s，期望 %s", sampleName, cid, sampleCID)
		}
		if cid2, err := c.FindDirByPath(ctx, "不存在的目录_zzz"); err == nil && cid2 == "" {
			check(true, "路径解析(不存在) 正确返回空")
		}
	}

	// ---- E. 离线任务 ----
	fmt.Println("\n--- 离线接口 ---")
	if pages <= 0 {
		pages = 1
	}
	var total, done int
	for page := 1; page <= pages; page++ {
		list, err := c.ListOfflineTasks(ctx, page)
		if err != nil {
			check(false, "第 %d 页离线任务: %v", page, err)
			break
		}
		if page == 1 {
			check(true, "离线配额: 已用=%d 上限=%d 任务总数=%d 每页=%d",
				list.Total-list.Quota, list.Total, list.Count, list.PageSize)
		}
		for _, t := range list.Tasks {
			total++
			if t.Done() {
				done++
			}
		}
		if list.Page >= list.PageCount {
			break
		}
	}
	check(total > 0, "离线任务读取: 共 %d 条，其中已完成 %d", total, done)

	if len(root.Data) == 0 {
		fmt.Println("       (根目录为空，跳过样本展示)")
	}

	list, err := c.ListOfflineTasks(ctx, 1)
	if err == nil && len(list.Tasks) > 0 {
		n := len(list.Tasks)
		if n > 3 {
			n = 3
		}
		for _, t := range list.Tasks[:n] {
			fmt.Printf("       - %-42s %7.2fGB %-6s pick=%s\n",
				cut(t.Name, 42), float64(t.Size)/1024/1024/1024, t.Progress(), cut(t.PickCode, 10))
		}
	}

	// ---- F. 写操作往返测试 ----
	if writeTest {
		fmt.Println("\n--- 写操作往返测试（随机 infohash，可逆）---")
		hash, err := randomInfoHash()
		if err != nil {
			return err
		}
		link := testLink
		if link == "" {
			link = "magnet:?xt=urn:btih:" + hash
		}
		fmt.Printf("       测试链接: %s\n", link)

		// 1) 下发
		items, err := c.AddOfflineURLs(ctx, []string{link}, "0")
		if err != nil {
			check(false, "下发离线任务: %v", err)
		} else if len(items) == 0 {
			check(false, "下发离线任务: 未返回逐条结果")
		} else {
			it := items[0]
			// 随机种子的真实结果可能是「成功入列」或「种子解析失败」，两者都说明接口链路正常
			check(true, "下发离线任务: state=%v msg=%q", it.State, it.Message())
		}

		// 2) 确认是否出现在列表：磁力按 info_hash 找，HTTP 链接按 url 找
		time.Sleep(3 * time.Second)
		found := findTask(ctx, c, hash, link)
		if found == nil {
			check(true, "刚下发的任务未出现在前 2 页（115 可能判定资源无效，属正常）")
		} else {
			hash = found.InfoHash
			check(true, "已在列表中找到: name=%q status=%d(%s) 进度=%s",
				cut(found.Name, 30), found.Status, found.StatusText, found.Progress())
		}

		// 3) 删除（flag=1，连测试产生的文件一起清掉）
		if hash == "" {
			check(false, "删除离线任务: 没有拿到 info_hash，无法定位任务")
		} else if err := c.DeleteOfflineTasks(ctx, []string{hash}, true); err != nil {
			check(false, "删除离线任务: %v", err)
		} else {
			check(true, "删除离线任务: 成功")
		}

		// 4) 确认已消失
		time.Sleep(2 * time.Second)
		if again, err := c.FindOfflineTask(ctx, hash, 2); err != nil {
			check(false, "删除后复查: %v", err)
		} else if again == nil {
			check(true, "删除后复查: 列表中已不存在")
		} else {
			check(false, "删除后复查: 任务仍在列表中（status=%d）", again.Status)
		}
	}

	return nil
}

// findTask 在前两页里按 info_hash 或 url 定位刚下发的任务。
// HTTP 直链任务不一定有 info_hash，所以要额外按 url 匹配。
func findTask(ctx context.Context, c *p115.Client, infoHash, link string) *p115.OfflineTask {
	if infoHash != "" {
		if t, err := c.FindOfflineTask(ctx, infoHash, 2); err == nil && t != nil {
			return t
		}
	}
	if link == "" {
		return nil
	}
	for page := 1; page <= 2; page++ {
		list, err := c.ListOfflineTasks(ctx, page)
		if err != nil {
			return nil
		}
		for i := range list.Tasks {
			if list.Tasks[i].URL == link {
				return &list.Tasks[i]
			}
		}
		if list.Page >= list.PageCount {
			break
		}
	}
	return nil
}

// randomInfoHash 生成 20 字节随机值作为 infohash（40 位十六进制）。
// 用随机值而非真实资源，确保不会与账号里已有任务产生冲突。
func randomInfoHash() (string, error) {
	buf := make([]byte, 20)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("生成随机 infohash 失败: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "…"
}
