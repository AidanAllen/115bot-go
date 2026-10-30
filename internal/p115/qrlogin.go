// Package p115 中的扫码登录实现。
//
// 与 cookie 粘贴相比，扫码登录有两个实打实的优势：
//  1. 不需要用户从浏览器开发者工具里手工抠 cookie（易错、易漏字段）；
//  2. cookie 由 115 签发并**绑定当前出口 IP 与设备**，所以必须在最终运行环境里执行，
//     否则拿到的是「别的机器的会话」，部署过去立刻失效。
//
// 接口契约（web app，已实测）：
//
//	GET  https://qrcodeapi.115.com/api/1.0/web/1.0/token/        → {uid,time,sign,qrcode}
//	GET  https://qrcodeapi.115.com/api/1.0/web/1.0/qrcode?uid=X  → PNG 图片
//	GET  https://qrcodeapi.115.com/get/status/?uid&time&sign     → {data:{status}}
//	POST https://qrcodeapi.115.com/app/1.0/web/1.0/login/qrcode/ → {data:{cookie}}
//
// 状态码：0 待扫码 / 1 已扫码待确认 / 2 已确认可取 cookie / -1 已过期 / -2 已取消。
// 注意：status 未到 2 时去取 result 会被拒（errno 40101017），所以必须严格判状态。
package p115

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

// DefaultQRApp 是默认使用的登录设备类型。
//
// 为什么不选 web（踩过的坑，也是选择依据）：
//  1. 115 的接口文档明确提示，报「IP登录异常」时尤其 app="web" 最容易中招；
//  2. 每种设备在 115 侧对应一个独立 SSOENT 槽位，同槽位只允许一个会话。
//     web 的槽位是 A1，用户随手在浏览器登录一次 115，bot 的会话就被踢下线了，
//     表现为「用着用着突然掉登录」。
//
// 为什么不选 ios/android：这两个槽位（D1/F1）正是用户手机 115 App 本体在用的，
// 选它们会把用户手机踢下线，且用户一开 App 又会把 bot 踢掉，互相伤害。
//
// 选 qandroid（「115管理_安卓端」，SSOENT=M1）：正常用户几乎不装 115管理，
// 该槽位长期空置，既不会被踢也不影响用户自己用 115。
const DefaultQRApp = "qandroid"

// QRAvailableApps 是 115 支持的全部扫码登录设备类型（键为接口参数，值为人类可读名）。
// 供 CLI 校验与前端/TG 展示使用。
var QRAvailableApps = map[string]string{
	"web":        "115生活_网页端",
	"ios":        "115生活_苹果端",
	"115ios":     "115_苹果端",
	"android":    "115生活_安卓端",
	"115android": "115_安卓端",
	"ipad":       "115生活_苹果平板端",
	"115ipad":    "115_苹果平板端",
	"tv":         "115生活_安卓电视端",
	"apple_tv":   "115生活_苹果电视端",
	"qandroid":   "115管理_安卓端",
	"qios":       "115管理_苹果端",
	"qipad":      "115管理_苹果平板端",
	"os_windows": "115生活_Windows端",
	"os_mac":     "115生活_macOS端",
	"os_linux":   "115生活_Linux端",
	"wechatmini": "115生活_微信小程序端",
	"alipaymini": "115生活_支付宝小程序",
	"harmony":    "115_鸿蒙端",
}

// QRSlot 描述一个登录槽位，用于向用户解释「选这个会不会互相踢」。
// ssoent 是 115 内部同一槽位的标识，同槽位新登录会顶掉旧会话。
var QRSlot = map[string]string{
	"web": "A1", "desktop": "A1",
	"ios": "D1", "bios": "D2", "115ios": "D3",
	"android": "F1", "bandroid": "F2", "115android": "F3",
	"ipad": "H1", "bipad": "H2", "115ipad": "H3",
	"tv": "I1", "apple_tv": "I2",
	"qandroid": "M1", "qios": "N1", "qipad": "O1",
	"os_windows": "P1", "windows": "P1",
	"os_mac": "P2", "mac": "P2",
	"os_linux": "P3", "linux": "P3",
	"wechatmini": "R1", "alipaymini": "R2",
	"harmony": "S1",
}

// normalizeQRApp 把别名归一化为 115 接口实际接受的设备名，
// 并对少数设备返回必须携带的 User-Agent（不匹配会被识别为伪造设备）。
//
// 规则来自 115 实际接口行为：desktop 等同 web；windows/mac/linux 加 os_ 前缀；
// ios 系列统一走 ios 但要用各自的 UA 区分手机/管理端/平板。
func normalizeQRApp(app string) (name string, ua string) {
	switch app = strings.TrimSpace(app); app {
	case "":
		return DefaultQRApp, ""
	case "desktop":
		return "web", ""
	case "windows":
		return "os_windows", ""
	case "mac":
		return "os_mac", ""
	case "linux":
		return "os_linux", ""
	case "ios":
		return "ios", "UPhone/1.0.0"
	case "qios":
		return "ios", "OfficePhone/1.0.0"
	case "ipad":
		return "ios", "UPad/1.0.0"
	case "qipad":
		return "ios", "OfficePad/1.0.0"
	}
	return app, ""
}

// qrPaths 是各接口相对基地址的路径模板。
type qrPaths struct {
	Token  string
	Image  string
	Status string
	Result string // %s 为 app 名
}

var defaultQRPaths = qrPaths{
	Token:  "/api/1.0/web/1.0/token/",
	Image:  "/api/1.0/web/1.0/qrcode?uid=%s",
	Status: "/get/status/",
	Result: "/app/1.0/%s/1.0/login/qrcode/",
}

// defaultQRBases 是按优先级排列的服务基地址。
//
// 为什么需要多个：115 的扫码服务走阿里云 WAF，实测在境外机器上，
// 到 qrcodeapi.115.com 的 **HTTPS 握手会在证书交换后段被中断**（TCP 可连、TLS 不通），
// 而同一域名的明文 HTTP 正常、备用域名 hnqrcodeapi.115.com 的 HTTPS 也正常。
// 因此按「HTTPS 主域名 → HTTPS 备域名 → HTTP 主域名」降级，
// 优先保证加密传输，实在不行才退到明文（并在日志中明确告警）。
var defaultQRBases = []string{
	"https://qrcodeapi.115.com",
	"https://hnqrcodeapi.115.com",
	"http://qrcodeapi.115.com",
}

// QRStatus 是扫码流程的阶段状态，取值与 115 返回的 data.status 一致。
type QRStatus int

const (
	QRWaiting  QRStatus = 0   // 尚未扫码
	QRScanned  QRStatus = 1   // 已扫码，等待手机端点「确认登录」
	QRSigned   QRStatus = 2   // 已确认，可换取 cookie
	QRExpired  QRStatus = -1  // 二维码过期
	QRCanceled QRStatus = -2  // 用户在手机上取消
	QRAborted  QRStatus = -99 // 本地判定为异常终止
)

// String 便于日志与 TG 文案直接使用。
func (s QRStatus) String() string {
	switch s {
	case QRWaiting:
		return "waiting"
	case QRScanned:
		return "scanned"
	case QRSigned:
		return "signed"
	case QRExpired:
		return "expired"
	case QRCanceled:
		return "canceled"
	case QRAborted:
		return "aborted"
	}
	return "unknown(" + strconv.Itoa(int(s)) + ")"
}

// QRToken 是一次扫码会话的凭据，由 115 签发。
type QRToken struct {
	UID       string
	Time      int64
	Sign      string
	ScanURL   string    // 形如 https://115.com/scan/dg-<uid>
	IssuedAt  time.Time // 本地记录，用于自行判定超时
	ExpiresAt time.Time
}

// QRLogin 是扫码登录客户端。
type QRLogin struct {
	hc     *http.Client
	paths  qrPaths
	bases  []string
	ua     string
	app    string // 登录设备类型，决定 115 侧占用哪个 SSOENT 槽位
	maxAge time.Duration
}

// NewQRLogin 创建扫码客户端。timeout 为单次 HTTP 超时，默认使用 DefaultQRApp 设备类型。
func NewQRLogin(timeout time.Duration) *QRLogin {
	return NewQRLoginWithApp(timeout, DefaultQRApp)
}

// NewQRLoginWithApp 指定登录设备类型创建客户端。
// app 为空时回落到 DefaultQRApp；未知值不会被拦截，交由 115 判定（便于新类型先试后补）。
func NewQRLoginWithApp(timeout time.Duration, app string) *QRLogin {
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	// TLS 握手单独收紧：境外到 115 的握手失败是常态，必须快速失败才能及时切到备用地址。
	tr := &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 8 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   6 * time.Second,
		MaxIdleConns:          4,
		MaxIdleConnsPerHost:   2,
		IdleConnTimeout:       60 * time.Second,
		ResponseHeaderTimeout: timeout,
	}

	// name 是发给 115 的设备参数，ua 是该设备必须配套的 User-Agent（少数设备才有，
	// 为空表示沿用网页 UA）。两者必须成对保存：整个登录流程（取码 / 轮询 / 换 cookie）
	// 都用同一份 UA，事后写库也用这一份，否则 UA 与会话不匹配会被判为异常设备。
	name, ua := normalizeQRApp(app)
	if ua == "" {
		ua = DefaultUA
	}
	return &QRLogin{
		hc:     &http.Client{Timeout: timeout, Transport: tr},
		paths:  defaultQRPaths,
		bases:  defaultQRBases,
		ua:     ua,
		app:    name,
		maxAge: 180 * time.Second, // 115 二维码有效期约 3 分钟，本地先留同量级上限
	}
}

// App 返回实际使用的设备类型（已归一化）。
func (q *QRLogin) App() string { return q.app }

// UA 返回本次登录使用的 User-Agent。
//
// 登录成功后必须用这个值写库（而不是另行取 p115.DefaultUA）：115 会校验
// UA 与会话的一致性，尤其 ios 系列设备各自带专用 UA，写错会被判为异常设备。
func (q *QRLogin) UA() string { return q.ua }

// qrState 抽取 115 各接口共有的结果字段。
// 115 的 state 在不同接口上既可能是 1/0，也可能是 true/false，故用 RawMessage 兜住。
type qrState struct {
	State   json.RawMessage `json:"state"`
	Code    json.RawMessage `json:"code"`
	Errno   json.RawMessage `json:"errno"`
	Error   string          `json:"error"`
	Message string          `json:"message"`
}

func (s qrState) ok() bool {
	v := strings.TrimSpace(string(s.State))
	return v == "1" || v == "true"
}

// describe 组装可读的错误描述，便于直接回显到 TG。
func (s qrState) describe(prefix string) string {
	parts := make([]string, 0, 3)
	for _, v := range []struct {
		k string
		r json.RawMessage
	}{{"code", s.Code}, {"errno", s.Errno}} {
		if t := rawToString(v.r); t != "" && t != "0" {
			parts = append(parts, v.k+"="+t)
		}
	}
	msg := s.Error
	if msg == "" {
		msg = s.Message
	}
	if msg != "" {
		parts = append(parts, msg)
	}
	if len(parts) == 0 {
		return prefix + "（115 未给出原因）"
	}
	return prefix + "：" + strings.Join(parts, " ")
}

// rawToString 把可能是数字、字符串或 null 的 JSON 值转成字符串。
func rawToString(r json.RawMessage) string {
	t := strings.TrimSpace(string(r))
	if t == "" || t == "null" {
		return ""
	}
	if unq, err := strconv.Unquote(t); err == nil {
		return unq
	}
	return t
}

// doJSON 发起请求并把响应解析进 out，同时做通用成功判定。
// 同时返回原始响应字节，便于调用方落盘诊断（尤其是 cookie 结构可能与预期不同）。
func (q *QRLogin) doJSON(ctx context.Context, method, path string, form url.Values, out any) (json.RawMessage, error) {
	return q.doJSONUA(ctx, method, path, form, out, q.ua)
}

// doJSONUA 与 doJSON 相同，但可指定本次请求的 User-Agent。
// 个别设备（如 ios 系）在换 cookie 时必须带对应 UA，否则会被判定为伪造设备。
//
// 容错策略分两层：
//  1. 网络层错误/5xx → 换下一个基地址重试（境外到 115 的 TLS 握手失败是常态）；
//  2. 业务错误（如参数非法、IP 异常）→ 立即返回，换地址没有任何帮助，还会徒增风控计数。
func (q *QRLogin) doJSONUA(ctx context.Context, method, path string, form url.Values, out any, ua string) (json.RawMessage, error) {
	var lastRaw json.RawMessage
	var lastErr error
	tried := make([]string, 0, len(q.bases))

	for _, base := range q.bases {
		full := base + path
		for attempt := 1; attempt <= 2; attempt++ {
			raw, retryable, err := q.doOnce(ctx, method, full, form, out, ua)
			if err == nil {
				return raw, nil
			}
			if !retryable {
				// 业务层拒绝，换地址无意义
				return raw, err
			}
			lastRaw, lastErr = raw, err
			if attempt == 2 || ctx.Err() != nil {
				break
			}
			select {
			case <-ctx.Done():
				return lastRaw, ctx.Err()
			case <-time.After(time.Duration(attempt) * 500 * time.Millisecond):
			}
		}
		tried = append(tried, base)
	}

	if len(tried) > 1 {
		return lastRaw, fmt.Errorf("所有 115 扫码地址均不可用（已尝试 %s），最后错误: %w", strings.Join(tried, "、"), lastErr)
	}
	return lastRaw, lastErr
}

// doOnce 是单次请求。第二个返回值表示该错误是否值得重试。
func (q *QRLogin) doOnce(ctx context.Context, method, rawURL string, form url.Values, out any, ua string) (json.RawMessage, bool, error) {
	var body io.Reader
	if form != nil {
		body = strings.NewReader(form.Encode())
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, false, fmt.Errorf("构造请求失败: %w", err)
	}
	if ua == "" {
		ua = q.ua
	}
	req.Header.Set("User-Agent", ua)
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	if form != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := q.hc.Do(req)
	if err != nil {
		return nil, true, fmt.Errorf("请求 115 失败: %w", err)
	}
	defer resp.Body.Close()

	// 限制读取大小，避免异常响应把内存打满（本项目对内存敏感）。
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, true, fmt.Errorf("读取 115 响应失败: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		// 5xx 属于服务端抖动，值得重试；4xx 不会因重试而好转。
		return raw, resp.StatusCode >= 500, fmt.Errorf("115 返回 HTTP %d", resp.StatusCode)
	}

	var st qrState
	if err := json.Unmarshal(raw, &st); err != nil {
		return raw, false, fmt.Errorf("115 响应不是合法 JSON（前 80 字节: %q）", truncate(string(raw), 80))
	}
	if !st.ok() {
		return raw, false, fmt.Errorf("%s", st.describe("115 拒绝了请求"))
	}
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			return raw, false, fmt.Errorf("解析 115 响应失败: %w", err)
		}
	}
	return raw, false, nil
}

// Token 申请一次扫码会话。
func (q *QRLogin) Token(ctx context.Context) (*QRToken, error) {
	var r struct {
		Data struct {
			UID    string `json:"uid"`
			Time   int64  `json:"time"`
			Sign   string `json:"sign"`
			Qrcode string `json:"qrcode"`
		} `json:"data"`
	}
	if _, err := q.doJSON(ctx, http.MethodGet, q.paths.Token, nil, &r); err != nil {
		return nil, err
	}
	if r.Data.UID == "" || r.Data.Sign == "" || r.Data.Time == 0 {
		return nil, fmt.Errorf("115 返回的二维码会话不完整（uid/sign/time 有缺失）")
	}
	now := time.Now()
	return &QRToken{
		UID:       r.Data.UID,
		Time:      r.Data.Time,
		Sign:      r.Data.Sign,
		ScanURL:   firstNonEmpty(r.Data.Qrcode, "https://115.com/scan/dg-"+r.Data.UID),
		IssuedAt:  now,
		ExpiresAt: now.Add(q.maxAge),
	}, nil
}

// Image 下载 115 官方的二维码图片（PNG 原始字节）。
// 直接取官方图而不是自行编码二维码内容，可避免「内容拼错导致扫不出来」这类难查的问题。
func (q *QRLogin) Image(ctx context.Context, uid string) ([]byte, error) {
	path := fmt.Sprintf(q.paths.Image, url.QueryEscape(uid))
	var lastErr error
	for _, base := range q.bases {
		img, err := q.fetchPNG(ctx, base+path)
		if err == nil {
			return img, nil
		}
		lastErr = err
		if ctx.Err() != nil {
			break
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("没有可用的二维码地址")
	}
	return nil, lastErr
}

// fetchPNG 从指定地址取二维码 PNG。
func (q *QRLogin) fetchPNG(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("构造二维码请求失败: %w", err)
	}
	req.Header.Set("User-Agent", q.ua)
	req.Header.Set("Referer", "https://115.com/")

	resp, err := q.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("下载二维码失败: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("下载二维码失败: HTTP %d", resp.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return nil, fmt.Errorf("读取二维码失败: %w", err)
	}
	if len(raw) < 8 || !bytes.HasPrefix(raw, []byte("\x89PNG")) {
		return nil, fmt.Errorf("二维码响应不是 PNG（前 8 字节: %x）", head(raw, 8))
	}
	return raw, nil
}

// Status 查询扫码状态。
func (q *QRLogin) Status(ctx context.Context, tok *QRToken) (QRStatus, error) {
	if tok == nil {
		return QRAborted, fmt.Errorf("扫码会话为空")
	}
	if time.Now().After(tok.ExpiresAt) {
		return QRExpired, nil
	}
	// 115 的状态接口要求带上签发时的 time，不能拿本地当前时间。
	qy := url.Values{}
	qy.Set("uid", tok.UID)
	qy.Set("time", strconv.FormatInt(tok.Time, 10))
	qy.Set("sign", tok.Sign)
	path := q.paths.Status + "?" + qy.Encode()

	var r struct {
		Data struct {
			Status *int   `json:"status"`
			Msg    string `json:"msg"`
		} `json:"data"`
	}
	if _, err := q.doJSON(ctx, http.MethodGet, path, nil, &r); err != nil {
		return QRAborted, err
	}
	if r.Data.Status == nil {
		// 个别版本在 data 之外直接给 state，此时视为「仍在等待」而不是失败。
		return QRWaiting, nil
	}
	switch QRStatus(*r.Data.Status) {
	case QRWaiting, QRScanned, QRSigned, QRExpired, QRCanceled:
		return QRStatus(*r.Data.Status), nil
	default:
		return QRAborted, fmt.Errorf("115 返回了未知扫码状态 %d（msg=%q）", *r.Data.Status, r.Data.Msg)
	}
}

// ResultFull 在状态为 QRSigned 后换取 cookie，并同时返回 115 的原始响应。
// 返回的 cookie 是完整会话串，可直接用于 webapi.115.com。
// 保留原文是为了在解析失败时能落盘排查——cookie 字段存在多种形态，实测踩过坑。
func (q *QRLogin) ResultFull(ctx context.Context, uid string) (string, json.RawMessage, error) {
	form := url.Values{}
	form.Set("account", uid)

	app, appUA := normalizeQRApp(q.app)
	raw, err := q.doJSONUA(ctx, http.MethodPost, fmt.Sprintf(q.paths.Result, app), form, nil, appUA)
	if err != nil {
		return "", raw, err
	}
	cookie, err := ParseCookie(raw)
	if err != nil {
		return "", raw, err
	}
	return cookie, raw, nil
}

// ParseCookie 从登录响应里提取 cookie。
//
// 115 的 cookie 字段有两种形态，实测都出现过：
//   - 字符串：`"UID=xxx; CID=yyy"`；
//   - 对象：`{"UID":"xxx","CID":"yyy",...}`，web 端扫码返回的就是这种。
//
// 只按字符串解析会在「已经扫码成功」的情况下报解析失败，这是实际踩到的第一个坑，
// 所以这里做成双形态兼容，并由单元测试锁住两种路径。
func ParseCookie(raw json.RawMessage) (string, error) {
	var top struct {
		Data struct {
			Cookie json.RawMessage `json:"cookie"`
		} `json:"data"`
		Cookie json.RawMessage `json:"cookie"`
	}
	if err := json.Unmarshal(raw, &top); err != nil {
		return "", fmt.Errorf("解析登录响应失败: %w", err)
	}
	cv := top.Data.Cookie
	if len(cv) == 0 || string(cv) == "null" {
		cv = top.Cookie
	}
	if len(cv) == 0 || string(cv) == "null" {
		return "", fmt.Errorf("115 响应中没有 cookie 字段")
	}
	return cookieFromValue(cv)
}

// cookieFromValue 把 cookie 字段的任意形态归一化为 `k=v; k2=v2`。
func cookieFromValue(v json.RawMessage) (string, error) {
	t := strings.TrimSpace(string(v))
	switch {
	case strings.HasPrefix(t, `"`):
		var s string
		if err := json.Unmarshal(v, &s); err != nil {
			return "", fmt.Errorf("cookie 字符串解析失败: %w", err)
		}
		if s = strings.TrimSpace(s); s == "" {
			return "", fmt.Errorf("115 返回了空 cookie")
		}
		return strings.TrimRight(s, "; "), nil

	case strings.HasPrefix(t, "{"):
		var m map[string]json.RawMessage
		if err := json.Unmarshal(v, &m); err != nil {
			return "", fmt.Errorf("cookie 对象解析失败: %w", err)
		}
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		// 排序只为输出稳定，cookie 各键顺序本身不影响语义。
		sort.Strings(keys)

		parts := make([]string, 0, len(keys))
		for _, k := range keys {
			val := strings.TrimSpace(string(m[k]))
			if val == "" || val == "null" {
				continue
			}
			var s string
			if err := json.Unmarshal(m[k], &s); err == nil {
				if s = strings.TrimSpace(s); s != "" {
					parts = append(parts, k+"="+s)
				}
				continue
			}
			// 数字/布尔等标量取字面量；嵌套结构不该出现在 cookie 里，跳过。
			if !strings.HasPrefix(val, "{") && !strings.HasPrefix(val, "[") {
				parts = append(parts, k+"="+val)
			}
		}
		if len(parts) == 0 {
			return "", fmt.Errorf("115 返回的 cookie 对象为空")
		}
		return strings.Join(parts, "; "), nil

	default:
		return "", fmt.Errorf("cookie 字段既不是字符串也不是对象（前 40 字节: %q）", truncate(t, 40))
	}
}

// WaitForLogin 阻塞等待用户完成扫码，成功时返回 cookie。
//
// onStatus 会在状态发生变化时被回调（waiting 阶段只在首次触发，避免刷屏）。
// 返回的错误已可读，可直接展示给用户。
func (q *QRLogin) WaitForLogin(ctx context.Context, tok *QRToken, interval time.Duration, onStatus func(QRStatus)) (string, json.RawMessage, error) {
	if interval <= 0 {
		interval = 2 * time.Second
	}
	last := QRStatus(-999)
	notify := func(s QRStatus) {
		if onStatus == nil || s == last {
			return
		}
		last = s
		onStatus(s)
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		st, err := q.Status(ctx, tok)
		if err != nil {
			// 网络抖动不该直接判定失败，交给上层超时控制。
			if ctx.Err() != nil {
				return "", nil, ctx.Err()
			}
		} else {
			notify(st)
			switch st {
			case QRSigned:
				return q.ResultFull(ctx, tok.UID)
			case QRExpired:
				return "", nil, fmt.Errorf("二维码已过期，请重新发起扫码")
			case QRCanceled:
				return "", nil, fmt.Errorf("已在手机端取消登录")
			}
		}

		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		case <-ticker.C:
		}
	}
}

// UpscalePNG 对 PNG 做整数倍最近邻放大。
//
// 115 官方二维码只有 205×205，直接投到手机屏幕上偏小、不好对焦。
// 放大只复制像素、不改变二维码内容，因此不影响可扫性。
func UpscalePNG(src []byte, factor int) ([]byte, error) {
	if factor <= 1 {
		return src, nil
	}
	img, err := png.Decode(bytes.NewReader(src))
	if err != nil {
		return nil, fmt.Errorf("解码二维码失败: %w", err)
	}
	b := img.Bounds()
	w, h := b.Dx()*factor, b.Dy()*factor

	// 先在水平方向展开成行，再整体复制行，比逐像素 Set 快且不额外占用接口开销。
	row := make([]byte, 0, w*4)
	dst := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < b.Dy(); y++ {
		row = row[:0]
		for x := 0; x < b.Dx(); x++ {
			r, g, bl, a := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			r8, g8, b8, a8 := uint8(r>>8), uint8(g>>8), uint8(bl>>8), uint8(a>>8)
			for i := 0; i < factor; i++ {
				row = append(row, r8, g8, b8, a8)
			}
		}
		for i := 0; i < factor; i++ {
			copy(dst.Pix[(y*factor+i)*dst.Stride:], row)
		}
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, dst); err != nil {
		return nil, fmt.Errorf("编码二维码失败: %w", err)
	}
	return buf.Bytes(), nil
}

// DefaultUA 是 web 端通用 UA。115 对空 UA 会返回风控页，
// 且登录时的 UA 与后续请求的 UA 应保持一致，否则容易被判定为会话异常。
// 全项目应以本常量为唯一来源。
const DefaultUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/122.0.0.0 Safari/537.36"

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}

func head(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}
