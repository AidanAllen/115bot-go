package p115

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// 上传相关接口地址。
//
// 注意：uploadinfo 里的 url_upload 指向 2.0/upload.php，但该接口已废弃
// （实测返回「客户端版本太低，请更新到最新版本！」）。必须用 4.0/initupload.php。
const (
	urlUploadInfo     = "https://proapi.115.com/app/uploadinfo"
	urlUploadInit     = "https://uplb.115.com/4.0/initupload.php"
	urlUploadToken    = "https://uplb.115.com/3.0/gettoken.php"
	urlUploadEndpoint = "https://uplb.115.com/3.0/getuploadinfo.php"
)

// 跨境链路的超时与重试参数。
const (
	// uploadDialTimeout 建连超时。
	uploadDialTimeout = 30 * time.Second
	// uploadTLSHandshakeTimeout 握手超时需要单独收紧：境外到 115 的握手经常被拖住。
	uploadTLSHandshakeTimeout = 15 * time.Second
	// uploadHeaderTimeout 等待响应头上限。
	//
	// 不设它会踩坑：单连接僵死时请求会永久挂起（实测进程卡 10 分钟无进展）。
	uploadHeaderTimeout = 5 * time.Minute
	// uploadAPIAttempts 轻量接口的重试次数。
	uploadAPIAttempts = 3
)

// uploadInfo 是 app/uploadinfo 的响应。
//
// ⚠️ 该接口字段**平铺在顶层**（没有 data 包装），和 category/get 一样。
type uploadInfo struct {
	State      bool   `json:"state"`
	Error      string `json:"error"`
	UserID     int64  `json:"user_id"`
	UserKey    string `json:"userkey"`
	SizeLimit  int64  `json:"size_limit"`
	Allowed    bool   `json:"upload_allowed"`
	AllowedMsg string `json:"upload_allowed_msg"`
}

// uploadCtx 是上传所需的账号上下文。
type uploadCtx struct {
	UserID    int64
	UserKey   string
	SizeLimit int64
}

// ossToken 是阿里云 OSS 的 STS 临时凭据。
type ossToken struct {
	AccessKeyID     string `json:"AccessKeyId"`
	AccessKeySecret string `json:"AccessKeySecret"`
	SecurityToken   string `json:"SecurityToken"`
	Expiration      string `json:"Expiration"`
	StatusCode      string `json:"StatusCode"`

	fetchedAt time.Time
}

func (t ossToken) valid() bool {
	return t.AccessKeyID != "" && t.AccessKeySecret != "" && t.SecurityToken != "" &&
		time.Since(t.fetchedAt) < 30*time.Minute
}

// uploadInitResp 是 4.0/initupload.php 解密后的响应。
type uploadInitResp struct {
	Request    string      `json:"request"`
	Status     int         `json:"status"`
	StatusCode int         `json:"statuscode"`
	StatusMsg  string      `json:"statusmsg"`
	PickCode   string      `json:"pickcode"`
	Bucket     string      `json:"bucket"`
	Object     string      `json:"object"`
	SignKey    string      `json:"sign_key"`
	SignCheck  string      `json:"sign_check"`
	Callback   ossCallback `json:"callback"`
}

// uploadStatus 语义（实测确认）。
const (
	uploadStatusNeedReal = 1 // 需要真实上传
	uploadStatusReuse    = 2 // 秒传成功
	uploadStatusSign     = 7 // 需要按区间做二次校验
)

// webUploader 是基于网页 cookie 的上传通道。
//
// 这是本项目唯一的上传实现：既能秒传，也能完成真实上传（含 OSS 分片）。
//
//	uploadinfo → initupload(4.0, 加密) → gettoken(STS) → OSS PUT → callback 入库
type webUploader struct {
	client *Client
	hc     *http.Client
	// ch 是本次上传使用的通道（客户端身份）
	ch UploadChannel

	mu    sync.Mutex
	info  uploadCtx
	token ossToken
	ossEP ossEndpoint

	// appVersion 参与签名，按所选通道注入
	appVersion string
	// ua 是该通道声明的 User-Agent，所有上传相关请求统一用它
	ua string
	// partSize 分片大小，允许测试覆盖
	partSize int64
	// 接口地址，允许测试整体替换
	urlInfo, urlInit, urlToken, urlEndpoint string
}

// newWebUploader 创建网页上传器，使用默认通道。
func newWebUploader(c *Client) *webUploader {
	return newWebUploaderWithChannel(c, DefaultUploadChannel())
}

// newWebUploaderWithChannel 用指定通道创建上传器。
//
// 通道同时决定 appversion（进签名）与 UA（进所有请求头），两者必须成对，
// 否则 115 侧的客户端一致性判断可能直接拒掉握手。
func newWebUploaderWithChannel(c *Client, ch UploadChannel) *webUploader {
	if ch.AppVersion == "" {
		ch = DefaultUploadChannel()
	}
	ua := strings.TrimSpace(ch.UA)
	if ua == "" {
		ua = uploadUA
	}
	return &webUploader{
		client:      c,
		hc:          newUploadHTTPClient(),
		ch:          ch,
		appVersion:  ch.AppVersion,
		ua:          ua,
		partSize:    multipartPartSize,
		urlInfo:     urlUploadInfo,
		urlInit:     urlUploadInit,
		urlToken:    urlUploadToken,
		urlEndpoint: urlUploadEndpoint,
	}
}

func (u *webUploader) Available() bool { return u != nil && u.client != nil && u.client.Ready() }

// Name 返回当前所用通道名，菜单与 /state 展示的就是它。
func (u *webUploader) Name() string {
	if u != nil && u.ch.Name != "" {
		return u.ch.Name
	}
	return DefaultUploadChannel().Name
}

// newUploadHTTPClient 构建上传用 HTTP 客户端。
//
// 不设 Client.Timeout（大文件整体耗时不可预估，交给 ctx 控制），
// 但必须给「等待响应头」设上限，否则跨境僵死会让请求永久挂起。
func newUploadHTTPClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   uploadDialTimeout,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			TLSHandshakeTimeout:   uploadTLSHandshakeTimeout,
			ResponseHeaderTimeout: uploadHeaderTimeout,
			ExpectContinueTimeout: 5 * time.Second,
			MaxIdleConnsPerHost:   4,
			IdleConnTimeout:       90 * time.Second,
			// 不压缩：上传都是二进制流，压缩无意义且会干扰 Content-Length
			DisableCompression: true,
		},
	}
}

// Upload 执行完整上传，返回 pick_code。
func (u *webUploader) Upload(ctx context.Context, localPath, fileName string, size int64, targetCID string) (string, error) {
	if !u.Available() {
		return "", ErrLoginRequired
	}
	if size <= 0 {
		return "", fmt.Errorf("文件大小非法: %d", size)
	}

	sum, err := FileSHA1(localPath)
	if err != nil {
		return "", err
	}
	target := normalizeTarget(targetCID)

	info, err := u.uploadContext(ctx)
	if err != nil {
		return "", err
	}
	if info.SizeLimit > 0 && size > info.SizeLimit {
		return "", fmt.Errorf("文件超过 115 单文件上限（%.1f GB > %.1f GB）",
			float64(size)/(1<<30), float64(info.SizeLimit)/(1<<30))
	}

	req := uploadInitReq{
		FileID:   sum,
		FileName: fileName,
		FileSize: size,
		Target:   target,
	}
	resp, err := u.initUpload(ctx, info, req)
	if err != nil {
		return "", err
	}

	// 二次校验：115 要求按指定字节区间算 SHA1 后重试
	if resp.Status == uploadStatusSign {
		if resp.SignKey == "" || resp.SignCheck == "" {
			return "", fmt.Errorf("115 要求二次校验但未给出 sign_key/sign_check")
		}
		off, length, perr := parseSignCheck(resp.SignCheck)
		if perr != nil {
			return "", perr
		}
		val, perr := fileSHA1Range(localPath, off, length)
		if perr != nil {
			return "", perr
		}
		req.SignKey, req.SignVal = resp.SignKey, val
		if resp, err = u.initUpload(ctx, info, req); err != nil {
			return "", err
		}
	}

	switch resp.Status {
	case uploadStatusReuse:
		if resp.PickCode == "" {
			return "", fmt.Errorf("115 返回秒传成功但缺少 pick_code")
		}
		return resp.PickCode, nil
	case uploadStatusNeedReal:
		// 继续走真实上传
	default:
		return "", &APIError{
			Op:      "initupload",
			Errno:   strconv.Itoa(resp.StatusCode),
			Message: orDefault(strings.TrimSpace(resp.StatusMsg), fmt.Sprintf("未知状态 %d", resp.Status)),
		}
	}

	if resp.Bucket == "" || resp.Object == "" {
		return "", fmt.Errorf("115 未返回上传目标（bucket/object 为空）")
	}
	if resp.Callback.Callback == "" {
		return "", fmt.Errorf("115 未返回上传回调配置")
	}

	tok, err := u.uploadToken(ctx, false)
	if err != nil {
		return "", err
	}
	u.token = tok

	// 接入点不常变，取到一次就复用，避免每次上传都多打一次接口
	if u.ossEP.APIEndpoint == "" {
		ep, err := u.endpoint(ctx)
		if err != nil {
			return "", err
		}
		u.ossEP = ep
	}

	result, err := u.ossUploadFile(ctx, resp.Bucket, resp.Object, localPath, size, resp.Callback)
	if err != nil {
		return "", err
	}
	if result.Data.PickCode == "" {
		return "", fmt.Errorf("115 回调未返回 pick_code")
	}
	if result.Data.FileSize > 0 && result.Data.FileSize != size {
		return "", fmt.Errorf("115 回报的文件大小与本地不一致（%d != %d）", result.Data.FileSize, size)
	}
	return result.Data.PickCode, nil
}

// uploadContext 拉取（并缓存）上传所需的 user_id / userkey。
func (u *webUploader) uploadContext(ctx context.Context) (uploadCtx, error) {
	u.mu.Lock()
	cached := u.info
	u.mu.Unlock()
	if cached.UserID != 0 && cached.UserKey != "" {
		return cached, nil
	}

	// uploadinfo 必须带 cookie：它靠会话识别用户（实测不带会返回「请重新登录」）
	body, err := u.apiGet(ctx, u.urlInfo)
	if err != nil {
		return uploadCtx{}, err
	}
	var out uploadInfo
	if err := json.Unmarshal(body, &out); err != nil {
		return uploadCtx{}, fmt.Errorf("解析 uploadinfo 失败: %w（前 120 字节: %q）", err, truncate(string(body), 120))
	}
	if !out.State {
		msg := orDefault(strings.TrimSpace(out.Error), "115 未返回上传信息")
		if isLoginError(msg) {
			return uploadCtx{}, ErrLoginRequired
		}
		return uploadCtx{}, &APIError{Op: "uploadinfo", Message: msg}
	}
	if !out.Allowed {
		return uploadCtx{}, &APIError{Op: "uploadinfo", Message: orDefault(out.AllowedMsg, "当前账号被限制上传")}
	}
	if out.UserID == 0 || out.UserKey == "" {
		return uploadCtx{}, fmt.Errorf("uploadinfo 缺少 user_id/userkey")
	}

	c := uploadCtx{UserID: out.UserID, UserKey: out.UserKey, SizeLimit: out.SizeLimit}
	u.mu.Lock()
	u.info = c
	u.mu.Unlock()
	return c, nil
}

// uploadInitReq 是 initupload 的业务参数。
type uploadInitReq struct {
	FileID   string
	FileName string
	FileSize int64
	Target   string
	SignKey  string
	SignVal  string
}

// initUpload 发起一次加密的 initupload 请求并解析响应。
func (u *webUploader) initUpload(ctx context.Context, info uploadCtx, in uploadInitReq) (*uploadInitResp, error) {
	ts := time.Now().Unix()
	userID := strconv.FormatInt(info.UserID, 10)
	sizeStr := strconv.FormatInt(in.FileSize, 10)

	fields := map[string]string{
		"appversion": u.appVersion,
		"target":     in.Target,
		"topupload":  "true",
		"userid":     userID,
		"userkey":    info.UserKey,
		"fileid":     in.FileID,
		"filename":   in.FileName,
		"filesize":   sizeStr,
		"sign_key":   in.SignKey,
		"sign_val":   in.SignVal,
		"t":          strconv.FormatInt(ts, 10),
		"sig":        makeUploadSig(info.UserKey, userID, in.FileID, in.Target),
		"token":      makeUploadToken(in.FileID, sizeStr, in.SignKey, in.SignVal, userID, ts, u.appVersion),
	}

	kEc, body, err := encodeUploadBody(fields, ts)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u.urlInit+"?k_ec="+kEc, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", u.ua)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "*/*")

	raw, err := u.do(ctx, req, "initupload")
	if err != nil {
		return nil, err
	}
	plain, err := decodeUploadResponse(raw)
	if err != nil {
		return nil, err
	}

	var out uploadInitResp
	if err := json.Unmarshal(plain, &out); err != nil {
		return nil, fmt.Errorf("解析 initupload 响应失败: %w（明文前 120 字节: %q）", err, truncate(string(plain), 120))
	}
	if out.Request != "upload" && out.Request != "" {
		return nil, fmt.Errorf("initupload 返回了非预期响应: %s", truncate(string(plain), 120))
	}
	return &out, nil
}

// decodeUploadResponse 解码 115 的加密响应：AES-CBC → 去填充 → LZ4 分块 → JSON。
func decodeUploadResponse(raw []byte) ([]byte, error) {
	if len(raw) < aesBlockSize {
		return nil, fmt.Errorf("上传响应过短（%d 字节）", len(raw))
	}
	// 响应长度可能不是 16 的整数倍（尾部有少量填充字节），按整块截取
	seg := raw[:len(raw)/aesBlockSize*aesBlockSize]

	dec, err := aesCBCDecrypt(seg)
	if err != nil {
		return nil, err
	}
	if len(dec) == 0 {
		return nil, fmt.Errorf("上传响应解密后为空")
	}
	// 部分响应直接是明文 JSON
	if dec[0] == '{' || dec[0] == '[' {
		return dec, nil
	}
	out, err := lz4FramesDecompress(dec)
	if err != nil {
		return nil, fmt.Errorf("解压上传响应失败: %w", err)
	}
	return out, nil
}

// uploadToken 获取（并可强制刷新）OSS STS 凭据。
func (u *webUploader) uploadToken(ctx context.Context, refresh bool) (ossToken, error) {
	u.mu.Lock()
	cached := u.token
	u.mu.Unlock()
	if !refresh && cached.valid() {
		return cached, nil
	}

	body, err := u.apiGet(ctx, u.urlToken)
	if err != nil {
		return ossToken{}, err
	}
	var tok ossToken
	if err := json.Unmarshal(body, &tok); err != nil {
		return ossToken{}, fmt.Errorf("解析上传凭据失败: %w", err)
	}
	if tok.AccessKeyID == "" || tok.SecurityToken == "" || tok.AccessKeySecret == "" {
		return ossToken{}, fmt.Errorf("上传凭据不完整（StatusCode=%s）", tok.StatusCode)
	}
	tok.fetchedAt = time.Now()

	u.mu.Lock()
	u.token = tok
	u.mu.Unlock()
	return tok, nil
}

// endpoint 获取 OSS 上传接入点。
func (u *webUploader) endpoint(ctx context.Context) (ossEndpoint, error) {
	body, err := u.apiGet(ctx, u.urlEndpoint)
	if err != nil {
		return ossEndpoint{}, err
	}
	var out struct {
		Endpoint string `json:"endpoint"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return ossEndpoint{}, fmt.Errorf("解析上传接入点失败: %w", err)
	}
	if out.Endpoint == "" {
		// 兜底：115 长期使用深圳节点
		return ossEndpoint{APIEndpoint: "oss-cn-shenzhen.aliyuncs.com"}, nil
	}
	return ossEndpoint{APIEndpoint: out.Endpoint}, nil
}

// apiGet 发起上传相关的 GET 请求（带当前会话 cookie）。
//
// TLS 握手在跨境链路上可能被长时间拖住，因此做有限次重试。
func (u *webUploader) apiGet(ctx context.Context, rawURL string) ([]byte, error) {
	var body []byte
	err := retry(ctx, uploadAPIAttempts, 800*time.Millisecond, func() error {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
		if err != nil {
			return err
		}
		req.Header.Set("Cookie", u.client.cookie)
		req.Header.Set("User-Agent", u.ua)
		req.Header.Set("Referer", "https://115.com/")

		b, err := u.do(ctx, req, "请求 115")
		if err != nil {
			return err
		}
		body = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return body, nil
}

// do 执行请求并返回响应体，做基础错误归一化。
func (u *webUploader) do(ctx context.Context, req *http.Request, op string) ([]byte, error) {
	resp, err := u.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s失败: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("读取%s响应失败: %w", op, err)
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, ErrLoginRequired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%s返回状态码 %d", op, resp.StatusCode)
	}
	return body, nil
}

// retry 最多执行 attempts 次 fn，失败按 base 递增退避后重试。
//
// 只用于「网络层抖动」这类值得重来的错误；业务层拒绝应立即返回。
// ctx 取消时立刻结束，不再等待退避。
func retry(ctx context.Context, attempts int, base time.Duration, fn func() error) error {
	if attempts < 1 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if lastErr = fn(); lastErr == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(time.Duration(i+1) * base):
		}
	}
	return lastErr
}
