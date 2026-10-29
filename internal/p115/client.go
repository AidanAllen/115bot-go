package p115

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// endpoints 集中声明 115 各接口地址。
// 抽成结构体而非散落常量，便于测试时整体替换为 mock 服务。
type endpoints struct {
	Nav         string
	Category    string
	Files       string
	Delete      string
	OfflineAdd  string
	OfflineList string
	OfflineDel  string
}

// defaultEndpoints 线上地址。
//
// 离线相关继续使用 115.com/web/lixian 这一套：实测新域名 clouddownload.115.com
// 的 add_task_urls 会返回「请升级新版本的115客户端」，而旧路径可正常下发。
var defaultEndpoints = endpoints{
	Nav:         "https://my.115.com/?ct=ajax&ac=nav",
	Category:    "https://webapi.115.com/category/get?cid=%s",
	Files:       "https://webapi.115.com/files",
	Delete:      "https://webapi.115.com/rb/delete",
	OfflineAdd:  "https://115.com/web/lixian/?ct=lixian&ac=add_task_urls",
	OfflineList: "https://115.com/web/lixian/?ct=lixian&ac=task_lists&page=%d",
	OfflineDel:  "https://115.com/web/lixian/?ct=lixian&ac=task_del",
}

// ErrLoginRequired 表示 115 未登录或登录态已失效。
var ErrLoginRequired = errors.New("115 登录态无效，请重新设置 cookie")

// APIError 远端返回的业务错误。
type APIError struct {
	Op      string
	Message string
	Errno   string
}

func (e *APIError) Error() string {
	// OSS 相关错误来自阿里云而不是 115，措辞上区分开，便于用户判断问题归属
	if strings.HasPrefix(e.Op, "oss") {
		name := strings.TrimPrefix(strings.TrimPrefix(e.Op, "oss"), "/")
		if name == "" {
			name = "上传"
		}
		if e.Errno != "" {
			return fmt.Sprintf("OSS %s失败（%s）: %s", name, e.Errno, e.Message)
		}
		return fmt.Sprintf("OSS %s失败: %s", name, e.Message)
	}
	if e.Errno != "" {
		return fmt.Sprintf("115 接口 %s 返回错误(errno=%s): %s", e.Op, e.Errno, e.Message)
	}
	return fmt.Sprintf("115 接口 %s 返回错误: %s", e.Op, e.Message)
}

// Client 是 115 Web 端接口客户端（基于 cookie + UA）。
type Client struct {
	hc     *http.Client
	cookie string
	ua     string
	ep     endpoints
}

// New 创建客户端。cookie / ua 为空时调用会直接返回 ErrLoginRequired。
func New(cookie, ua string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &Client{
		hc: &http.Client{
			Timeout: timeout,
			// 不自动跟随 302 到登录页，便于识别失效
			CheckRedirect: func(req *http.Request, via []*http.Request) error {
				if len(via) >= 5 {
					return errors.New("重定向次数过多")
				}
				return nil
			},
		},
		cookie: strings.TrimSpace(cookie),
		ua:     strings.TrimSpace(ua),
		ep:     defaultEndpoints,
	}
}

// newWithEndpoints 供测试注入 mock 地址。
func newWithEndpoints(cookie, ua string, timeout time.Duration, ep endpoints) *Client {
	c := New(cookie, ua, timeout)
	c.ep = ep
	return c
}

// Ready 判断是否已具备调用条件（cookie 与 ua 均已配置）。
func (c *Client) Ready() bool { return c.cookie != "" && c.ua != "" }

func (c *Client) newRequest(ctx context.Context, method, rawURL string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Cookie", c.cookie)
	req.Header.Set("User-Agent", c.ua)
	req.Header.Set("Referer", "https://115.com/")
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Accept-Language", "zh-CN,zh;q=0.9")
	return req, nil
}

func (c *Client) do(req *http.Request) ([]byte, error) {
	if !c.Ready() {
		return nil, ErrLoginRequired
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求 115 失败: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	// 限制读取上限，避免异常响应占用内存
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, fmt.Errorf("读取 115 响应失败: %w", err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("115 返回状态码 %d", resp.StatusCode)
	}
	return b, nil
}

func (c *Client) get(ctx context.Context, rawURL string) ([]byte, error) {
	req, err := c.newRequest(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	return c.do(req)
}

func (c *Client) postForm(ctx context.Context, rawURL string, form url.Values) ([]byte, error) {
	req, err := c.newRequest(ctx, http.MethodPost, rawURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return c.do(req)
}

// decodeEnvelope 解析通用外层结构，并根据 state 判定是否登录失效。
func decodeEnvelope(op string, body []byte) (*envelope, error) {
	var env envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("解析 %s 响应失败: %w", op, err)
	}
	if !env.State {
		msg := strings.TrimSpace(env.Error)
		if msg == "" {
			msg = "未知错误"
		}
		if isLoginError(msg) {
			return nil, ErrLoginRequired
		}
		return nil, &APIError{Op: op, Message: msg}
	}
	return &env, nil
}

// isLoginError 识别 115 的登录失效文案。
func isLoginError(msg string) bool {
	for _, kw := range []string{"请先登录", "登录失效", "登陆失效", "未登录"} {
		if strings.Contains(msg, kw) {
			return true
		}
	}
	return false
}
