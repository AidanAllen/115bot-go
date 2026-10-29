package p115

import "strings"

// UploadChannel 是一个可选的转存（上传）通道。
//
// 115 的上传握手（uploadinfo → initupload → STS → OSS）本身只有一套实现，
// 各端真正的差别在于请求声明的**客户端身份**：appversion（参与 token 计算）
// 与 User-Agent。这里把它们成对地做成可点选的通道，
// 让用户按自己的 115 会话来源选择，而不是替他猜。
type UploadChannel struct {
	ID         string // 短标识，用于 /channel <id> 与按钮回调
	Name       string // 展示名
	AppVersion string // 上传接口声明的客户端版本（参与 token 计算）
	UA         string // 该通道使用的 User-Agent
	Note       string // 适用会话说明
}

// DefaultUploadChannelID 是默认通道。它的组合是当前唯一经过完整真实环境验证的一套。
const DefaultUploadChannelID = "web"

// 各通道的客户端版本取值。
//
// appVerDesktop / appVerApp 取自旧版 Java bot 里实际在用的客户端版本串；
// 两条小程序没有可靠的公开版本号，因此沿用同一版本号 ——
// 它们与 app 通道的区别只在 UA（115 侧据此判断来源）。
const (
	appVerMain    = "36.2.28"
	appVerDesktop = "2.0.6.6"
	appVerApp     = "30.5.1"
)

// chUA 构造 app 类客户端的 UA（沿用 115 官方 UA 的三段结构）。
func chUA(ver, tag string) string {
	return "Mozilla/5.0 115disk/" + ver + " 115Browser/" + ver + " " + tag + ver
}

// uploadChannels 是全部可选通道，顺序即展示顺序。
var uploadChannels = []UploadChannel{
	{
		ID:         "web",
		Name:       "115 网页通道",
		AppVersion: appVerMain,
		UA:         chUA(appVerMain, "115wangpan_android/"),
		Note:       "默认，唯一完成真实环境全链路验证的通道",
	},
	{
		ID:         "app",
		Name:       "115 App 通道",
		AppVersion: appVerApp,
		UA:         chUA(appVerApp, "115wangpan_android/"),
		Note:       "以手机 App 旧版客户端身份上传",
	},
	{
		ID:         "alipaymini",
		Name:       "支付宝小程序通道",
		AppVersion: appVerApp,
		UA:         chUA(appVerApp, "115wangpan_alipaymini/"),
		Note:       "用支付宝小程序会话时配套",
	},
	{
		ID:         "wechatmini",
		Name:       "微信小程序通道",
		AppVersion: appVerApp,
		UA:         chUA(appVerApp, "115wangpan_wechatmini/"),
		Note:       "用微信小程序会话时配套",
	},
}

// UploadChannels 返回全部通道（副本）。
func UploadChannels() []UploadChannel {
	out := make([]UploadChannel, len(uploadChannels))
	copy(out, uploadChannels)
	return out
}

// LookupUploadChannel 按标识查找通道，忽略大小写与首尾空格。
func LookupUploadChannel(id string) (UploadChannel, bool) {
	key := strings.ToLower(strings.TrimSpace(id))
	if key == "" {
		return UploadChannel{}, false
	}
	for _, c := range uploadChannels {
		if c.ID == key {
			return c, true
		}
	}
	return UploadChannel{}, false
}

// DefaultUploadChannel 返回默认通道。
func DefaultUploadChannel() UploadChannel {
	c, ok := LookupUploadChannel(DefaultUploadChannelID)
	if !ok {
		// 表被改坏时的兜底：常量不一致属于编码错误，不应静默返回空值
		return UploadChannel{ID: "web", Name: "115 网页通道", AppVersion: appVerMain, UA: chUA(appVerMain, "115wangpan_android/")}
	}
	return c
}
