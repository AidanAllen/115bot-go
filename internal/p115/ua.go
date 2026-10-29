package p115

import "strings"

// UAProfile 一个预置 User-Agent 档位。
//
// 115 的部分接口（网页通道上传、离线下发）会按 UA 判断请求来自哪个端，
// 因此把常见客户端的 UA 做成可点选的档位，用户按自己的会话来源选一个即可，
// 比手工粘贴一大串字符串可靠得多。
type UAProfile struct {
	ID   string // 短标识，用于 /ua <id> 与按钮回调
	Name string // 展示名
	UA   string // 实际 UA 字符串
	Note string // 适用会话说明
}

// DefaultUAProfileID 是默认档位，与 /login 扫码写入的 UA 一致。
const DefaultUAProfileID = "web"

// 各端 UA 取值。
//
// desktop 与 app 两条取自线上长期使用的旧版 Java bot 常量，是真实在用的值；
// 两条小程序 UA 按对应客户端的常见格式拼装（含客户端标识，保证 115 侧能识别来源），
// 版本串可能与你抓包到的不一致 —— 这种情况下用 /ua 的「手动输入」粘贴自己的即可。
const (
	uaDesktop    = "Mozilla/5.0 115Desktop/2.0.6.6"
	uaApp        = "Mozilla/5.0 115disk/30.5.1"
	uaAlipayMini = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 AlipayClient/10.5.0 AlipayMiniProgram/1.0"
	uaWeChatMini = "Mozilla/5.0 (iPhone; CPU iPhone OS 16_6 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Mobile/15E148 MicroMessenger/8.0.42(0x18002a32) NetType/WIFI Language/zh_CN miniProgram"
)

// uaProfiles 是全部可选档位，顺序即展示顺序。
var uaProfiles = []UAProfile{
	{ID: "web", Name: "网页端 Chrome", UA: DefaultUA, Note: "默认，扫码登录后自动写入"},
	{ID: "desktop", Name: "115 桌面端", UA: uaDesktop, Note: "115 PC 客户端 v2.0.6.6"},
	{ID: "app", Name: "115 手机 App", UA: uaApp, Note: "v30.5.1，安卓 / iOS 通用样式"},
	{ID: "alipaymini", Name: "支付宝小程序", UA: uaAlipayMini, Note: "用支付宝扫码登录时配套"},
	{ID: "wechatmini", Name: "微信小程序", UA: uaWeChatMini, Note: "用微信小程序登录时配套"},
}

// UAProfiles 返回全部档位（副本，调用方修改不影响内部表）。
func UAProfiles() []UAProfile {
	out := make([]UAProfile, len(uaProfiles))
	copy(out, uaProfiles)
	return out
}

// LookupUAProfile 按档位标识查找，忽略大小写与首尾空格。
func LookupUAProfile(id string) (UAProfile, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	if key == "" {
		return UAProfile{}, false
	}
	for _, p := range uaProfiles {
		if p.ID == key {
			return p, true
		}
	}
	return UAProfile{}, false
}

// MatchUAProfile 判断一段 UA 是否正好等于某个预置档位，用于展示当前所处档位。
func MatchUAProfile(ua string) (UAProfile, bool) {
	s := strings.TrimSpace(ua)
	if s == "" {
		return UAProfile{}, false
	}
	for _, p := range uaProfiles {
		if p.UA == s {
			return p, true
		}
	}
	return UAProfile{}, false
}
