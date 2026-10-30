package p115

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/md5"
	"crypto/sha1"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"hash/crc32"
	"net/url"
	"sort"
	"strings"
)

// 115 客户端内置的固定常量。
//
// 说明：4.0/initupload.php 的请求体虽名为「ECDH + AES」，但实际用的是硬编码的
// 公钥与 AES 密钥，并没有真正的密钥协商（无随机性）。因此可完整复刻。
// 这些值来自 115 客户端二进制，属于协议常量，不是账号凭据。
var (
	cipherCRCSalt = []byte("^j>WD3Kr?J2gLFjD4W2y@")
	cipherMD5Salt = []byte("Qclm8MGWUv59TnrR0XPg")
	cipherPubKey  = []byte{
		0x1d, 0x03, 0x0e, 0x80, 0xa1, 0x78, 0xdc, 0xee, 0xce, 0xcd,
		0xa3, 0x77, 0xde, 0x12, 0x8d, 0x8e, 0xd9, 0xdd, 0xcf, 0x55,
		0xae, 0x61, 0xed, 0x46, 0xea, 0x12, 0x1a, 0x1c, 0xfc, 0x81,
	}
	cipherAESKey = []byte{
		0xfb, 0x1a, 0x19, 0xd6, 0x52, 0xf5, 0xaa, 0xf7,
		0xbc, 0x65, 0x1d, 0x0f, 0x69, 0xbf, 0x42, 0x2f,
	}
	cipherAESIV = []byte{
		0x69, 0xbf, 0x42, 0x2f, 0x49, 0x96, 0x05, 0x50,
		0xa0, 0xad, 0x44, 0xec, 0x34, 0x46, 0xcb, 0x4c,
	}
)

// aesBlockSize 是 AES 分组长度，加解密与响应截断都要用到。
const aesBlockSize = aes.BlockSize

// uploadAppVersion 是上传接口声明的客户端版本，会参与 token 计算与 UA 组装。
// 与 channel.go 的 appVerMain 保持同一份取值，避免两处硬编码同一版本号导致漂移。
const uploadAppVersion = appVerMain

// uploadUA 是上传接口要求的 UA。115 会校验 UA 与 appversion 的一致性。
// 由 chUA 统一生成，与默认网页通道的 UA 严格一致。
//
// 两个用途：newWebUploaderWithChannel 在通道 UA 为空时用它兜底；
// channel_test.go 用它锚定默认通道 UA（常量与通道表两处取值不允许漂移）。
const uploadUA = "Mozilla/5.0 115disk/" + uploadAppVersion + " 115Browser/" + uploadAppVersion + " 115wangpan_android/" + uploadAppVersion

// ecdhEncodeToken 生成请求参数 k_ec。
//
// 结构（48 字节）：pubkey[0:15] + 0x00 0x73 0x00*3 + ts(LE32)
// + pubkey[15:30] + 0x00 0x01 0x00*3 + crc32(CRC_SALT+前缀)(LE32)，最后 base64。
func ecdhEncodeToken(ts int64) string {
	buf := make([]byte, 0, 48)
	buf = append(buf, cipherPubKey[:15]...)
	buf = append(buf, 0x00, 0x73, 0x00, 0x00, 0x00)
	buf = binary.LittleEndian.AppendUint32(buf, uint32(ts))
	buf = append(buf, cipherPubKey[15:]...)
	buf = append(buf, 0x00, 0x01, 0x00, 0x00, 0x00)

	crc := crc32.NewIEEE()
	_, _ = crc.Write(cipherCRCSalt)
	_, _ = crc.Write(buf)
	buf = binary.LittleEndian.AppendUint32(buf, crc.Sum32())

	return base64.StdEncoding.EncodeToString(buf)
}

// makeUploadSig 计算请求体的 sig 字段。
//
//	sig = UPPER( SHA1( userkey || hex(SHA1(userid+fileid+target+"0")) || "000000" ) )
func makeUploadSig(userkey, userid, fileid, target string) string {
	inner := sha1.Sum([]byte(userid + fileid + target + "0"))

	h := sha1.New()
	_, _ = h.Write([]byte(userkey))
	_, _ = h.Write([]byte(hex.EncodeToString(inner[:])))
	_, _ = h.Write([]byte("000000"))
	return strings.ToUpper(hex.EncodeToString(h.Sum(nil)))
}

// makeUploadToken 计算请求体的 token 字段。
//
//	token = hex( MD5( MD5_SALT || fileid+filesize+signKey+signVal+userid+t
//	                  || hex(MD5(十进制 userid)) || appversion ) )
func makeUploadToken(fileid, filesize, signKey, signVal, userid string, t int64, appversion string) string {
	uidNum := md5.Sum([]byte(userid))

	h := md5.New()
	_, _ = h.Write(cipherMD5Salt)
	_, _ = h.Write([]byte(fileid + filesize + signKey + signVal + userid + fmt.Sprintf("%d", t)))
	_, _ = h.Write([]byte(hex.EncodeToString(uidNum[:])))
	_, _ = h.Write([]byte(appversion))
	return hex.EncodeToString(h.Sum(nil))
}

// aesCBCEncrypt 用固定 AES-128 密钥做 CBC 加密，PKCS#7 填充。
func aesCBCEncrypt(plain []byte) ([]byte, error) {
	block, err := aes.NewCipher(cipherAESKey)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	padded := pkcs7Pad(plain, aes.BlockSize)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, cipherAESIV).CryptBlocks(out, padded)
	return out, nil
}

// aesCBCDecrypt 用固定 AES-128 密钥做 CBC 解密并去除 PKCS#7 填充。
func aesCBCDecrypt(cipherText []byte) ([]byte, error) {
	block, err := aes.NewCipher(cipherAESKey)
	if err != nil {
		return nil, fmt.Errorf("初始化 AES 失败: %w", err)
	}
	if len(cipherText) == 0 || len(cipherText)%aes.BlockSize != 0 {
		return nil, fmt.Errorf("密文长度非法: %d", len(cipherText))
	}
	out := make([]byte, len(cipherText))
	cipher.NewCBCDecrypter(block, cipherAESIV).CryptBlocks(out, cipherText)
	return pkcs7Unpad(out, aes.BlockSize), nil
}

// pkcs7Pad 按块大小补齐。整块对齐时补满一整块（与 115 的实现一致）。
func pkcs7Pad(data []byte, blockSize int) []byte {
	pad := blockSize - len(data)%blockSize
	return append(data, bytes.Repeat([]byte{byte(pad)}, pad)...)
}

// pkcs7Unpad 去除填充。填充非法时原样返回，避免把解密失败误判为数据损坏。
func pkcs7Unpad(data []byte, blockSize int) []byte {
	if len(data) == 0 {
		return data
	}
	pad := int(data[len(data)-1])
	if pad == 0 || pad > blockSize || pad > len(data) {
		return data
	}
	for _, b := range data[len(data)-pad:] {
		if int(b) != pad {
			return data
		}
	}
	return data[:len(data)-pad]
}

// lz4BlockDecompress 解压单个 LZ4 block（无帧头的 block 格式）。
//
// 115 的响应体是若干「2 字节小端长度 + LZ4 block」串接，每块解压上限 8KB。
// 这里自行实现以保持零外部依赖、单二进制交付。
func lz4BlockDecompress(src []byte) ([]byte, error) {
	dst := make([]byte, 0, len(src)*3)
	i := 0
	for i < len(src) {
		token := src[i]
		i++

		// 字面量段
		litLen := int(token >> 4)
		if litLen == 15 {
			for i < len(src) {
				b := src[i]
				i++
				litLen += int(b)
				if b != 255 {
					break
				}
			}
		}
		if i+litLen > len(src) {
			return nil, fmt.Errorf("LZ4 字面量越界: need %d, remain %d", litLen, len(src)-i)
		}
		dst = append(dst, src[i:i+litLen]...)
		i += litLen

		// 最后一个序列只有字面量，没有匹配段
		if i >= len(src) {
			break
		}

		// 匹配段：2 字节小端偏移量
		if i+2 > len(src) {
			return nil, fmt.Errorf("LZ4 偏移量越界")
		}
		offset := int(src[i]) | int(src[i+1])<<8
		i += 2
		if offset == 0 || offset > len(dst) {
			return nil, fmt.Errorf("LZ4 非法偏移量: %d（已输出 %d）", offset, len(dst))
		}

		matchLen := int(token & 0x0F)
		if matchLen == 15 {
			for i < len(src) {
				b := src[i]
				i++
				matchLen += int(b)
				if b != 255 {
					break
				}
			}
		}
		matchLen += 4

		// 逐字节复制以正确处理重叠区（LZ4 允许 offset < matchLen）
		start := len(dst) - offset
		for k := 0; k < matchLen; k++ {
			dst = append(dst, dst[start+k])
		}
	}
	return dst, nil
}

// lz4FramesDecompress 解压 115 的「多 block 拼接」格式。
//
// 该格式不带总长度，是若干「2 字节小端长度 + block」的串接。
// 响应经 AES 解密后尾部可能残留未去掉的填充字节（115 的填充长度不固定），
// 因此这里按块自描述结构推进：一旦剩余字节凑不出一个完整分块就停止，
// 而不是报错——否则一个字节的残留就会让整个响应不可用。
// 真正的数据问题会由随后的 JSON 解析暴露。
func lz4FramesDecompress(src []byte) ([]byte, error) {
	var out []byte
	i := 0
	for i+2 <= len(src) {
		n := int(binary.LittleEndian.Uint16(src[i : i+2]))
		if n == 0 || i+2+n > len(src) {
			break // 尾部残留/长度越界，视为结束
		}
		block, err := lz4BlockDecompress(src[i+2 : i+2+n])
		if err != nil {
			return nil, err
		}
		out = append(out, block...)
		i += 2 + n
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("LZ4 未解出任何数据（明文头 %x）", head(src, 8))
	}
	return out, nil
}

// encodeUploadBody 把参数字典编码为 115 要求的加密请求体，并返回 URL 上的 k_ec。
//
// 关键细节：空值字段会被丢弃。115 服务端对空字段敏感（例如 sign_key/sign_val
// 在首次请求时必须完全不出现，而不是传空串），且签名 token 基于实际提交的字段计算。
// 注意 appid 固定为 0 时也属于「不提交」——115 侧有默认值。
func encodeUploadBody(fields map[string]string, ts int64) (kEc string, body []byte, err error) {
	vals := url.Values{}
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		v := fields[k]
		if v == "" {
			continue
		}
		vals.Set(k, v)
	}
	plain := []byte(vals.Encode())

	body, err = aesCBCEncrypt(plain)
	if err != nil {
		return "", nil, err
	}
	return ecdhEncodeToken(ts), body, nil
}
