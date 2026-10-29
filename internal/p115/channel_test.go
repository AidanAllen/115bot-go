package p115

import (
	"strings"
	"testing"
	"time"
)

func TestUploadChannelsWellFormed(t *testing.T) {
	chs := UploadChannels()
	if len(chs) == 0 {
		t.Fatal("没有任何转存通道")
	}
	ids := map[string]bool{}
	for _, c := range chs {
		if c.ID == "" || c.Name == "" {
			t.Errorf("通道缺少标识或名称：%+v", c)
		}
		if ids[c.ID] {
			t.Errorf("通道标识重复：%s", c.ID)
		}
		ids[c.ID] = true
		if c.AppVersion == "" {
			t.Errorf("通道 %s 缺少 appversion（会进签名计算）", c.ID)
		}
		if err := ValidateUA(c.UA); err != nil {
			t.Errorf("通道 %s 的 UA 不合法：%v", c.ID, err)
		}
		// UA 必须携带与之一致的版本串，否则 115 侧的客户端一致性判断可能拒握手
		if !strings.Contains(c.UA, c.AppVersion) {
			t.Errorf("通道 %s 的 UA 与 appversion 不一致：UA=%q ver=%s", c.ID, c.UA, c.AppVersion)
		}
	}
	if !ids[DefaultUploadChannelID] {
		t.Errorf("默认通道 %q 不在表里", DefaultUploadChannelID)
	}
}

func TestLookupUploadChannel(t *testing.T) {
	for _, pair := range [][2]string{{"web", "web"}, {"WEB", "web"}, {" app ", "app"}, {"wechatmini", "wechatmini"}} {
		got, ok := LookupUploadChannel(pair[0])
		if !ok {
			t.Fatalf("查找 %q 失败", pair[0])
		}
		if got.ID != pair[1] {
			t.Errorf("查找 %q 得到 %q，期望 %q", pair[0], got.ID, pair[1])
		}
	}
	for _, bad := range []string{"", "   ", "流媒体", "openapi"} {
		if _, ok := LookupUploadChannel(bad); ok {
			t.Errorf("%q 不该命中任何通道", bad)
		}
	}
}

// TestDefaultChannelKeepsVerifiedCombo 默认通道必须保持历史验证过的组合。
//
// 上传接口最容易出问题的地方就在 appversion（进 token 签名）与 UA 的搭配，
// 一旦被改动，原先跑通的几十 GB 上传会突然失败，所以钉死这个组合。
func TestDefaultChannelKeepsVerifiedCombo(t *testing.T) {
	def := DefaultUploadChannel()
	if def.AppVersion != uploadAppVersion {
		t.Errorf("默认通道 appversion = %q，期望保持不变 %q", def.AppVersion, uploadAppVersion)
	}
	if def.UA != uploadUA {
		t.Errorf("默认通道 UA 与历史值不一致：\n got: %q\nwant: %q", def.UA, uploadUA)
	}
}

// TestNewUploaderPicksChannel 选了通道就该用它的身份去握手。
func TestNewUploaderPicksChannel(t *testing.T) {
	c := New("UID=1;CID=2;SEID=3", DefaultUA, time.Second)
	if u := NewUploader(c, ""); u.Name() != DefaultUploadChannel().Name {
		t.Errorf("空标识应走默认通道，实际 %q", u.Name())
	}
	if u := NewUploader(c, DefaultUploadChannelID); u.Name() != DefaultUploadChannel().Name {
		t.Errorf("显式默认通道后 Name() = %q", u.Name())
	}
	app, _ := LookupUploadChannel("app")
	if u := NewUploader(c, "app"); u.Name() != app.Name {
		t.Errorf("指定 app 通道后 Name() = %q，期望 %q", u.Name(), app.Name)
	}
	if u := NewUploader(c, "不存在的通道"); u.Name() != DefaultUploadChannel().Name {
		t.Errorf("非法通道标识应回退默认，实际 %q", u.Name())
	}
	// 无登录凭据时不能因为选了通道就假装可用
	if u := NewUploader(New("", "", time.Second), "app"); u.Available() {
		t.Error("无凭据时仍应不可用")
	}
}

// TestWebUploaderUsesChannelUA appversion 与 UA 必须一起注入到上传请求里。
func TestWebUploaderUsesChannelUA(t *testing.T) {
	ch, _ := LookupUploadChannel("wechatmini")
	u := newWebUploaderWithChannel(New("UID=1;CID=2;SEID=3", DefaultUA, time.Second), ch)
	if u.appVersion != ch.AppVersion {
		t.Errorf("appversion 未生效：%q", u.appVersion)
	}
	if u.ua != ch.UA {
		t.Errorf("UA 未生效：%q", u.ua)
	}
	if u.Name() != ch.Name {
		t.Errorf("Name() = %q，期望 %q", u.Name(), ch.Name)
	}
	// 缺省值兜底：空版本串不能产出空白 UA
	blank := newWebUploaderWithChannel(New("UID=1;CID=2;SEID=3", DefaultUA, time.Second), UploadChannel{})
	if blank.appVersion == "" || blank.ua == "" {
		t.Error("空通道未回退到默认身份")
	}
}
