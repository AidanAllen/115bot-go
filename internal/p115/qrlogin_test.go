package p115

import (
	"bytes"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
	"strings"
	"testing"
)

// TestParseCookieString 覆盖 cookie 为字符串的形态。
func TestParseCookieString(t *testing.T) {
	raw := json.RawMessage(`{"state":1,"code":0,"data":{"cookie":"UID=u1; CID=c1; SEID=s1; KID=k1"}}`)
	got, err := ParseCookie(raw)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	want := "UID=u1; CID=c1; SEID=s1; KID=k1"
	if got != want {
		t.Fatalf("cookie 不符\n want=%q\n  got=%q", want, got)
	}
}

// TestParseCookieObject 覆盖 cookie 为对象的形态。
// 这是 web 端扫码真实返回的形状：早期只按字符串解析，会误报「解析失败」。
func TestParseCookieObject(t *testing.T) {
	raw := json.RawMessage(`{"state":1,"code":0,"data":{"cookie":{"UID":"u1","CID":"c1","SEID":"s1","KID":"k1"}}}`)
	got, err := ParseCookie(raw)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	// 键按字典序输出，保证结果稳定
	want := "CID=c1; KID=k1; SEID=s1; UID=u1"
	if got != want {
		t.Fatalf("cookie 不符\n want=%q\n  got=%q", want, got)
	}
}

// TestParseCookieObjectMixedValues 覆盖对象里混有数字/空值/嵌套的情况，
// 要求标量保留、空值与嵌套跳过，且不产生 `key=` 这类半截项。
func TestParseCookieObjectMixedValues(t *testing.T) {
	raw := json.RawMessage(`{"state":1,"data":{"cookie":{
		"UID":"u1",
		"UIDEXP":1793164808,
		"EMPTY":"",
		"NIL":null,
		"NESTED":{"a":"b"}
	}}}`)
	got, err := ParseCookie(raw)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	want := "UID=u1; UIDEXP=1793164808"
	if got != want {
		t.Fatalf("cookie 不符\n want=%q\n  got=%q", want, got)
	}
}

// TestParseCookieTopLevelFallback 兼容 cookie 直接挂在顶层的情况。
func TestParseCookieTopLevelFallback(t *testing.T) {
	raw := json.RawMessage(`{"state":1,"cookie":"UID=u1"}`)
	got, err := ParseCookie(raw)
	if err != nil {
		t.Fatalf("不应报错: %v", err)
	}
	if got != "UID=u1" {
		t.Fatalf("期望 UID=u1，实际 %q", got)
	}
}

// TestParseCookieInvalid 覆盖各种应报错的输入，确保错误可读且不 panic。
func TestParseCookieInvalid(t *testing.T) {
	cases := map[string]string{
		"缺 cookie 字段": `{"state":1,"data":{}}`,
		"cookie 为空串":  `{"state":1,"data":{"cookie":"   "}}`,
		"cookie 空对象":  `{"state":1,"data":{"cookie":{}}}`,
		"cookie 是数组":  `{"state":1,"data":{"cookie":["a"]}}`,
		"cookie 是数字":  `{"state":1,"data":{"cookie":123}}`,
	}
	for name, body := range cases {
		if _, err := ParseCookie(json.RawMessage(body)); err == nil {
			t.Fatalf("%s 应报错，实际未报", name)
		}
	}
}

// TestQRStatusString 锁定状态文案，TG 侧直接展示这些字符串。
func TestQRStatusString(t *testing.T) {
	cases := []struct {
		st   QRStatus
		want string
	}{
		{QRWaiting, "waiting"},
		{QRScanned, "scanned"},
		{QRSigned, "signed"},
		{QRExpired, "expired"},
		{QRCanceled, "canceled"},
		{QRAborted, "aborted"},
	}
	for _, c := range cases {
		if got := c.st.String(); got != c.want {
			t.Fatalf("%d 期望 %q，实际 %q", int(c.st), c.want, got)
		}
	}
	// 未知值不应 panic
	if got := QRStatus(42).String(); !strings.Contains(got, "42") {
		t.Fatalf("未知状态应包含原值，实际 %q", got)
	}
}

// TestUpscalePNG 验证放大只改尺寸、不改内容（像素颜色逐一对应）。
func TestUpscalePNG(t *testing.T) {
	src := image.NewRGBA(image.Rect(0, 0, 4, 4))
	for y := 0; y < 4; y++ {
		for x := 0; x < 4; x++ {
			if (x+y)%2 == 0 {
				src.Set(x, y, color.RGBA{0, 0, 0, 255})
			} else {
				src.Set(x, y, color.RGBA{255, 255, 255, 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}

	out, err := UpscalePNG(buf.Bytes(), 3)
	if err != nil {
		t.Fatalf("放大失败: %v", err)
	}
	img, err := png.Decode(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("解码放大结果失败: %v", err)
	}
	if got := img.Bounds(); got.Dx() != 12 || got.Dy() != 12 {
		t.Fatalf("尺寸期望 12x12，实际 %dx%d", got.Dx(), got.Dy())
	}
	// 抽查：源 (1,0) 为白，(1,0)*3 范围内都应为白
	r, g, b, _ := img.At(3, 0).RGBA()
	if r>>8 != 255 || g>>8 != 255 || b>>8 != 255 {
		t.Fatalf("放大后像素值应保持白色，实际 rgb=%d,%d,%d", r>>8, g>>8, b>>8)
	}
	// 源 (0,0) 为黑
	r, g, b, _ = img.At(0, 0).RGBA()
	if r>>8 != 0 || g>>8 != 0 || b>>8 != 0 {
		t.Fatalf("放大后像素值应保持黑色，实际 rgb=%d,%d,%d", r>>8, g>>8, b>>8)
	}
}

// TestUpscalePNGInvalid 非 PNG 输入应报错而不是静默返回垃圾。
func TestUpscalePNGInvalid(t *testing.T) {
	if _, err := UpscalePNG([]byte("not a png"), 2); err == nil {
		t.Fatal("非 PNG 输入应报错")
	}
}
