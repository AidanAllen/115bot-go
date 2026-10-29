package bot

import (
	"strings"
	"testing"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// TestPanelsHaveBackButton 每个二级窗口都必须带「返回上一层」。
//
// 子面板是层层点进去的，没有退路就只能重新敲命令；这类缺口只有用户点到才会发现，
// 所以在这里把「面板 → 有返回按钮 → 目标面板存在」整条链路钉死。
func TestPanelsHaveBackButton(t *testing.T) {
	panels := map[string]tgbotapi.InlineKeyboardMarkup{
		"UA 档位面板":    uaPickerMenu(""),
		"转存通道面板":     channelPickerMenu(""),
		"登录设备面板":     appPickerMenu(false),
		"登录设备面板(全部)": appPickerMenu(true),
		"全部命令面板":     helpMenu(),
		"输入提示":       cancelMenu(),
		"目录浏览器底部":    tgbotapi.NewInlineKeyboardMarkup(dirFooterRows(dirTargetTransfer)...),
	}

	for name, kb := range panels {
		found := false
		for _, row := range kb.InlineKeyboard {
			for _, b := range row {
				if b.CallbackData == nil {
					continue
				}
				dest, ok := strings.CutPrefix(*b.CallbackData, cbBackPrefix)
				if !ok {
					continue
				}
				found = true
				if !isKnownPanel(dest) {
					t.Errorf("%s 的返回目标 %q 没有对应面板", name, dest)
				}
			}
		}
		if !found {
			t.Errorf("%s 缺少「返回上一层」按钮", name)
		}
	}
}

// TestBackCallbackShortEnough 返回按钮的回调数据要远小于 TG 的 64 字节上限。
func TestBackCallbackShortEnough(t *testing.T) {
	data := cbBackPrefix + backToSettings
	if len(data) > 64 {
		t.Fatalf("回调数据过长（%d）：%s", len(data), data)
	}
	if !strings.HasPrefix(*backButton(backToSettings).CallbackData, cbBackPrefix) {
		t.Fatal("返回按钮的回调数据缺少 back 前缀")
	}
}

// TestParseDirCallbackIgnoresBack 返回回调不能被目录浏览器或其它前缀吃掉。
func TestParseDirCallbackIgnoresBack(t *testing.T) {
	if _, ok := parseDirCallback(cbBackPrefix + backToSettings); ok {
		t.Fatal("目录浏览器不应解析返回回调")
	}
}
