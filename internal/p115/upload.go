package p115

import (
	"context"
	"crypto/sha1"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// ErrUploaderNotConfigured 未配置任何可用的上传凭据。
var ErrUploaderNotConfigured = errors.New("未配置 115 登录凭据，无法执行转存")

// Uploader 抽象「把本地文件放入 115 指定目录」的能力。
type Uploader interface {
	Name() string
	Available() bool
	Upload(ctx context.Context, localPath, fileName string, size int64, targetCID string) (pickCode string, err error)
}

// NewUploader 依据当前登录态构建上传通道。
//
// 唯一的凭据来源是 115 登录会话（cookie + UA），因此扫码登录后即可直接使用，
// 不需要申请任何第三方 token。网页通道同时支持秒传与真实上传（含 OSS 分片）。
//
// channelID 指定客户端身份（见 channel.go 的档位表）；未知或空值会退回默认通道，
// 宁可用已验证过的组合，也不要拿空 UA 去握手。
func NewUploader(c *Client, channelID string) Uploader {
	if c == nil || !c.Ready() {
		return unavailableUploader{}
	}
	ch, ok := LookupUploadChannel(channelID)
	if !ok {
		ch = DefaultUploadChannel()
	}
	return newWebUploaderWithChannel(c, ch)
}

// SetUploadPartSize 调整网页通道的分片大小。
//
// 正常运行时用默认 10MB；调试/低带宽环境可以调小，便于快速验证分片链路。
func SetUploadPartSize(u Uploader, size int64) {
	if w, ok := u.(*webUploader); ok && size > 0 {
		w.partSize = size
	}
}

// unavailableUploader 是未登录 115 时的占位实现：所有能力如实报告为不可用，
// 避免上层误以为已经具备转存能力。
type unavailableUploader struct{}

func (unavailableUploader) Name() string    { return "未配置" }
func (unavailableUploader) Available() bool { return false }
func (unavailableUploader) Upload(context.Context, string, string, int64, string) (string, error) {
	return "", ErrUploaderNotConfigured
}

// parseSignCheck 解析形如 "0-131071" 的区间串为起始偏移与长度。
func parseSignCheck(s string) (offset int64, length int64, err error) {
	parts := strings.Split(strings.TrimSpace(s), "-")
	if len(parts) != 2 {
		return 0, 0, fmt.Errorf("无法解析 sign_check: %q", s)
	}
	start, err := strconv.ParseInt(strings.TrimSpace(parts[0]), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("sign_check 起始位置非法: %q", parts[0])
	}
	end, err := strconv.ParseInt(strings.TrimSpace(parts[1]), 10, 64)
	if err != nil {
		return 0, 0, fmt.Errorf("sign_check 结束位置非法: %q", parts[1])
	}
	if end < start {
		return 0, 0, fmt.Errorf("sign_check 区间非法: %q", s)
	}
	return start, end - start + 1, nil
}

// fileSHA1Range 计算文件 [offset, offset+length) 区间的 SHA1（大写）。
// 115 的 sign_val 要求大写十六进制。
func fileSHA1Range(path string, offset, length int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return "", fmt.Errorf("定位文件区间失败: %w", err)
	}
	h := sha1.New()
	if _, err := io.CopyN(h, f, length); err != nil && !errors.Is(err, io.EOF) {
		return "", fmt.Errorf("读取文件区间失败: %w", err)
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}

// normalizeTarget 上传请求的 target 形如 U_1_<目录 id>。
func normalizeTarget(cid string) string {
	cid = strings.TrimSpace(cid)
	if strings.HasPrefix(cid, "U_1_") {
		return cid
	}
	if cid == "" {
		return "U_1_0"
	}
	return "U_1_" + cid
}

// FileSHA1 计算整个文件的 SHA1（大写十六进制），用作 115 的 fileid。
func FileSHA1(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("打开文件失败: %w", err)
	}
	defer func() { _ = f.Close() }()

	h := sha1.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("计算文件 SHA1 失败: %w", err)
	}
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil))), nil
}
