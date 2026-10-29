package p115

import "testing"

func TestUAProfilesWellFormed(t *testing.T) {
	ps := UAProfiles()
	if len(ps) == 0 {
		t.Fatal("没有任何 UA 档位")
	}

	ids := map[string]bool{}
	uas := map[string]bool{}
	for _, p := range ps {
		if p.ID == "" || p.Name == "" {
			t.Errorf("档位缺少标识或名称：%+v", p)
		}
		if ids[p.ID] {
			t.Errorf("档位标识重复：%s", p.ID)
		}
		ids[p.ID] = true
		if err := ValidateUA(p.UA); err != nil {
			t.Errorf("档位 %s 的 UA 通不过校验：%v", p.ID, err)
		}
		if uas[p.UA] {
			t.Errorf("档位 UA 取值重复：%s", p.ID)
		}
		uas[p.UA] = true
	}
	if !ids[DefaultUAProfileID] {
		t.Errorf("默认档位 %q 不在表里", DefaultUAProfileID)
	}
}

func TestLookupUAProfile(t *testing.T) {
	ok := [][]string{
		{"web", "web"},
		{"WEB", "web"},
		{"  desktop ", "desktop"},
		{"app", "app"},
		{"alipaymini", "alipaymini"},
		{"wechatmini", "wechatmini"},
	}
	for _, pair := range ok {
		got, found := LookupUAProfile(pair[0])
		if !found {
			t.Fatalf("查找 %q 失败", pair[0])
		}
		if got.ID != pair[1] {
			t.Errorf("查找 %q 得到 %q，期望 %q", pair[0], got.ID, pair[1])
		}
	}

	for _, bad := range []string{"", "  ", "mozilla", "ie6", "自定义"} {
		if _, found := LookupUAProfile(bad); found {
			t.Errorf("%q 不该命中任何档位", bad)
		}
	}
}

// TestDefaultProfileMatchesDefaultUA 默认档位的取值必须就是包的默认 UA，
// 否则 /login 写入的 UA 会在菜单里显示成「自定义」。
func TestDefaultProfileMatchesDefaultUA(t *testing.T) {
	p, found := LookupUAProfile(DefaultUAProfileID)
	if !found {
		t.Fatalf("找不到默认档位 %q", DefaultUAProfileID)
	}
	if p.UA != DefaultUA {
		t.Errorf("默认档位 UA = %q，与 DefaultUA %q 不一致", p.UA, DefaultUA)
	}
}

func TestMatchUAProfileRoundTrip(t *testing.T) {
	for _, p := range UAProfiles() {
		got, found := MatchUAProfile("  " + p.UA + "  ")
		if !found {
			t.Fatalf("预置 UA 未匹配到任何档位：%s", p.ID)
		}
		if got.ID != p.ID {
			t.Errorf("UA 匹配错档位：得到 %s，期望 %s", got.ID, p.ID)
		}
	}
	if _, found := MatchUAProfile("Mozilla/5.0 (Windows NT 10.0) 我自己抓的"); found {
		t.Error("自定义 UA 不应匹配到预置档位")
	}
	if _, found := MatchUAProfile(""); found {
		t.Error("空 UA 不应匹配到档位")
	}
}
