package p115

import (
	"bytes"
	"encoding/hex"
	"testing"
)

// 以下向量由 scripts/gen_cipher_vector.py 生成，
// AES 密文由 openssl enc -aes-128-cbc 产生（权威实现），
// 用于确保 Go 实现与 115 客户端行为逐字节一致。
const (
	vecTimestamp = 1730000000
	vecKEc       = "HQMOgKF43O7OzaN33hKNAHMAAACAtB1njtndz1WuYe1G6hIaHPyBAAEAAACx5+Bc"
	vecUserkey   = "0123456789abcdef0123456789abcdef"
	vecUserID    = "342204514"
	vecFileID    = "DA39A3EE5E6B4B0D3255BFEF95601890AFD80709"
	vecTarget    = "U_1_2491055587586025989"
	vecSig       = "351C70A0C889CC1B9DC93401C69FAE4DE3DB4051"
	vecBodyToken = "004c9a843d5803bbf8334a1d11a443fa"

	// 明文：appversion=36.2.28&fileid=...&filename=%E6%B5%8B%E8%AF%95+%E8%A7%86%E9%A2%91.mp4...
	vecPlain = "appversion=36.2.28&fileid=DA39A3EE5E6B4B0D3255BFEF95601890AFD80709" +
		"&filename=%E6%B5%8B%E8%AF%95+%E8%A7%86%E9%A2%91.mp4&filesize=1024" +
		"&sig=351C70A0C889CC1B9DC93401C69FAE4DE3DB4051&t=1730000000" +
		"&target=U_1_2491055587586025989&token=004c9a843d5803bbf8334a1d11a443fa" +
		"&topupload=true&userid=342204514&userkey=0123456789abcdef0123456789abcdef"

	// openssl 对 vecPlain 加密的结果（PKCS#7 填充后 336 字节）
	vecCipherHex = "f21faffbdcc764300fa6e316f0eca561aea922c9cb08a20392f4c49b8c15868a" +
		"61fb469c2f81ea11b4ad1a73bb6e61ade4534cfff2ac98cfebf92af42fcce5be" +
		"6c7dbdfe54f3ad8a4e0d5aef2a8e85675c53e6c616a4eb1a490c2adb4e96043f" +
		"876c8b9ebc158337ae9b9429781931d9e4aa590241b88d469e8f94b11281aef7" +
		"ae7f8fb2928fea1b86c4272368d440414b173f9be30f7e7374ad612deb0ada91" +
		"e159ef46bd3256e467b1ad60c74b27cc4e0a97b84cd615615612dd40ab83bdc6" +
		"6e3af9e61cdbc09947a828f4a7b29e4797b61d2c348c0d18d20103f58648bc70" +
		"84d8efbac19bbf6137e14434a563474cb56e7bc3c1bc51450a3ea6dfe35d1e3c" +
		"11eeba4089f7f3c4ef5251bf81264ab1437f8c4ccc813b02abbfd7e4607fdd20" +
		"87c63915fcd9da46e6600a15b643cdcec71c591a4b580fb20a58786fdebf23d9" +
		"20500e995a5710cbca677a18079c2c24"
)

func TestECDHEncodeToken(t *testing.T) {
	got := ecdhEncodeToken(vecTimestamp)
	if got != vecKEc {
		t.Fatalf("k_ec 不匹配\n期望 %q\n实际 %q", vecKEc, got)
	}
}

func TestMakeUploadSig(t *testing.T) {
	got := makeUploadSig(vecUserkey, vecUserID, vecFileID, vecTarget)
	if got != vecSig {
		t.Fatalf("sig 不匹配\n期望 %q\n实际 %q", vecSig, got)
	}
}

func TestMakeUploadToken(t *testing.T) {
	got := makeUploadToken(vecFileID, "1024", "", "", vecUserID, vecTimestamp, uploadAppVersion)
	if got != vecBodyToken {
		t.Fatalf("token 不匹配\n期望 %q\n实际 %q", vecBodyToken, got)
	}
}

func TestAESMatchesOpenSSL(t *testing.T) {
	want, err := hex.DecodeString(vecCipherHex)
	if err != nil {
		t.Fatal(err)
	}

	got, err := aesCBCEncrypt([]byte(vecPlain))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("AES 密文与 openssl 不一致\n期望 %x\n实际 %x", want, got)
	}

	// 往返解密
	back, err := aesCBCDecrypt(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(back) != vecPlain {
		t.Fatalf("解密结果不一致\n期望 %q\n实际 %q", vecPlain, back)
	}
}

// TestEncodeUploadBody 确认整套编码与 Python 参考实现产出的明文一致。
func TestEncodeUploadBody(t *testing.T) {
	fields := map[string]string{
		"appversion": uploadAppVersion,
		"fileid":     vecFileID,
		"filename":   "测试 视频.mp4",
		"filesize":   "1024",
		"path":       "", // 空值应被丢弃
		"sign_key":   "",
		"sign_val":   "",
		"sig":        vecSig,
		"t":          "1730000000",
		"target":     vecTarget,
		"token":      vecBodyToken,
		"topupload":  "true",
		"userid":     vecUserID,
		"userkey":    vecUserkey,
	}
	kEc, body, err := encodeUploadBody(fields, vecTimestamp)
	if err != nil {
		t.Fatal(err)
	}
	if kEc != vecKEc {
		t.Fatalf("k_ec 不匹配: %q", kEc)
	}

	wantCipher, _ := hex.DecodeString(vecCipherHex)
	if !bytes.Equal(body, wantCipher) {
		plain, _ := aesCBCDecrypt(body)
		t.Fatalf("请求体密文不一致\n实际明文 %q\n期望明文 %q", plain, vecPlain)
	}
}

// TestPKCS7FullBlockPadding 整块对齐时必须补满一整块，否则服务端会截断最后 16 字节。
func TestPKCS7FullBlockPadding(t *testing.T) {
	data := bytes.Repeat([]byte("a"), 16)
	padded := pkcs7Pad(data, 16)
	if len(padded) != 32 {
		t.Fatalf("整块对齐应补 16 字节，实际长度 %d", len(padded))
	}
	if !bytes.Equal(pkcs7Unpad(padded, 16), data) {
		t.Fatal("反填充后数据不一致")
	}
}

func TestLZ4BlockLiteralOnly(t *testing.T) {
	// 纯字面量块：token 高位=长度（<15），低位无匹配段
	// token=0x40 → 字面量 4 字节，后接 "abcd"
	block := []byte{0x40, 'a', 'b', 'c', 'd'}
	got, err := lz4BlockDecompress(block)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcd" {
		t.Fatalf("期望 abcd，实际 %q", got)
	}
}

func TestLZ4BlockWithMatch(t *testing.T) {
	// 序列1：字面量 "abcd"（token 高 4 位 = 4），然后 offset=4、matchLen=4+4=8
	// token = 0x44（高 4 位 4 = 字面量 4，低 4 位 4 = 匹配长度 4+4）
	// 期望输出：abcd + "abcdabcd"（重叠复制）
	block := []byte{0x44, 'a', 'b', 'c', 'd', 0x04, 0x00}
	got, err := lz4BlockDecompress(block)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcdabcdabcd" {
		t.Fatalf("期望 abcdabcdabcd，实际 %q", got)
	}
}

func TestLZ4BlockExtendedLength(t *testing.T) {
	// 字面量长度 15 + 扩展 5 = 20 字节
	block := make([]byte, 0, 24)
	block = append(block, 0xF0, 0x05)
	block = append(block, bytes.Repeat([]byte{'x'}, 20)...)
	got, err := lz4BlockDecompress(block)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 20 || string(got) != string(bytes.Repeat([]byte{'x'}, 20)) {
		t.Fatalf("扩展长度解析失败，长度 %d", len(got))
	}
}

func TestLZ4FramesMultiBlock(t *testing.T) {
	// 两个分块：[len=5][0x40 abcd] + [len=5][0x40 efgh]
	var src []byte
	src = append(src, 0x05, 0x00, 0x40, 'a', 'b', 'c', 'd')
	src = append(src, 0x05, 0x00, 0x40, 'e', 'f', 'g', 'h')

	got, err := lz4FramesDecompress(src)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "abcdefgh" {
		t.Fatalf("期望 abcdefgh，实际 %q", got)
	}
}

func TestLZ4RejectsBadInput(t *testing.T) {
	// offset 超过已输出长度
	if _, err := lz4BlockDecompress([]byte{0x04, 'a', 'b', 'c', 'd', 0x09, 0x00}); err == nil {
		t.Fatal("非法偏移量应报错")
	}
	// 字面量越界
	if _, err := lz4BlockDecompress([]byte{0x80, 'a'}); err == nil {
		t.Fatal("字面量越界应报错")
	}
	// 分块声明长度超出实际
	if _, err := lz4FramesDecompress([]byte{0xFF, 0x00, 0x40}); err == nil {
		t.Fatal("分块越界应报错")
	}
}

// TestLZ4FramesToleratesTrailingBytes 解密后尾部残留填充字节时不应整体失败。
//
// 115 的响应经 AES 解密后，PKCS7 填充长度并不固定，偶尔会残留 1~2 字节。
// 早期实现把这些残留当作「长度字段不完整」直接报错，导致上传解析失败。
func TestLZ4FramesToleratesTrailingBytes(t *testing.T) {
	// 一个合法分块：[len=5][0x40 abcd] 后面跟 1 个孤字节
	src := []byte{0x05, 0x00, 0x40, 'a', 'b', 'c', 'd', 0x0c}
	got, err := lz4FramesDecompress(src)
	if err != nil {
		t.Fatalf("尾部残留不应报错: %v", err)
	}
	if string(got) != "abcd" {
		t.Fatalf("期望 abcd，实际 %q", got)
	}

	// 完全无效的输入仍应报错，避免静默返回空
	if _, err := lz4FramesDecompress([]byte{0xFF, 0xFF}); err == nil {
		t.Fatal("无有效分块时应报错")
	}
}
