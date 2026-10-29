package p115

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
)

// LinkType 资源链接类型。
type LinkType string

const (
	LinkMagnet LinkType = "magnet"
	LinkEd2k   LinkType = "ed2k"
)

// Link 一条可下发到 115 离线下载的资源链接。
type Link struct {
	Type LinkType
	URL  string
}

var (
	// 磁力链：magnet:?xt=urn:btih:<hash> 后可跟任意参数
	magnetRe = regexp.MustCompile(`(?i)magnet:\?xt=urn:btih:[0-9a-z]{32,40}(?:&[^\s"'<>）】\]]+)*`)
	// ed2k：ed2k://|file|名称|大小|哈希|/ 或带 h= 参数
	ed2kRe = regexp.MustCompile(`(?i)ed2k://\|file\|[^|\r\n]+\|\d+\|[0-9a-f]{32}\|(?:h=[0-9a-z]{32}\|)?/?`)
)

// ExtractLinks 从任意文本中提取磁力链与 ed2k 链接，按出现顺序返回并去重。
func ExtractLinks(text string) []Link {
	var out []Link
	seen := make(map[string]struct{})

	add := func(typ LinkType, raw string) {
		u := strings.TrimSpace(raw)
		// 去掉尾部可能粘连的中英文标点
		u = strings.TrimRight(u, ".,;:，。；：、！？!?）)】]\"'")
		if u == "" {
			return
		}
		key := strings.ToLower(u)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		out = append(out, Link{Type: typ, URL: u})
	}

	for _, m := range magnetRe.FindAllString(text, -1) {
		add(LinkMagnet, m)
	}
	for _, m := range ed2kRe.FindAllString(text, -1) {
		add(LinkEd2k, m)
	}
	return out
}

// ErrInvalidCookie cookie 格式不合法。
var ErrInvalidCookie = errors.New("cookie 格式错误")

// ValidateCookie 做本地格式校验，避免把明显无效的内容提交给远端。
//
// 115 存在两类有效会话，满足其一即可：
//  1. 网页会话：必须含 USERSESSIONID（会话主键），形如
//     USERSESSIONID=xxx; UID=1@xx; CID=abc; SEID=xxx
//  2. 扫码 / 第三方登录会话（alipaymini、各客户端）：没有 USERSESSIONID，
//     但一定有 UID、CID、SEID 三项，形如
//     CID=xxx; KID=xxx; SEID=xxx; UID=342204514_R2_1973193990
//
// 只要求 USERSESSIONID 会把扫码登录出来的会话全部误判为非法（/cookie 与 --check-cookie 会被拦）。
func ValidateCookie(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return fmt.Errorf("%w：内容为空", ErrInvalidCookie)
	}
	if strings.ContainsAny(s, "\r\n") {
		return fmt.Errorf("%w：不能包含换行", ErrInvalidCookie)
	}
	if len(s) > 4096 {
		return fmt.Errorf("%w：长度超过 4096 字符", ErrInvalidCookie)
	}

	pairs := 0
	hasSession := false
	// 扫码会话三件套：UID / CID / SEID
	uid, cid, seid := false, false, false
	for _, part := range strings.Split(s, ";") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		k, _, ok := strings.Cut(part, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("%w：存在不合法片段 %q", ErrInvalidCookie, truncate(part, 24))
		}
		pairs++
		switch strings.ToUpper(strings.TrimSpace(k)) {
		case "USERSESSIONID":
			hasSession = true
		case "UID":
			uid = true
		case "CID":
			cid = true
		case "SEID":
			seid = true
		}
	}
	if pairs == 0 {
		return fmt.Errorf("%w：未解析到任何 name=value", ErrInvalidCookie)
	}
	if !hasSession && !(uid && cid && seid) {
		return fmt.Errorf("%w：缺少会话标识（需要 USERSESSIONID，或 UID+CID+SEID 组合）", ErrInvalidCookie)
	}
	return nil
}

// ErrInvalidCID cid 不合法。
var ErrInvalidCID = errors.New("cid 格式错误")

// NormalizeCID 校验并规范化目录 cid。115 的 cid 为纯数字串。
func NormalizeCID(raw string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", fmt.Errorf("%w：内容为空", ErrInvalidCID)
	}
	if len(s) > 24 {
		return "", fmt.Errorf("%w：长度过长", ErrInvalidCID)
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("%w：请输入数字", ErrInvalidCID)
		}
	}
	// 115 根目录 cid 为 0，允许读取但业务上禁止作为落盘目标
	return s, nil
}

// ValidateUA 做 UA 的轻量校验。
func ValidateUA(raw string) error {
	s := strings.TrimSpace(raw)
	if s == "" {
		return errors.New("ua 不能为空")
	}
	if len(s) > 512 {
		return errors.New("ua 长度超过 512 字符")
	}
	if strings.ContainsAny(s, "\r\n") {
		return errors.New("ua 不能包含换行")
	}
	return nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
