package bot

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// TestRenderAppList 确认设备列表包含全部类型、默认项和风险提示。
func TestRenderAppList(t *testing.T) {
	out := renderAppList()
	if out == "" {
		t.Fatal("列表不应为空")
	}
	// 默认设备必须出现且带标记
	if !strings.Contains(out, "qandroid") {
		t.Fatalf("应包含默认设备 qandroid，实际输出:\n%s", out)
	}
	if !strings.Contains(out, "← 默认") {
		t.Fatalf("应标出默认设备，实际输出:\n%s", out)
	}
	// 高风险设备要有警告
	for _, want := range []string{"web", "ios", "android", "alipaymini"} {
		if !strings.Contains(out, want) {
			t.Fatalf("应包含设备 %q，实际输出:\n%s", want, out)
		}
	}
	if !strings.Contains(out, "互踢") {
		t.Fatalf("应包含互踢风险提示，实际输出:\n%s", out)
	}
}

// TestLoginErrText 覆盖等待过程中的错误映射。
func TestLoginErrText(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"超时", context.DeadlineExceeded, TxtLoginExpired},
		{"取消", context.Canceled, TxtLoginExpired},
		{"过期", errors.New("二维码已过期，请重新发起扫码"), TxtLoginExpired},
		{"用户取消", errors.New("已在手机端取消登录"), TxtLoginCanceled},
	}
	for _, c := range cases {
		if got := loginErrText(c.err); got != c.want {
			t.Fatalf("%s: 期望 %q，实际 %q", c.name, c.want, got)
		}
	}

	// 其他错误应带原因
	got := loginErrText(errors.New("网络不可达"))
	if !strings.Contains(got, "网络不可达") {
		t.Fatalf("应保留原始错误信息，实际 %q", got)
	}
}

// TestLoginManagerRegisterAndStop 验证登记与取消的基本语义。
func TestLoginManagerRegisterAndStop(t *testing.T) {
	a := &App{}

	ctx, cancel := context.WithCancel(context.Background())
	st := &qrLoginState{cancel: cancel, app: "qandroid", msgID: 1}
	a.setLogin(100, st)

	if !a.stopLogin(100) {
		t.Fatal("存在会话时 stopLogin 应返回 true")
	}
	select {
	case <-ctx.Done():
	default:
		t.Fatal("stopLogin 应调用 cancel")
	}
	if a.stopLogin(100) {
		t.Fatal("已无会话时 stopLogin 应返回 false")
	}
}

// TestSetLoginReplacesPrevious 同一 chat 再次发起登录时，应取消上一条。
func TestSetLoginReplacesPrevious(t *testing.T) {
	a := &App{}

	ctx1, cancel1 := context.WithCancel(context.Background())
	s1 := &qrLoginState{cancel: cancel1, app: "qandroid", msgID: 1}
	a.setLogin(200, s1)

	ctx2, cancel2 := context.WithCancel(context.Background())
	s2 := &qrLoginState{cancel: cancel2, app: "web", msgID: 2}
	a.setLogin(200, s2)

	select {
	case <-ctx1.Done():
	default:
		t.Fatal("新会话登记时应取消旧会话")
	}
	select {
	case <-ctx2.Done():
		t.Fatal("新会话不应被取消")
	default:
	}

	a.loginMu.Lock()
	cur := a.logins[200]
	a.loginMu.Unlock()
	if cur != s2 {
		t.Fatal("当前会话应为后登记的那条")
	}
}

// TestClearLoginOnlyRemovesMatching 迟到的 goroutine 不能误删后续新建的会话。
func TestClearLoginOnlyRemovesMatching(t *testing.T) {
	a := &App{}

	_, cancel1 := context.WithCancel(context.Background())
	s1 := &qrLoginState{cancel: cancel1, msgID: 1}
	_, cancel2 := context.WithCancel(context.Background())
	s2 := &qrLoginState{cancel: cancel2, msgID: 2}

	a.setLogin(300, s2)
	// 模拟旧 goroutine 退出时的清理
	a.clearLogin(300, s1)

	a.loginMu.Lock()
	_, still := a.logins[300]
	a.loginMu.Unlock()
	if !still {
		t.Fatal("清理旧会话时不应删除新会话")
	}

	// 真正属于它的清理应生效
	a.clearLogin(300, s2)
	a.loginMu.Lock()
	_, still = a.logins[300]
	a.loginMu.Unlock()
	if still {
		t.Fatal("匹配的会话应被删除")
	}
}

// TestLoginManagerIsolation 不同会话互不影响。
func TestLoginManagerIsolation(t *testing.T) {
	a := &App{}

	ctx1, cancel1 := context.WithCancel(context.Background())
	ctx2, cancel2 := context.WithCancel(context.Background())
	a.setLogin(1, &qrLoginState{cancel: cancel1, msgID: 1})
	a.setLogin(2, &qrLoginState{cancel: cancel2, msgID: 2})

	if !a.stopLogin(1) {
		t.Fatal("会话 1 应存在")
	}
	select {
	case <-ctx1.Done():
	default:
		t.Fatal("会话 1 应被取消")
	}
	select {
	case <-ctx2.Done():
		t.Fatal("会话 2 不应受影响")
	default:
	}
}
