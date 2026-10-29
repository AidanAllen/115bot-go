package bot

import (
	"strings"
	"testing"
	"time"
)

func TestSessionTakeOnce(t *testing.T) {
	m := newSessionManager(time.Minute)
	m.Set(100, ActionCookie)

	act, ok := m.Take(100)
	if !ok || act != ActionCookie {
		t.Fatalf("Take 期望 cookie，实际 %v %v", act, ok)
	}

	// 已消费，第二次应取不到
	if _, ok := m.Take(100); ok {
		t.Fatal("状态应已被消费，第二次不应取到")
	}
}

func TestSessionExpire(t *testing.T) {
	m := newSessionManager(time.Minute)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	m.now = func() time.Time { return base }

	m.Set(200, ActionUA)

	// 未过期
	if _, ok := m.Take(200); !ok {
		t.Fatal("未过期时应可取到")
	}

	// 前进 2 分钟后新设置一个状态，应已过期
	m.Set(200, ActionUA)
	m.now = func() time.Time { return base.Add(2 * time.Minute) }
	if _, ok := m.Take(200); ok {
		t.Fatal("过期后不应取到")
	}
}

func TestSessionIsolationByChat(t *testing.T) {
	m := newSessionManager(time.Minute)
	m.Set(1, ActionCookie)
	m.Set(2, ActionTransferCID)

	if act, _ := m.Take(1); act != ActionCookie {
		t.Fatalf("chat 1 期望 cookie，实际 %v", act)
	}
	// 消费 chat 1 不应影响 chat 2
	if act, _ := m.Take(2); act != ActionTransferCID {
		t.Fatalf("chat 2 期望 transfercid，实际 %v", act)
	}
}

func TestActionPromptCoverage(t *testing.T) {
	all := []Action{ActionCookie, ActionUA, ActionTransferCID, ActionOfflineCID, ActionSetID}
	for _, a := range all {
		if actionPrompt(a) == "" {
			t.Fatalf("动作 %q 缺少提示语", a)
		}
	}
	if p := actionPrompt(Action("不存在的动作")); p != "" {
		t.Fatal("未知动作应返回空提示")
	}
}

func TestRenderMenuText(t *testing.T) {
	text := renderMenuText(accountSnapshot{
		Enabled:       true,
		HasCookie:     true,
		UALabel:       "", // 完全没配 UA
		LoginOK:       true,
		Nickname:      "tester",
		TransferCID:   "111",
		OfflineCID:    "222",
		UploaderName:  "115 网页通道",
		UploaderReady: true,
	})
	for _, want := range []string{"115 设置", "已开启", "tester", "未设置", "111", "222", "115 网页通道"} {
		if !strings.Contains(text, want) {
			t.Fatalf("菜单文本缺少 %q：\n%s", want, text)
		}
	}
	// 通道名必须来自实际选中的上传器，不能写死
	if strings.Contains(text, "开放平台") {
		t.Fatalf("通道名不应写死为开放平台：\n%s", text)
	}

	// 选了档位后要显示档位名；显式设置与内置默认要有区分
	explicit := renderMenuText(accountSnapshot{UALabel: "支付宝小程序", UAExplicit: true})
	if !strings.Contains(explicit, "支付宝小程序") || strings.Contains(explicit, "内置默认") {
		t.Fatalf("显式档位显示不正确：\n%s", explicit)
	}
	builtin := renderMenuText(accountSnapshot{UALabel: "网页端 Chrome"})
	if !strings.Contains(builtin, "网页端 Chrome") || !strings.Contains(builtin, "内置默认") {
		t.Fatalf("内置默认档位显示不正确：\n%s", builtin)
	}
}

func TestSanitizeFileName(t *testing.T) {
	cases := map[string]string{
		"../../etc/passwd": "_.._etc_passwd",
		"a/b\\c.mp4":       "a_b_c.mp4",
		"  正常名称.mp4  ":     "正常名称.mp4",
	}
	for in, want := range cases {
		if got := sanitizeFileName(in); got != want {
			t.Fatalf("输入 %q 期望 %q，实际 %q", in, want, got)
		}
	}
}

func TestHumanSize(t *testing.T) {
	cases := map[int64]string{
		512:                    "512 B",
		2048:                   "2.00 KB",
		5 * 1024 * 1024:        "5.00 MB",
		3 * 1024 * 1024 * 1024: "3.00 GB",
	}
	for in, want := range cases {
		if got := humanSize(in); got != want {
			t.Fatalf("输入 %d 期望 %q，实际 %q", in, want, got)
		}
	}
}
