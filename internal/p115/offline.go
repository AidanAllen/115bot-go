package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// AddOfflineURLs 批量添加离线任务，返回逐条结果。
//
// 参数依据（实测确认，不要再按方法名去猜字段）：
//
//	POST 115.com/web/lixian/?ct=lixian&ac=add_task_urls
//	form: url[0]=<链接>&url[1]=<链接>...&wp_path_id=<目标目录 cid>
//
// 关键坑：字段名是 **url[i] 数组**，不是 urls。
// 用 urls 提交时 115 会返回「错误的链接」（errcode 10004），
// 看起来像是链接格式问题，实际是参数名不对，极易误判。
//
// 成功后 result 里每条会带 info_hash，可用于后续查询/删除任务。
func (c *Client) AddOfflineURLs(ctx context.Context, links []string, wpPathID string) ([]AddOfflineItem, error) {
	wpPathID = strings.TrimSpace(wpPathID)
	if wpPathID == "" {
		return nil, fmt.Errorf("未设置离线保存目录 cid")
	}

	form := url.Values{}
	n := 0
	for _, l := range links {
		if l = strings.TrimSpace(l); l != "" {
			form.Set(fmt.Sprintf("url[%d]", n), l)
			n++
		}
	}
	if n == 0 {
		return nil, fmt.Errorf("没有可下发的链接")
	}
	form.Set("wp_path_id", wpPathID)

	body, err := c.postForm(ctx, c.ep.OfflineAdd, form)
	if err != nil {
		return nil, err
	}
	return parseAddOffline(body)
}

// parseAddOffline 解析 add_task_urls 的结果。
//
// 115 当前返回逐条明细在 result 数组、错误文案在 error_msg；
// 早期形态则把明细放在 data（数组或单对象）。两种都接住。
func parseAddOffline(body []byte) ([]AddOfflineItem, error) {
	var raw struct {
		State    *bool            `json:"state"`
		ErrorMsg string           `json:"error_msg"`
		Message  string           `json:"message"`
		Error    string           `json:"error"`
		ErrCode  int              `json:"errcode"`
		Result   []AddOfflineItem `json:"result"`
		Data     json.RawMessage  `json:"data"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("解析离线任务响应失败: %w", err)
	}

	// 现网形态：逐条结果在 result
	if len(raw.Result) > 0 {
		return raw.Result, nil
	}

	// 兼容：明细放在 data（数组 / 单对象）
	trimmed := strings.TrimSpace(string(raw.Data))
	if trimmed != "" && trimmed != "null" {
		if strings.HasPrefix(trimmed, "[") {
			var items []AddOfflineItem
			if err := json.Unmarshal(raw.Data, &items); err != nil {
				return nil, fmt.Errorf("解析离线任务结果失败: %w", err)
			}
			return items, nil
		}
		var one AddOfflineItem
		if err := json.Unmarshal(raw.Data, &one); err != nil {
			return nil, fmt.Errorf("解析离线任务结果失败: %w", err)
		}
		return []AddOfflineItem{one}, nil
	}

	// 没有任何逐条明细时，才用顶层 state 判断整体成败
	if raw.State != nil && !*raw.State {
		msg := firstNonEmpty(raw.ErrorMsg, raw.Message, raw.Error)
		if msg == "" {
			msg = fmt.Sprintf("115 拒绝了离线任务（errcode=%d，未给出原因）", raw.ErrCode)
		}
		if isLoginError(msg) {
			return nil, ErrLoginRequired
		}
		return nil, &APIError{Op: "add_task_urls", Message: msg}
	}
	return nil, nil
}

// ListOfflineTasks 拉取离线任务列表。
// 该接口字段平铺在顶层（非 envelope 包装），故单独解析。
func (c *Client) ListOfflineTasks(ctx context.Context, page int) (*OfflineTaskList, error) {
	if page <= 0 {
		page = 1
	}
	body, err := c.get(ctx, fmt.Sprintf(c.ep.OfflineList, page))
	if err != nil {
		return nil, err
	}
	var out OfflineTaskList
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析离线任务列表失败: %w", err)
	}
	// 成功响应不带 state 字段（State 为 nil），不能拿零值 false 当失败判断。
	if out.State != nil && !*out.State {
		if isLoginError(out.Error) {
			return nil, ErrLoginRequired
		}
		msg := strings.TrimSpace(out.Error)
		if msg == "" {
			msg = "115 拒绝返回离线任务列表（未给出原因）"
		}
		return nil, &APIError{Op: "task_lists", Message: msg}
	}
	return &out, nil
}

// DeleteOfflineTasks 删除离线任务。
// POST 115.com/web/lixian/?ct=lixian&ac=task_del
//
//	form: hash[0]=<info_hash>&hash[1]=...&flag=0|1
//
// flag=1 表示连已下载的文件一起删除，flag=0 仅移除任务记录。
// 实测成功响应为：{"state":true,"errtype":"suc","errcode":0}（无 data 段）。
func (c *Client) DeleteOfflineTasks(ctx context.Context, hashes []string, delFile bool) error {
	cleaned := make([]string, 0, len(hashes))
	for _, h := range hashes {
		if h = strings.TrimSpace(h); h != "" {
			cleaned = append(cleaned, h)
		}
	}
	if len(cleaned) == 0 {
		return fmt.Errorf("没有指定要删除的离线任务")
	}

	form := url.Values{}
	for i, h := range cleaned {
		form.Set(fmt.Sprintf("hash[%d]", i), h)
	}
	flag := "0"
	if delFile {
		flag = "1"
	}
	form.Set("flag", flag)

	body, err := c.postForm(ctx, c.ep.OfflineDel, form)
	if err != nil {
		return err
	}
	var r struct {
		State    bool   `json:"state"`
		ErrType  string `json:"errtype"`
		ErrCode  int    `json:"errcode"`
		ErrorMsg string `json:"error_msg"`
		Error    string `json:"error"`
	}
	if err := json.Unmarshal(body, &r); err != nil {
		return fmt.Errorf("解析删除结果失败: %w", err)
	}
	if !r.State {
		msg := firstNonEmpty(r.ErrorMsg, r.Error)
		if msg == "" {
			msg = fmt.Sprintf("删除离线任务失败（errcode=%d）", r.ErrCode)
		}
		if isLoginError(msg) {
			return ErrLoginRequired
		}
		return &APIError{Op: "task_del", Message: msg}
	}
	return nil
}

// FindOfflineTask 按 info_hash 在前若干页里查找任务，便于测试与状态确认。
// 返回 nil 表示未找到。
func (c *Client) FindOfflineTask(ctx context.Context, infoHash string, maxPages int) (*OfflineTask, error) {
	infoHash = strings.ToLower(strings.TrimSpace(infoHash))
	if infoHash == "" {
		return nil, fmt.Errorf("info_hash 为空")
	}
	if maxPages <= 0 {
		maxPages = 3
	}
	for page := 1; page <= maxPages; page++ {
		list, err := c.ListOfflineTasks(ctx, page)
		if err != nil {
			return nil, err
		}
		for i := range list.Tasks {
			if strings.ToLower(list.Tasks[i].InfoHash) == infoHash {
				return &list.Tasks[i], nil
			}
		}
		if list.Page >= list.PageCount || len(list.Tasks) == 0 {
			break
		}
	}
	return nil, nil
}

// CategoryGet 查询指定目录信息，用于校验 cid 是否存在、可写。
//
// 实测要点：
//   - cid=0（根目录）调用该接口会返回「参数错误」（errNo 1001），
//     所以根目录在本地直接给出，不浪费一次请求也不会误判；
//   - 不存在的 cid 返回 errNo 70005「文件不存在或已删除」，正好可用于校验；
//   - 该接口字段平铺在顶层、无 data 包装，不能用通用 envelope 解析。
func (c *Client) CategoryGet(ctx context.Context, cid string) (*CategoryData, error) {
	cid = strings.TrimSpace(cid)
	if cid == "" || cid == "0" {
		return &CategoryData{State: true, FileName: "根目录"}, nil
	}

	body, err := c.get(ctx, fmt.Sprintf(c.ep.Category, url.QueryEscape(cid)))
	if err != nil {
		return nil, err
	}
	var d CategoryData
	if err := json.Unmarshal(body, &d); err != nil {
		return nil, fmt.Errorf("解析目录信息失败: %w", err)
	}
	if !d.State {
		msg := strings.TrimSpace(d.Error)
		if msg == "" {
			msg = fmt.Sprintf("查询目录失败（errNo=%d）", d.ErrNo)
		}
		if isLoginError(msg) {
			return nil, ErrLoginRequired
		}
		return nil, &APIError{Op: "category/get", Message: msg}
	}
	return &d, nil
}

// CategoryPath 把目录信息拼成 "a/b/c" 形式，便于展示。
// paths 只含父级链、不含自身，故最后要补上 FileName。
func (d *CategoryData) CategoryPath() string {
	if d == nil {
		return ""
	}
	parts := make([]string, 0, len(d.Paths)+1)
	for _, p := range d.Paths {
		if p.FileName != "" {
			parts = append(parts, p.FileName)
		}
	}
	if d.FileName != "" {
		parts = append(parts, d.FileName)
	}
	return strings.Join(parts, "/")
}
