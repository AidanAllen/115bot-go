package p115

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"
)

// FileEntry 是目录下的一个条目。
//
// 115 的文件列表接口用极简字段名（n=名称、s=大小、pc=pick_code、fc=分类），
// 这里保留原始 json tag，同时提供语义化访问方法，避免上层到处写 it.N。
type FileEntry struct {
	CID      string `json:"cid"` // 目录 id（目录才有）
	FID      string `json:"fid"` // 文件 id（文件才有）
	ParentID string `json:"pid"`
	Name     string `json:"n"`
	Size     int64  `json:"s"`
	PickCode string `json:"pc"`
	Category int    `json:"fc"` // 文件分类编号（目录为 0），注意是数字不是字符串
	Sha1     string `json:"sha"`
	IsStar   int    `json:"m"`
}

// IsDir 判断是否为目录。115 用 cid/fid 二选一区分。
func (f FileEntry) IsDir() bool {
	return strings.TrimSpace(f.CID) != "" && strings.TrimSpace(f.FID) == ""
}

// ID 返回该条目的 id（目录取 cid，文件取 fid）。
func (f FileEntry) ID() string {
	if f.IsDir() {
		return f.CID
	}
	return f.FID
}

// FileList 是文件列表响应。
// 与 task_lists 类似：成功时不带 state，失败才给 state/error。
type FileList struct {
	State  *bool       `json:"state"`
	Error  string      `json:"error"`
	Count  int         `json:"count"`
	Offset int         `json:"offset"`
	Limit  int         `json:"limit"`
	Data   []FileEntry `json:"data"`
}

// ListFiles 列出指定目录下的条目（目录在前）。
// GET webapi.115.com/files?aid=1&cid=<cid>&limit=<n>&offset=<o>&show_dir=1&type=0
func (c *Client) ListFiles(ctx context.Context, cid string, offset, limit int) (*FileList, error) {
	cid = strings.TrimSpace(cid)
	if cid == "" {
		cid = "0"
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if offset < 0 {
		offset = 0
	}
	u := fmt.Sprintf("%s?aid=1&cid=%s&offset=%d&limit=%d&show_dir=1&type=0&o=user_ptime&asc=0",
		c.ep.Files, url.QueryEscape(cid), offset, limit)

	body, err := c.get(ctx, u)
	if err != nil {
		return nil, err
	}
	var out FileList
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析文件列表失败: %w", err)
	}
	if out.State != nil && !*out.State {
		msg := strings.TrimSpace(out.Error)
		if msg == "" {
			msg = "115 拒绝返回文件列表（未给出原因）"
		}
		if isLoginError(msg) {
			return nil, ErrLoginRequired
		}
		return nil, &APIError{Op: "files", Message: msg}
	}
	return &out, nil
}

// ListDirs 列出指定目录下的**子目录**，自动跨页收集、跳过文件。
//
// 115 的 files 接口把目录和文件混在同一页返回，分页按总条目计。
// 如果只取一页再过滤，目录下文件一多就会漏掉目录、甚至一页里一个目录都没有，
// 所以这里逐页翻直到凑够 limit 个目录或翻完为止。
func (c *Client) ListDirs(ctx context.Context, cid string, limit int) ([]FileEntry, error) {
	if limit <= 0 {
		limit = 50
	}
	if limit > 200 {
		limit = 200
	}
	const pageSize = 100
	out := make([]FileEntry, 0, limit)
	for offset := 0; len(out) < limit; offset += pageSize {
		list, err := c.ListFiles(ctx, cid, offset, pageSize)
		if err != nil {
			return nil, err
		}
		for _, it := range list.Data {
			if it.IsDir() {
				out = append(out, it)
			}
		}
		if len(list.Data) < pageSize {
			break // 已经翻完
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// FindDirByPath 从根目录开始按名称逐级查找目录，返回最终目录 cid。
// 找不到时返回空字符串（不视为错误）。用于把「云下载/电影」这类可读路径解析成 cid。
func (c *Client) FindDirByPath(ctx context.Context, path string) (string, error) {
	parts := splitPath(path)
	cid := "0"
	for _, name := range parts {
		list, err := c.ListFiles(ctx, cid, 0, 200)
		if err != nil {
			return "", err
		}
		found := ""
		for _, it := range list.Data {
			if it.IsDir() && it.Name == name {
				found = it.CID
				break
			}
		}
		if found == "" {
			return "", nil
		}
		cid = found
	}
	return cid, nil
}

func splitPath(p string) []string {
	p = strings.Trim(strings.TrimSpace(p), "/")
	if p == "" {
		return nil
	}
	raw := strings.Split(p, "/")
	out := make([]string, 0, len(raw))
	for _, s := range raw {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// DeleteFiles 把文件移入回收站。
// POST webapi.115.com/rb/delete  form: fid[0]=<id>&fid[1]=<id>...
func (c *Client) DeleteFiles(ctx context.Context, fileIDs []string) error {
	if len(fileIDs) == 0 {
		return nil
	}
	form := url.Values{}
	for i, id := range fileIDs {
		form.Set(fmt.Sprintf("fid[%d]", i), id)
	}
	body, err := c.postForm(ctx, c.ep.Delete, form)
	if err != nil {
		return err
	}
	var out struct {
		State bool   `json:"state"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return fmt.Errorf("解析删除结果失败: %w", err)
	}
	if !out.State {
		msg := strings.TrimSpace(out.Error)
		if msg == "" {
			msg = "未知原因"
		}
		if isLoginError(msg) {
			return ErrLoginRequired
		}
		return &APIError{Op: "rb/delete", Message: msg}
	}
	return nil
}
