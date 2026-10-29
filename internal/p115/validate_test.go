package p115

import (
	"strings"
	"testing"
)

func TestExtractLinks(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want int
	}{
		{
			name: "单条磁力链",
			in:   "magnet:?xt=urn:btih:ABCDEF1234567890ABCDEF1234567890ABCDEF12",
			want: 1,
		},
		{
			name: "磁力链带参数与中文包裹",
			in:   "【资源】magnet:?xt=urn:btih:abcdef1234567890abcdef1234567890abcdef12&dn=test&tr=udp://x ，请查收",
			want: 1,
		},
		{
			name: "ed2k 链接",
			in:   "ed2k://|file|movie.mkv|1234567890|ABCDEF1234567890ABCDEF1234567890|/",
			want: 1,
		},
		{
			name: "多条混合",
			in: `magnet:?xt=urn:btih:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa
ed2k://|file|b.mkv|100|BBBBBBBBBBBBBBBBBBBBBBBBBBBBBBBB|/
magnet:?xt=urn:btih:cccccccccccccccccccccccccccccccc`,
			want: 3,
		},
		{
			name: "重复链接去重",
			in: `magnet:?xt=urn:btih:dddddddddddddddddddddddddddddddd
magnet:?xt=urn:btih:DDDDDDDDDDDDDDDDDDDDDDDDDDDDDDDD`,
			want: 1,
		},
		{
			name: "无链接",
			in:   "今天天气不错，没有链接",
			want: 0,
		},
		{
			name: "非 btih 磁力链不应误抓",
			in:   "magnet:?xt=urn:sha1:ABCDEF",
			want: 0,
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := ExtractLinks(c.in)
			if len(got) != c.want {
				t.Fatalf("期望 %d 条，实际 %d 条：%+v", c.want, len(got), got)
			}
		})
	}
}

func TestExtractLinksType(t *testing.T) {
	got := ExtractLinks("ed2k://|file|a.mkv|10|AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA|/")
	if len(got) != 1 || got[0].Type != LinkEd2k {
		t.Fatalf("期望识别为 ed2k，实际 %+v", got)
	}
	got = ExtractLinks("magnet:?xt=urn:btih:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb")
	if len(got) != 1 || got[0].Type != LinkMagnet {
		t.Fatalf("期望识别为 magnet，实际 %+v", got)
	}
}

func TestExtractLinksTrimsPunctuation(t *testing.T) {
	got := ExtractLinks("看这个 magnet:?xt=urn:btih:cccccccccccccccccccccccccccccccc。")
	if len(got) != 1 {
		t.Fatalf("期望 1 条，实际 %d", len(got))
	}
	if strings.HasSuffix(got[0].URL, "。") {
		t.Fatalf("尾部中文标点未清理：%q", got[0].URL)
	}
}

func TestValidateCookie(t *testing.T) {
	ok := []string{
		"USERSESSIONID=abc123; UID=12345; CID=xyz",
		"uid=1; usersessionid=ABC",
		// 扫码 / 第三方登录会话：无 USERSESSIONID，靠 UID+CID+SEID 标识
		"CID=c5dc4a9f; KID=82297c7d; SEID=4391ceab; UID=342204514_R2_1973193990",
		"UID=1@a.com; CID=abc; SEID=def",
	}
	for _, s := range ok {
		if err := ValidateCookie(s); err != nil {
			t.Fatalf("期望通过，实际报错：%v（输入 %q）", err, s)
		}
	}

	bad := map[string]string{
		"空":    "",
		"无键值":  "abcd",
		"缺会话键": "UID=123; CID=456",
		"仅两件套": "UID=123; SEID=456", // 扫码会话三件套不全
		"含换行":  "USERSESSIONID=a\nURL=1",
		"空键名":  "=value; USERSESSIONID=a",
		"片段非法": "USERSESSIONID=a; badpart",
	}
	for name, s := range bad {
		if err := ValidateCookie(s); err == nil {
			t.Fatalf("%s：期望报错，实际通过（输入 %q）", name, s)
		}
	}
}

func TestNormalizeCID(t *testing.T) {
	good := map[string]string{
		" 123456 ":            "123456",
		"0":                   "0",
		"2996801920078707833": "2996801920078707833",
	}
	for in, want := range good {
		got, err := NormalizeCID(in)
		if err != nil {
			t.Fatalf("期望通过，实际报错：%v（输入 %q）", err, in)
		}
		if got != want {
			t.Fatalf("期望 %q，实际 %q", want, got)
		}
	}

	bad := []string{"", "abc", "12a3", "-1", "1.5", "很长的中文串"}
	for _, in := range bad {
		if _, err := NormalizeCID(in); err == nil {
			t.Fatalf("期望报错，实际通过：%q", in)
		}
	}
}

func TestNormalizeTarget(t *testing.T) {
	cases := map[string]string{
		"123":     "U_1_123",
		"U_1_456": "U_1_456",
		"":        "U_1_0",
	}
	for in, want := range cases {
		if got := normalizeTarget(in); got != want {
			t.Fatalf("输入 %q 期望 %q，实际 %q", in, want, got)
		}
	}
}

func TestValidateUA(t *testing.T) {
	if err := ValidateUA("Mozilla/5.0 (Windows NT 10.0; Win64; x64)"); err != nil {
		t.Fatalf("合法 UA 应通过：%v", err)
	}
	if err := ValidateUA(""); err == nil {
		t.Fatal("空 UA 应报错")
	}
	if err := ValidateUA("a\nb"); err == nil {
		t.Fatal("含换行的 UA 应报错")
	}
	if err := ValidateUA(strings.Repeat("x", 600)); err == nil {
		t.Fatal("超长 UA 应报错")
	}
}

func TestIsLoginError(t *testing.T) {
	for _, s := range []string{"请先登录,后操作！", "登录失效", "未登录"} {
		if !isLoginError(s) {
			t.Fatalf("应识别为登录错误：%q", s)
		}
	}
	for _, s := range []string{"磁盘空间不足", ""} {
		if isLoginError(s) {
			t.Fatalf("不应识别为登录错误：%q", s)
		}
	}
}
