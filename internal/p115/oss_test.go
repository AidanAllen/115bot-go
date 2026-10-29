package p115

import (
	"encoding/base64"
	"testing"
)

// 向量来自 scripts/_oss_vector.json（Python hmac 计算的权威值）。
// 签名串的格式极易出错，尤其是 CanonicalizedOSSHeaders 的尾随换行，
// 少一个换行就只能在真实 OSS 上看到 SignatureDoesNotMatch，所以必须有回归测试。
const (
	vecOSSAKID   = "STS.TESTKEYID1234567890"
	vecOSSSecret = "TESTSECRETabcdefghijklmnop"
	vecOSSToken  = "CAIS-TEST-SECURITY-TOKEN"
	vecOSSDate   = "Wed, 28 Oct 2026 14:11:49 GMT"
	vecOSSBucket = "fhnfile"
	vecOSSObject = "6ae203ebd01704eda00212448f11e253fed327d0"
	vecOSSCb     = `{"callbackUrl":"https://uplb.115.com/3.0/completeupload.php","callbackBody":"bucket=${bucket}"}`
	vecOSSCbVar  = `{"x:pick_code":"6ae203ebf3446136b36392c5","x:user_id":"342204514"}`
	vecOSSSign   = "Qmor2AaW8/4tObBRwlaufoIdSYw="
)

func TestOSSSignMatchesVector(t *testing.T) {
	tok := ossToken{
		AccessKeyID:     vecOSSAKID,
		AccessKeySecret: vecOSSSecret,
		SecurityToken:   vecOSSToken,
	}
	res := ossResource{Bucket: vecOSSBucket, Object: vecOSSObject}

	headers := map[string]string{
		"Date":               vecOSSDate,
		"x-oss-callback":     base64.StdEncoding.EncodeToString([]byte(vecOSSCb)),
		"x-oss-callback-var": base64.StdEncoding.EncodeToString([]byte(vecOSSCbVar)),
	}

	got := ossSign(tok, "PUT", res, headers)
	want := "OSS " + vecOSSAKID + ":" + vecOSSSign
	if got != want {
		t.Fatalf("OSS 签名不匹配\n期望 %s\n实际 %s", want, got)
	}
}

func TestOSSSignMultipartSubresource(t *testing.T) {
	tok := ossToken{AccessKeyID: "AK", AccessKeySecret: "SK", SecurityToken: "ST"}

	cases := []struct {
		sub  map[string]string
		want string
	}{
		{map[string]string{"uploads": ""}, "/fhnfile/obj?uploads"},
		{map[string]string{"uploadId": "abc"}, "/fhnfile/obj?uploadId=abc"},
		{map[string]string{"partNumber": "3", "uploadId": "abc"}, "/fhnfile/obj?partNumber=3&uploadId=abc"},
		{nil, "/fhnfile/obj"},
	}
	for _, c := range cases {
		got := ossResource{Bucket: "fhnfile", Object: "obj", Subresource: c.sub}.String()
		if got != c.want {
			t.Fatalf("子资源拼接不符，期望 %q，实际 %q", c.want, got)
		}
		// 确保能签出非空 Authorization
		if auth := ossSign(tok, "POST", ossResource{Bucket: "fhnfile", Object: "obj", Subresource: c.sub}, nil); auth == "" {
			t.Fatal("签名为空")
		}
	}
}

func TestOSSBucketHostIsVirtualHost(t *testing.T) {
	ep := ossEndpoint{APIEndpoint: "oss-cn-shenzhen.aliyuncs.com"}
	if got := ep.bucketHost("fhnfile"); got != "fhnfile.oss-cn-shenzhen.aliyuncs.com" {
		t.Fatalf("bucket 必须在域名前缀，实际 %q", got)
	}
	// 已经带前缀时不应重复拼接
	ep2 := ossEndpoint{APIEndpoint: "fhnfile.oss-cn-shenzhen.aliyuncs.com"}
	if got := ep2.bucketHost("fhnfile"); got != "fhnfile.oss-cn-shenzhen.aliyuncs.com" {
		t.Fatalf("重复拼接了 bucket: %q", got)
	}
}

func TestParseSignCheckOK(t *testing.T) {
	// 已有测试在 upload 相关文件里覆盖，这里补充 115 上传真实出现的区间
	off, length, err := parseSignCheck("1048576-2097151")
	if err != nil || off != 1048576 || length != 1048576 {
		t.Fatalf("解析失败: off=%d len=%d err=%v", off, length, err)
	}
}
