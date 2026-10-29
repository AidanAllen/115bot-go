package p115

import (
	"encoding/json"
	"fmt"
	"strings"
)

// envelope 是 115 多数接口的通用外层结构。
// 已通过真实请求验证存在 state / data / error 三个字段。
// data 段类型因接口而异，统一用 RawMessage 承接后按需二次解析。
type envelope struct {
	State bool            `json:"state"`
	Error string          `json:"error"`
	Data  json.RawMessage `json:"data"`
}

// NavData my.115.com/?ct=ajax&ac=nav 的 data 段。
type NavData struct {
	UserID    json.Number `json:"user_id"`
	UserName  string      `json:"user_name"`
	Vip       json.Number `json:"vip"`
	SizeTotal json.Number `json:"size_total"`
	SizeUsed  json.Number `json:"size_used"`
}

// UserInfo 对外暴露的账号信息。
type UserInfo struct {
	UserID   int64
	UserName string
	Raw      NavData
}

// CategoryPathNode 是目录路径上的一个节点。
// 真实字段是 file_id / file_name（不是 cid / name），这里按线上数据取名。
type CategoryPathNode struct {
	FileID   json.Number `json:"file_id"`
	FileName string      `json:"file_name"`
}

// flexString 承接 115 里「同一字段忽而数字、忽而字符串」的字段。
//
// 实测 category/get：非空目录返回 "count":"1020"（字符串），
// 空目录返回 count:0（数字）。若按 string 声明，空目录会反序列化失败，
// 表现为「刚建好的空目录查不到」，排查起来非常隐蔽。
type flexString string

// UnmarshalJSON 数字与字符串都接受，统一落成字符串字面值。
func (f *flexString) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	if s == "" || s == "null" {
		*f = ""
		return nil
	}
	if strings.HasPrefix(s, "\"") {
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = flexString(v)
		return nil
	}
	// 数字、布尔等字面量：直接保留其文本形态
	*f = flexString(s)
	return nil
}

// String 返回字符串形态，便于拼接展示。
func (f flexString) String() string { return string(f) }

// CategoryData 是 category/get 的响应。
//
// 三个容易踩的坑（已用线上真实响应核对）：
//  1. 字段**平铺在顶层**，没有 data 包装，所以不能走通用 envelope 解析；
//  2. 目录名是 file_name，且 paths 只包含**父级链、不含自身**；
//  3. count / folder_count 在空目录返回数字、非空目录返回字符串。
//
// 真实样例（非空）：
//
//	{"state":true,"error":"","errNo":0,"count":"902","size":"773.31GB",
//	 "folder_count":"88","file_name":"云下载","pick_code":"...",
//	 "paths":[{"file_id":0,"file_name":"根目录"}]}
//
// 真实样例（空目录，注意 count 与 folder_count 是数字）：
//
//	{"state":true,"error":"","errNo":0,"count":0,"size":"0B","folder_count":0,
//	 "file_name":"离线","paths":[{"file_id":0,"file_name":"根目录"}]}
type CategoryData struct {
	State       bool               `json:"state"`
	Error       string             `json:"error"`
	ErrNo       int                `json:"errNo"`
	Count       flexString         `json:"count"`
	Size        flexString         `json:"size"`
	FolderCount flexString         `json:"folder_count"`
	FileName    string             `json:"file_name"`
	PickCode    string             `json:"pick_code"`
	IsPrivate   int                `json:"is_private"`
	Paths       []CategoryPathNode `json:"paths"`
}

// AddOfflineItem 是 add_task_urls 返回的单条结果。
//
// 实测 115 当前返回的形态是：
//
//	{"state":false,"error_msg":"错误的链接","errno":0,
//	 "result":[{"state":false,"error_msg":"错误的链接","errtype":"err","errcode":10004}],
//	 "errcode":10004}
//
// 即错误文案字段名是 error_msg（不是 error），逐条结果在 result（不是 data）。
// 这里同时保留几个历史字段名，避免上游改动时又出现「取不到错误原因」的情况。
type AddOfflineItem struct {
	State    bool        `json:"state"`
	ErrorMsg string      `json:"error_msg"`
	ErrMsg   string      `json:"errmsg"`
	Error    string      `json:"error"`
	URL      string      `json:"url"`
	InfoHash string      `json:"info_hash"`
	Name     string      `json:"name"`
	Errno    json.Number `json:"errno"`
	ErrCode  int         `json:"errcode"`
	ErrType  string      `json:"errtype"`
}

// Message 统一取单条结果的错误文案。
func (it AddOfflineItem) Message() string {
	return firstNonEmpty(it.ErrorMsg, it.ErrMsg, it.Error)
}

// OfflineTask 是单条离线任务。
//
// 字段严格依据 115 真实响应 web/lixian?ct=lixian&ac=task_lists 抄录，
// 不要按「看起来应该叫什么」去猜——这里的字段名全部经过线上数据核对。
type OfflineTask struct {
	InfoHash     string   `json:"info_hash"`
	Name         string   `json:"name"`
	URL          string   `json:"url"` // 原始链接：magnet / ed2k
	Size         int64    `json:"size"`
	Status       int      `json:"status"`      // 2=下载成功（实测样本）；其余以 StatusText 为准
	StatusText   string   `json:"status_text"` // 服务端已给中文文案，直接展示最稳妥
	PercentDone  float64  `json:"percentDone"` // 实际可能带小数，故用 float64
	DisplayPct   int      `json:"display_percent"`
	RateDownload int64    `json:"rateDownload"` // 字节/秒
	Peers        int      `json:"peers"`
	AddTime      int64    `json:"add_time"`
	LastUpdate   int64    `json:"last_update"`
	LeftTime     int64    `json:"left_time"`
	FileID       string   `json:"file_id"`
	DeleteFileID string   `json:"delete_file_id"`
	PickCode     string   `json:"pick_code"`
	FileCategory int      `json:"file_category"`
	FileClassify string   `json:"file_classify"`
	Actions      []string `json:"available_actions"`
}

// Done 判断任务是否已下载完成。
func (t OfflineTask) Done() bool { return t.Status == 2 }

// Progress 返回便于展示的进度文案。
func (t OfflineTask) Progress() string {
	if t.Done() {
		return "已完成"
	}
	if t.DisplayPct > 0 {
		return fmt.Sprintf("%d%%", t.DisplayPct)
	}
	if t.StatusText != "" {
		return t.StatusText
	}
	return "未知"
}

// OfflineTaskList 离线任务列表响应。
//
// 重要：成功时 115 直接返回 {page, tasks, count, quota, total, ...}，
// **响应里不含 state 字段**；只有失败时才给 state/error。
// 所以 State 用指针，用来区分「未提供（=成功）」与「明确为 false（=失败）」。
type OfflineTaskList struct {
	State     *bool         `json:"state"`
	Error     string        `json:"error"`
	Page      int           `json:"page"`
	PageRow   int           `json:"page_row"`
	PageSize  int           `json:"page_size"`
	PageCount int           `json:"page_count"`
	Count     int           `json:"count"`
	Quota     int64         `json:"quota"` // 剩余配额
	Total     int64         `json:"total"` // 配额上限
	Tasks     []OfflineTask `json:"tasks"`
}
