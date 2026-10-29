package p115

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"sort"
	"strings"
	"time"
)

// ossBucketResource 是 OSS V1 签名中的 CanonicalizedResource。
// 形如 /<bucket>/<object>，有子资源时追加 ?key=value（按 key 字典序）。
type ossResource struct {
	Bucket      string
	Object      string
	Subresource map[string]string
}

func (r ossResource) String() string {
	s := "/" + r.Bucket + "/" + orDefault(r.Object, "")
	if len(r.Subresource) == 0 {
		return s
	}
	keys := make([]string, 0, len(r.Subresource))
	for k := range r.Subresource {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		if v := r.Subresource[k]; v != "" {
			parts = append(parts, k+"="+v)
		} else {
			parts = append(parts, k)
		}
	}
	return s + "?" + strings.Join(parts, "&")
}

// ossSign 生成 OSS V1 签名头（Authorization + Date + x-oss-security-token）。
//
// 签名串格式（阿里云 OSS V1，实测校验通过）：
//
//	METHOD\n
//	Content-MD5\n
//	Content-Type\n
//	Date\n
//	CanonicalizedOSSHeaders        // 每个 x-oss-* 头一行，含尾随换行
//	CanonicalizedResource
func ossSign(token ossToken, method string, res ossResource, headers map[string]string) string {
	lower := make(map[string]string, len(headers)+2)
	for k, v := range headers {
		lower[strings.ToLower(k)] = v
	}
	lower["x-oss-security-token"] = token.SecurityToken

	date := lower["date"]
	if d := lower["x-oss-date"]; d != "" {
		date = d
	}
	if date == "" {
		date = time.Now().UTC().Format(http.TimeFormat)
		lower["date"] = date
	}

	// 收集所有 x-oss-* 头，按 key 排序，每行以 \n 结尾
	ossKeys := make([]string, 0, 4)
	for k := range lower {
		if strings.HasPrefix(k, "x-oss-") {
			ossKeys = append(ossKeys, k)
		}
	}
	sort.Strings(ossKeys)
	var canon strings.Builder
	for _, k := range ossKeys {
		canon.WriteString(k)
		canon.WriteByte(':')
		canon.WriteString(lower[k])
		canon.WriteByte('\n')
	}

	stringToSign := strings.Join([]string{
		strings.ToUpper(method),
		lower["content-md5"],
		lower["content-type"],
		date,
		canon.String() + res.String(),
	}, "\n")

	mac := hmac.New(sha1.New, []byte(token.AccessKeySecret))
	mac.Write([]byte(stringToSign))
	signature := base64.StdEncoding.EncodeToString(mac.Sum(nil))
	return "OSS " + token.AccessKeyID + ":" + signature
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

// ossEndpoint 是 OSS 上传参数的抽象，便于测试注入。
type ossEndpoint struct {
	// APIEndpoint 形如 oss-cn-shenzhen.aliyuncs.com（不含 bucket 前缀）
	APIEndpoint string
	// hostOverride 仅供测试：直接指定 Host，跳过 bucket 前缀拼接
	hostOverride string
}

// bucketHost 返回 virtual-host 风格的访问域名。
//
// 必须把 bucket 放进域名：若用 <endpoint>/<bucket>/<object> 的 path-style，
// OSS 计算 CanonicalizedResource 会得到 /<object>，与签名用的 /<bucket>/<object>
// 不一致，直接 SignatureDoesNotMatch。
func (e ossEndpoint) bucketHost(bucket string) string {
	if e.hostOverride != "" {
		return e.hostOverride
	}
	ep := strings.TrimPrefix(strings.TrimPrefix(e.APIEndpoint, "https://"), "http://")
	if strings.HasPrefix(ep, bucket+".") {
		return ep
	}
	return bucket + "." + ep
}

func (e ossEndpoint) scheme() string {
	if strings.HasPrefix(e.APIEndpoint, "http://") {
		return "http"
	}
	return "https"
}

func (e ossEndpoint) objectURL(bucket, object string) string {
	return fmt.Sprintf("%s://%s/%s", e.scheme(), e.bucketHost(bucket), escapeObjectPath(object))
}

// escapeObjectPath 只转义路径中必要的字符，保留 "/"。
func escapeObjectPath(object string) string {
	segs := strings.Split(object, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return strings.Join(segs, "/")
}

// ossPutObject 单次 PUT 上传整个文件。
// 适用于中小文件；大文件走分片，避免一次超时前功尽弃。
//
// Content-Type 留空：115 的做法就是不带该头（空串参与签名），
// 保持「不设置」可避免 Go 自动补默认值导致签名与发送不一致。
func (u *webUploader) ossPutObject(ctx context.Context, bucket, object string, r io.Reader, size int64, callback ossCallback) ([]byte, error) {
	res := ossResource{Bucket: bucket, Object: object}
	headers := map[string]string{
		"Content-Type":       "",
		"x-oss-callback":     base64.StdEncoding.EncodeToString([]byte(callback.Callback)),
		"x-oss-callback-var": base64.StdEncoding.EncodeToString([]byte(callback.CallbackVar)),
	}
	auth := ossSign(u.token, http.MethodPut, res, headers)

	req, err := http.NewRequestWithContext(ctx, http.MethodPut, u.ossEP.objectURL(bucket, object), r)
	if err != nil {
		return nil, fmt.Errorf("构造 OSS 请求失败: %w", err)
	}
	req.ContentLength = size
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("x-oss-security-token", u.token.SecurityToken)
	req.Header.Set("Authorization", auth)

	return u.doOSS(req, "OSS 上传")
}

// doOSS 执行 OSS 请求并返回响应体。
func (u *webUploader) doOSS(req *http.Request, op string) ([]byte, error) {
	resp, err := u.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s请求失败: %w", op, err)
	}
	defer func() { _ = resp.Body.Close() }()

	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("读取%s响应失败: %w", op, err)
	}
	if resp.StatusCode != http.StatusOK {
		return body, &APIError{Op: "oss", Errno: fmt.Sprintf("HTTP%d", resp.StatusCode), Message: strings.TrimSpace(extractOSSError(body))}
	}
	return body, nil
}

// extractOSSError 从 OSS 的 XML 错误响应里提取可读信息。
func extractOSSError(body []byte) string {
	var e struct {
		Code    string `xml:"Code"`
		Message string `xml:"Message"`
	}
	if err := xml.Unmarshal(body, &e); err == nil && (e.Code != "" || e.Message != "") {
		return e.Code + " " + e.Message
	}
	s := strings.TrimSpace(string(body))
	if len(s) > 200 {
		s = s[:200]
	}
	return s
}

// ossCallback 是 115 在 init 响应里给出的回调配置。
type ossCallback struct {
	Callback    string `json:"callback"`
	CallbackVar string `json:"callback_var"`
}

// ossInitResult 是 completeupload 回调的业务结果。
type ossInitResult struct {
	State   bool   `json:"state"`
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    struct {
		PickCode string `json:"pick_code"`
		FileSize int64  `json:"file_size"`
		FileID   string `json:"file_id"`
		Sha1     string `json:"sha1"`
		FileName string `json:"file_name"`
		CID      string `json:"cid"`
		IsVideo  int    `json:"is_video"`
	} `json:"data"`
}

// multipartPartSize 是分片大小默认值。10MB 在重试成本与请求数之间比较平衡，
// 同时保证单次请求的内存占用可控（本项目对内存敏感）。
const multipartPartSize = 10 << 20

// ossSinglePutLimit 是 OSS 单次 PUT 的上限（5GB）。
//
// 为什么默认走单 PUT 而不分片：实测分片能上传成功、也能合并，但 115 的
// completeupload.php 会返回 errno=10002「校验文件失败」。原因是 OSS 回调模板
// 里的 ${sha1} 在分片模式拿不到整体 SHA1（回调 body 由 115 固定给出，无法改）。
// 单 PUT 模式下 115 能正常校验并入库，因此 5GB 以内一律走单 PUT。
//
// 超过该上限时仍会尝试分片，并在失败时给出明确提示，而不是静默回退。
const ossSinglePutLimit = int64(5) << 30

// maxMultipartParts 是 OSS 允许的分片数上限。
const maxMultipartParts = 10000

// maxPartSize 是 OSS 单片大小上限。
const maxPartSize = int64(5) << 30

// ossUploadFile 按大小自动选择单请求或分片上传。
func (u *webUploader) ossUploadFile(ctx context.Context, bucket, object, localPath string, size int64, cb ossCallback) (*ossInitResult, error) {
	if size <= ossSinglePutLimit {
		return u.putWithRetry(ctx, bucket, object, localPath, size, cb)
	}
	return u.ossMultipartUpload(ctx, bucket, object, localPath, size, cb)
}

// parseOSSCallback 解析回调响应。
func parseOSSCallback(body []byte) (*ossInitResult, error) {
	var out ossInitResult
	if err := json.Unmarshal(body, &out); err != nil {
		return nil, fmt.Errorf("解析 OSS 回调结果失败: %w（原始响应前 120 字节: %q）", err, truncate(string(body), 120))
	}
	if !out.State {
		msg := out.Message
		if msg == "" {
			msg = "115 未接受该文件"
		}
		return nil, &APIError{Op: "completeupload", Errno: fmt.Sprintf("%d", out.Code), Message: msg}
	}
	return &out, nil
}

// putWithRetry 单 PUT 上传，失败自动重试。
//
// 跨境链路实测波动极大（同一个 3MB 文件耗时从 6s 到 90s），中途断流很常见。
// 单 PUT 重试需要把文件句柄重新定位到开头，代价远小于让用户重来。
func (u *webUploader) putWithRetry(ctx context.Context, bucket, object, localPath string, size int64, cb ossCallback) (*ossInitResult, error) {
	var body []byte
	err := retry(ctx, uploadAPIAttempts, 2*time.Second, func() error {
		f, err := os.Open(localPath)
		if err != nil {
			return fmt.Errorf("打开文件失败: %w", err)
		}
		defer func() { _ = f.Close() }()

		b, err := u.ossPutObject(ctx, bucket, object, f, size, cb)
		if err != nil {
			return err
		}
		body = b
		return nil
	})
	if err != nil {
		return nil, err
	}
	return parseOSSCallback(body)
}

// ossMultipartUpload 分片上传大文件。
//
// 流程：InitiateMultipartUpload → 逐片 UploadPart（失败重试）→ Complete。
// 分片上传的额外好处是单片失败只需重传该片，不必整文件重来。
func (u *webUploader) ossMultipartUpload(ctx context.Context, bucket, object, localPath string, size int64, cb ossCallback) (*ossInitResult, error) {
	uploadID, err := u.ossInitMultipart(ctx, bucket, object)
	if err != nil {
		return nil, err
	}

	f, err := os.Open(localPath)
	if err != nil {
		return nil, fmt.Errorf("打开文件失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	partSize := u.partSize
	if partSize <= 0 {
		partSize = calculatePartSize(size)
	}
	var parts []ossPart
	buf := make([]byte, partSize)

	for num := 1; ; num++ {
		off := int64(num-1) * partSize
		if off >= size {
			break
		}
		want := partSize
		if remain := size - off; remain < want {
			want = remain
		}

		// 每片只读入固定缓冲区，内存占用与文件大小无关
		chunk := buf[:want]
		if _, err := f.ReadAt(chunk, off); err != nil && err != io.EOF {
			return nil, fmt.Errorf("读取第 %d 片失败: %w", num, err)
		}

		etag, err := u.ossUploadPartWithRetry(ctx, bucket, object, uploadID, num, chunk)
		if err != nil {
			u.ossAbortMultipart(context.WithoutCancel(ctx), bucket, object, uploadID)
			return nil, fmt.Errorf("上传第 %d 片失败: %w", num, err)
		}
		parts = append(parts, ossPart{Number: num, ETag: etag})
	}

	return u.ossCompleteMultipart(ctx, bucket, object, uploadID, parts2xml(parts), cb)
}

// calculatePartSize 按文件大小选分片大小，避免分片数超过 OSS 上限。
func calculatePartSize(size int64) int64 {
	part := int64(multipartPartSize)
	if need := (size + maxMultipartParts - 1) / maxMultipartParts; need > part {
		part = need
	}
	if part > maxPartSize {
		part = maxPartSize
	}
	return part
}

// ossPart 是一片已上传分片的信息。
type ossPart struct {
	Number int
	ETag   string
}

// parts2xml 把分片列表拼成 CompleteMultipartUpload 的 XML 体。
func parts2xml(parts []ossPart) string {
	var sb strings.Builder
	sb.WriteString("<CompleteMultipartUpload>")
	for _, p := range parts {
		sb.WriteString(fmt.Sprintf("<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>", p.Number, p.ETag))
	}
	sb.WriteString("</CompleteMultipartUpload>")
	return sb.String()
}

// ossInitMultipart 初始化分片上传，返回 uploadId。
func (u *webUploader) ossInitMultipart(ctx context.Context, bucket, object string) (string, error) {
	res := ossResource{Bucket: bucket, Object: object, Subresource: map[string]string{"uploads": ""}}
	auth := ossSign(u.token, http.MethodPost, res, map[string]string{})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		u.ossEP.objectURL(bucket, object)+"?uploads", bytes.NewReader(nil))
	if err != nil {
		return "", err
	}
	req.ContentLength = 0
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("x-oss-security-token", u.token.SecurityToken)
	req.Header.Set("Authorization", auth)

	body, err := u.doOSS(req, "初始化分片上传")
	if err != nil {
		return "", err
	}
	var out struct {
		UploadID string `xml:"UploadId"`
	}
	if err := xml.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("解析 uploadId 失败: %w", err)
	}
	if out.UploadID == "" {
		return "", fmt.Errorf("OSS 未返回 uploadId")
	}
	return out.UploadID, nil
}

// ossUploadPartWithRetry 单片失败自动重试。
//
// 跨境链路上偶发超时/重置很常见，单片重传的代价远小于整文件重来。
func (u *webUploader) ossUploadPartWithRetry(ctx context.Context, bucket, object, uploadID string, num int, data []byte) (string, error) {
	var etag string
	err := retry(ctx, uploadAPIAttempts, time.Second, func() error {
		e, err := u.ossUploadPart(ctx, bucket, object, uploadID, num, data)
		if err != nil {
			return err
		}
		etag = e
		return nil
	})
	if err != nil {
		return "", err
	}
	return etag, nil
}

// ossUploadPart 上传单片，返回 ETag。
func (u *webUploader) ossUploadPart(ctx context.Context, bucket, object, uploadID string, num int, data []byte) (string, error) {
	sub := map[string]string{
		"partNumber": fmt.Sprintf("%d", num),
		"uploadId":   uploadID,
	}
	res := ossResource{Bucket: bucket, Object: object, Subresource: sub}
	auth := ossSign(u.token, http.MethodPut, res, map[string]string{})

	rawURL := fmt.Sprintf("%s?partNumber=%d&uploadId=%s", u.ossEP.objectURL(bucket, object), num, url.QueryEscape(uploadID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, rawURL, bytes.NewReader(data))
	if err != nil {
		return "", err
	}
	req.ContentLength = int64(len(data))
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("x-oss-security-token", u.token.SecurityToken)
	req.Header.Set("Authorization", auth)

	resp, err := u.hc.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<18))
	if resp.StatusCode != http.StatusOK {
		return "", &APIError{Op: "oss/part", Errno: fmt.Sprintf("HTTP%d", resp.StatusCode), Message: extractOSSError(body)}
	}
	etag := resp.Header.Get("ETag")
	if etag == "" {
		return "", fmt.Errorf("OSS 未返回 ETag")
	}
	return etag, nil
}

// ossCompleteMultipart 合并分片并带上回调。
func (u *webUploader) ossCompleteMultipart(ctx context.Context, bucket, object, uploadID, bodyXML string, cb ossCallback) (*ossInitResult, error) {
	sub := map[string]string{"uploadId": uploadID}
	res := ossResource{Bucket: bucket, Object: object, Subresource: sub}
	headers := map[string]string{
		"Content-Type":       "application/xml",
		"x-oss-callback":     base64.StdEncoding.EncodeToString([]byte(cb.Callback)),
		"x-oss-callback-var": base64.StdEncoding.EncodeToString([]byte(cb.CallbackVar)),
	}
	auth := ossSign(u.token, http.MethodPost, res, headers)

	rawURL := fmt.Sprintf("%s?uploadId=%s", u.ossEP.objectURL(bucket, object), url.QueryEscape(uploadID))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, rawURL, strings.NewReader(bodyXML))
	if err != nil {
		return nil, err
	}
	req.ContentLength = int64(len(bodyXML))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("x-oss-security-token", u.token.SecurityToken)
	req.Header.Set("Authorization", auth)

	body, err := u.doOSS(req, "合并分片")
	if err != nil {
		return nil, err
	}
	return parseOSSCallback(body)
}

// ossAbortMultipart 放弃分片上传，避免 OSS 上残留未完成的碎片。
func (u *webUploader) ossAbortMultipart(ctx context.Context, bucket, object, uploadID string) {
	sub := map[string]string{"uploadId": uploadID}
	res := ossResource{Bucket: bucket, Object: object, Subresource: sub}
	auth := ossSign(u.token, http.MethodDelete, res, map[string]string{})

	rawURL := fmt.Sprintf("%s?uploadId=%s", u.ossEP.objectURL(bucket, object), url.QueryEscape(uploadID))
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, rawURL, nil)
	if err != nil {
		return
	}
	req.Header.Set("Date", time.Now().UTC().Format(http.TimeFormat))
	req.Header.Set("x-oss-security-token", u.token.SecurityToken)
	req.Header.Set("Authorization", auth)

	resp, err := u.hc.Do(req)
	if err == nil {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 1<<16))
		_ = resp.Body.Close()
	}
}
