package p115

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// 真实响应向量：从 115 的 4.0/initupload.php 直接抓取（status=1，需真实上传）。
// 用于验证「AES-CBC 解密 → 去填充 → LZ4 分块解压」这条链路对真实数据有效。
const realInitRespHex = "c97293b17b6ac74897bb0042a85c153280e8ab19030d8275713084366d2d50cd" +
	"47be6c486e21623546a0d01945cbc8cb947e305f5b3818f5fda3291746823867eb3f5e2850515e08a" +
	"359809178f6185e4e64f1c8e7431fd57bf4f1f3417c64b8adab06407f7bfb049f86d9cc1118426" +
	"37c8d3239f2a68cb87737c54bda8b0aa493e4f9c44b8220feff25c22939112850e45c0683233a10" +
	"f2081eeac570e9a438aafbeb014ab7ca49b5fc6024174d4861b259a649613ec80e15df2113fd262" +
	"fc728b92d9278b78f853aea6b433117b2e4df462b7c121b98fd8d086ac24c67263a0ce8ada540f0" +
	"e91bc3a5a04dc678fd8a6caf02785173d9f58e6f47dd360733a8b54f95b4e2f73de1d9821be84b3" +
	"0ed520c72d0774debb8217e807632f6d78deb54c7490314fa1afcbb2ac2b13a94280c4db5cd3ed6" +
	"97e989c9b154161ddbc3c9e0d6f3b746ea04dcb178fb6a87e39dee31a3dce0a74d9e0f584a9cd18" +
	"8ca826fab4bc26ed5d031ea9a0ba7fcc73b0c33de9cec49f59cee72880f3b48d0a8da9705677758" +
	"d13021f7c3162981db306bd5b8046fac18b6c52841b4f4cb31caf01a5d9df495ff5f5a5d5c2a620" +
	"927cd3f036ef2f3f963f379413ad3a7c1081c6f447f627dee47f7bf69d49d0d703146928c573d6d" +
	"c1e45ad31b154e973ffc22aaf9fe2c1b939ebcd8bdbd7ac66816e5c77b5a56466a32f20f05c0f87" +
	"673848d34b320e2e0e0010100e05162da88"

func TestDecodeUploadResponseRealVector(t *testing.T) {
	raw, err := hex.DecodeString(realInitRespHex)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := decodeUploadResponse(raw)
	if err != nil {
		t.Fatalf("解码真实响应失败: %v", err)
	}

	var out uploadInitResp
	if err := json.Unmarshal(plain, &out); err != nil {
		t.Fatalf("解析为 uploadInitResp 失败: %v（明文前 100 字节: %q）", err, truncate(string(plain), 100))
	}
	if out.Request != "upload" {
		t.Fatalf("request 字段不符: %q", out.Request)
	}
	if out.Status != uploadStatusNeedReal {
		t.Fatalf("status 期望 %d（需真实上传），实际 %d", uploadStatusNeedReal, out.Status)
	}
	if out.Bucket == "" || out.Object == "" {
		t.Fatalf("未解析出 bucket/object: %+v", out)
	}
	if out.Callback.Callback == "" || out.Callback.CallbackVar == "" {
		t.Fatalf("未解析出 callback 配置")
	}
}

func TestDecodeUploadResponsePlainJSON(t *testing.T) {
	// 少数错误响应直接是明文 JSON，解码链要能兼容
	body := []byte(`{"request":"upload","status":4,"statusmsg":"bad"}`)
	enc, err := aesCBCEncrypt(body)
	if err != nil {
		t.Fatal(err)
	}
	out, err := decodeUploadResponse(enc)
	if err != nil {
		t.Fatalf("明文 JSON 分支失败: %v", err)
	}
	if string(out) != string(body) {
		t.Fatalf("明文不一致: %q", out)
	}
}

// uploadMock 模拟 115 的上传相关接口。
type uploadMock struct {
	t *testing.T

	initStatuses []uploadInitResp // 按顺序返回；用完后返回最后一个
	token        ossToken

	ossURL string // 供 getuploadinfo 返回，指向测试用的 OSS 服务

	initCalls int
	mu        sync.Mutex
	lastForm  map[string]string
	lastKEc   string
}

func (m *uploadMock) handler() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("/uploadinfo", func(w http.ResponseWriter, r *http.Request) {
		// ⚠️ 字段平铺在顶层，无 data 包装
		_, _ = w.Write([]byte(`{"state":true,"user_id":342204514,` +
			`"userkey":"CD3C42E2513DD9D8169EE11E403301AAE42BE291",` +
			`"size_limit":123480309760,"upload_allowed":true}`))
	})

	mux.HandleFunc("/initupload", func(w http.ResponseWriter, r *http.Request) {
		m.mu.Lock()
		m.lastKEc = r.URL.Query().Get("k_ec")
		idx := m.initCalls
		if idx >= len(m.initStatuses) {
			idx = len(m.initStatuses) - 1
		}
		resp := m.initStatuses[idx]
		m.initCalls++
		m.mu.Unlock()

		// 校验请求体确实被加密，并解出表单
		raw, _ := io.ReadAll(r.Body)
		// 请求体是纯 AES-CBC 密文（不是 LZ4 压缩的响应格式）
		plain, err := aesCBCDecrypt(raw[:len(raw)/aesBlockSize*aesBlockSize])
		if err != nil {
			m.t.Errorf("init 请求体解密失败: %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if strings.Contains(string(plain), " ") || !strings.Contains(string(plain), "sig=") {
			m.t.Errorf("init 请求体不是合法 form: %q", truncate(string(plain), 200))
		}
		form := map[string]string{}
		for _, kv := range strings.Split(string(plain), "&") {
			k, v, _ := strings.Cut(kv, "=")
			form[k] = v
		}
		m.mu.Lock()
		m.lastForm = form
		m.mu.Unlock()

		body, _ := json.Marshal(resp)
		enc, err := aesCBCEncrypt(body)
		if err != nil {
			m.t.Errorf("加密响应失败: %v", err)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(enc)
	})

	mux.HandleFunc("/gettoken", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(fmt.Sprintf(`{"StatusCode":"200","AccessKeyId":%q,"AccessKeySecret":%q,"SecurityToken":%q}`,
			m.token.AccessKeyID, m.token.AccessKeySecret, m.token.SecurityToken)))
	})

	mux.HandleFunc("/getuploadinfo", func(w http.ResponseWriter, r *http.Request) {
		// 测试里用 http，因此 endpoint 带 scheme
		_, _ = w.Write([]byte(`{"endpoint":"http://` + strings.TrimPrefix(m.ossURL, "http://") + `"}`))
	})

	return mux
}

// ossRecorder 记录 OSS 请求，模拟 115 的 completeupload 回调。
type ossRecorder struct {
	mu       sync.Mutex
	parts    int
	complete bool
	uploaded int64
	authSeen bool
	cbSeen   bool
}

func (o *ossRecorder) server(t *testing.T, filesize int64, pickCode string) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		o.mu.Lock()
		defer o.mu.Unlock()

		if r.Header.Get("Authorization") == "" {
			t.Errorf("OSS 请求缺少 Authorization")
		}
		if r.Header.Get("x-oss-security-token") == "" {
			t.Errorf("OSS 请求缺少 x-oss-security-token")
		}
		o.authSeen = true

		q := r.URL.Query()
		switch {
		case r.Method == http.MethodPost && q.Has("uploads"):
			_, _ = w.Write([]byte(`<InitiateMultipartUploadResult><UploadId>UPLOAD-1</UploadId></InitiateMultipartUploadResult>`))
		case r.Method == http.MethodPut && q.Has("partNumber"):
			n, _ := io.Copy(io.Discard, r.Body)
			o.parts++
			o.uploaded += n
			w.Header().Set("ETag", `"`+q.Get("partNumber")+`"`)
			w.WriteHeader(http.StatusOK)
		case r.Method == http.MethodPost && q.Has("uploadId"):
			if r.Header.Get("x-oss-callback") != "" {
				o.cbSeen = true
			}
			o.complete = true
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"state":true,"code":0,"message":"","data":{"pick_code":%q,"file_size":%d,"file_id":"1","sha1":"X","file_name":"f","cid":"2"}}`,
				pickCode, filesize)))
		case r.Method == http.MethodPut:
			n, _ := io.Copy(io.Discard, r.Body)
			o.parts = 1
			o.uploaded = n
			if r.Header.Get("x-oss-callback") != "" {
				o.cbSeen = true
			}
			o.complete = true
			_, _ = w.Write([]byte(fmt.Sprintf(
				`{"state":true,"code":0,"message":"","data":{"pick_code":%q,"file_size":%d,"file_id":"1","sha1":"X","file_name":"f","cid":"2"}}`,
				pickCode, filesize)))
		default:
			t.Errorf("意外的 OSS 请求: %s %s", r.Method, r.URL.String())
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
}

func writeTempFile(t *testing.T, name string, size int) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, name)
	buf := make([]byte, size)
	for i := range buf {
		buf[i] = byte('a' + i%26)
	}
	if err := os.WriteFile(p, buf, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func newTestWebUploader(t *testing.T, apiSrv *httptest.Server, ossSrv *httptest.Server) *webUploader {
	c := New("UID=1_1;CID=1;SEID=2", DefaultUA, 10*time.Second)
	u := newWebUploader(c)
	u.urlInfo = apiSrv.URL + "/uploadinfo"
	u.urlInit = apiSrv.URL + "/initupload"
	u.urlToken = apiSrv.URL + "/gettoken"
	u.urlEndpoint = apiSrv.URL + "/getuploadinfo"
	return u
}

// TestUploadReuse 秒传路径：status=2 直接返回 pick_code，不发 OSS 请求。
func TestUploadReuse(t *testing.T) {
	local := writeTempFile(t, "a.mp4", 1024)
	tok := ossToken{AccessKeyID: "AK", AccessKeySecret: "SK", SecurityToken: "ST"}

	m := &uploadMock{t: t, token: tok,
		initStatuses: []uploadInitResp{{Request: "upload", Status: uploadStatusReuse, PickCode: "PCSEC"}},
	}
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.handler().ServeHTTP(w, r)
	}))
	defer apiSrv.Close()

	u := newTestWebUploader(t, apiSrv, nil)
	pick, err := u.Upload(context.Background(), local, "a.mp4", 1024, "123")
	if err != nil {
		t.Fatalf("秒传应成功: %v", err)
	}
	if pick != "PCSEC" {
		t.Fatalf("pick_code 期望 PCSEC，实际 %q", pick)
	}
	if m.initCalls != 1 {
		t.Fatalf("秒传应只调用一次 init，实际 %d 次", m.initCalls)
	}
}

// TestUploadSignCheckRound 二次校验：status=7 → 计算区间 SHA1 → 重试 → status=2。
func TestUploadSignCheckRound(t *testing.T) {
	local := writeTempFile(t, "b.mp4", 2048)
	tok := ossToken{AccessKeyID: "AK", AccessKeySecret: "SK", SecurityToken: "ST"}

	m := &uploadMock{t: t, token: tok, initStatuses: []uploadInitResp{
		{Request: "upload", Status: uploadStatusSign, SignKey: "K1", SignCheck: "0-1023"},
		{Request: "upload", Status: uploadStatusReuse, PickCode: "PC2"},
	}}
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.handler().ServeHTTP(w, r)
	}))
	defer apiSrv.Close()

	u := newTestWebUploader(t, apiSrv, nil)
	pick, err := u.Upload(context.Background(), local, "b.mp4", 2048, "123")
	if err != nil {
		t.Fatalf("二次校验后应成功: %v", err)
	}
	if pick != "PC2" {
		t.Fatalf("pick_code 期望 PC2，实际 %q", pick)
	}
	if m.initCalls != 2 {
		t.Fatalf("应调用 init 两次，实际 %d 次", m.initCalls)
	}
	// 第二次必须带上 sign_key / sign_val
	if m.lastForm["sign_key"] != "K1" {
		t.Fatalf("sign_key 未回传: %v", m.lastForm)
	}
	if m.lastForm["sign_val"] == "" {
		t.Fatalf("sign_val 未回传: %v", m.lastForm)
	}
	// 首次请求不应带空字段
	if _, ok := m.lastForm["path"]; ok {
		t.Fatalf("空字段 path 不应出现在表单里")
	}
}

// TestUploadRealSinglePut 真实上传（小文件走单请求 PUT）。
func TestUploadRealSinglePut(t *testing.T) {
	local := writeTempFile(t, "c.mp4", 4096)
	tok := ossToken{AccessKeyID: "AK", AccessKeySecret: "SK", SecurityToken: "ST"}

	rec := &ossRecorder{}
	ossSrv := rec.server(t, 4096, "PCREAL")
	defer ossSrv.Close()

	m := &uploadMock{t: t, token: tok, initStatuses: []uploadInitResp{{
		Request: "upload", Status: uploadStatusNeedReal, PickCode: "PCTMP",
		Bucket: "fhnfile", Object: "obj123",
		Callback: ossCallback{Callback: `{"callbackUrl":"https://x"}`, CallbackVar: `{"x:pick_code":"PCREAL"}`},
	}}}
	m.ossURL = ossSrv.URL
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.handler().ServeHTTP(w, r)
	}))
	defer apiSrv.Close()

	u := newTestWebUploader(t, apiSrv, ossSrv)
	// endpoint 指向测试服务器（http + host 覆盖）
	u.ossEP = ossEndpoint{APIEndpoint: ossSrv.URL, hostOverride: strings.TrimPrefix(ossSrv.URL, "http://")}

	pick, err := u.Upload(context.Background(), local, "c.mp4", 4096, "123")
	if err != nil {
		t.Fatalf("真实上传应成功: %v", err)
	}
	if pick != "PCREAL" {
		t.Fatalf("pick_code 应取回调结果 PCREAL，实际 %q", pick)
	}
	if rec.uploaded != 4096 {
		t.Fatalf("上传字节数不符: %d", rec.uploaded)
	}
	if !rec.cbSeen {
		t.Fatal("OSS 请求未携带 x-oss-callback")
	}
}

// TestUploadRealMultipart 分片上传（文件大于 2 个分片）。
func TestUploadRealMultipart(t *testing.T) {
	const size = 25
	local := writeTempFile(t, "d.mp4", size)
	tok := ossToken{AccessKeyID: "AK", AccessKeySecret: "SK", SecurityToken: "ST"}

	rec := &ossRecorder{}
	ossSrv := rec.server(t, size, "PCMULTI")
	defer ossSrv.Close()

	m := &uploadMock{t: t, token: tok, initStatuses: []uploadInitResp{{
		Request: "upload", Status: uploadStatusNeedReal, PickCode: "PCTMP",
		Bucket: "fhnfile", Object: "obj456",
		Callback: ossCallback{Callback: `{"callbackUrl":"https://x"}`, CallbackVar: `{"x:pick_code":"PCMULTI"}`},
	}}}
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.handler().ServeHTTP(w, r)
	}))
	defer apiSrv.Close()

	u := newTestWebUploader(t, apiSrv, ossSrv)
	u.token = tok   // 分片入口直接调用，需手动注入 STS 凭据
	u.partSize = 10 // 强制分片：25 字节 → 3 片
	u.ossEP = ossEndpoint{APIEndpoint: ossSrv.URL, hostOverride: strings.TrimPrefix(ossSrv.URL, "http://")}

	// 分片入口直接调用：默认阈值是 5GB（OSS 单 PUT 上限），
	// 线上不会因为小文件走进分片，但分片协议本身仍需保证正确。
	cb := ossCallback{Callback: `{"callbackUrl":"https://x"}`, CallbackVar: `{"x:pick_code":"PCMULTI"}`}
	res, err := u.ossMultipartUpload(context.Background(), "fhnfile", "obj456", local, size, cb)
	if err != nil {
		t.Fatalf("分片上传应成功: %v", err)
	}
	if res.Data.PickCode != "PCMULTI" {
		t.Fatalf("pick_code 期望 PCMULTI，实际 %q", res.Data.PickCode)
	}
	if rec.parts != 3 {
		t.Fatalf("期望 3 个分片，实际 %d", rec.parts)
	}
	if rec.uploaded != size {
		t.Fatalf("分片累计字节数不符: %d != %d", rec.uploaded, size)
	}
	if !rec.complete {
		t.Fatal("未调用合并分片")
	}
}

// TestUploadReportsAPIError 115 拒绝时错误信息应可读。
func TestUploadReportsAPIError(t *testing.T) {
	local := writeTempFile(t, "e.mp4", 10)
	tok := ossToken{AccessKeyID: "AK", AccessKeySecret: "SK", SecurityToken: "ST"}

	m := &uploadMock{t: t, token: tok, initStatuses: []uploadInitResp{{
		Request: "upload", Status: 4, StatusCode: 402,
		StatusMsg: "userid/filesize/target invalid",
	}}}
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.handler().ServeHTTP(w, r)
	}))
	defer apiSrv.Close()

	u := newTestWebUploader(t, apiSrv, nil)
	_, err := u.Upload(context.Background(), local, "e.mp4", 10, "123")
	if err == nil {
		t.Fatal("应返回错误")
	}
	if !strings.Contains(err.Error(), "target invalid") {
		t.Fatalf("错误信息应包含 115 的原因，实际: %v", err)
	}
}

// TestUploadRequiresLogin 未配置 cookie 时返回登录错误。
func TestUploadRequiresLogin(t *testing.T) {
	u := newWebUploader(New("", "", time.Second))
	if u.Available() {
		t.Fatal("无 cookie 时不应可用")
	}
	if _, err := u.Upload(context.Background(), "x", "x", 1, "1"); err == nil {
		t.Fatal("应返回错误")
	}
}

// TestUploadRejectsOversize 超过 115 单文件上限时应提前拒绝，避免白传。
func TestUploadRejectsOversize(t *testing.T) {
	local := writeTempFile(t, "f.mp4", 100)
	tok := ossToken{AccessKeyID: "AK", AccessKeySecret: "SK", SecurityToken: "ST"}

	m := &uploadMock{t: t, token: tok, initStatuses: []uploadInitResp{{
		Request: "upload", Status: uploadStatusReuse, PickCode: "X",
	}}}
	apiSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 返回一个很小的 size_limit
		if r.URL.Path == "/uploadinfo" {
			_, _ = w.Write([]byte(`{"state":true,"user_id":1,"userkey":"K","size_limit":50,"upload_allowed":true}`))
			return
		}
		m.handler().ServeHTTP(w, r)
	}))
	defer apiSrv.Close()

	u := newTestWebUploader(t, apiSrv, nil)
	_, err := u.Upload(context.Background(), local, "f.mp4", 100, "1")
	if err == nil || !strings.Contains(err.Error(), "上限") {
		t.Fatalf("应提示超过上限，实际: %v", err)
	}
}
