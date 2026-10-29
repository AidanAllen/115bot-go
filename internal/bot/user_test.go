package bot

import (
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/AidanAllen/115bot-go/internal/config"
	"github.com/AidanAllen/115bot-go/internal/p115"
	"github.com/AidanAllen/115bot-go/internal/store"
)

// newUserTestApp 造一个只带存储与配置的 App（不发消息，只验证数据归属）。
func newUserTestApp(t *testing.T) *App {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &App{
		st: st,
		cfg: &config.Config{
			HTTPTimeout: 5 * time.Second,
			DefaultUA:   p115.DefaultUA,
		},
		log:      slog.New(slog.NewTextHandler(io.Discard, nil)),
		sessions: newSessionManager(time.Minute),
		logins:   map[int64]*qrLoginState{},
	}
}

// TestUserCredentialsAreIsolated 一个人的 115 凭据绝不能出现在另一个人名下。
//
// 这是「每用户独立 115 账号」的地基：一旦串号，A 的文件就会传到 B 的网盘，
// 而且从日志上看一切正常，只能等用户自己发现。
func TestUserCredentialsAreIsolated(t *testing.T) {
	a := newUserTestApp(t)
	const alice, bob = int64(111), int64(222)

	if a.userLoggedIn(alice) || a.userLoggedIn(bob) {
		t.Fatal("初始状态不应有人已登录")
	}

	if err := a.st.SetUserConfig(alice, store.KeyCookie, "UID=1;CID=1;SEID=1"); err != nil {
		t.Fatal(err)
	}
	if !a.userLoggedIn(alice) {
		t.Fatal("alice 应已登录")
	}
	if a.userLoggedIn(bob) {
		t.Fatal("bob 没登录，却被判为已登录")
	}
	if a.userClient(bob).Ready() {
		t.Fatal("bob 的客户端不该带着 alice 的 cookie 就绪")
	}

	if err := a.st.SetUserConfig(alice, store.KeyTransferCID, "3550559550308353647"); err != nil {
		t.Fatal(err)
	}
	if got := a.userTransferCID(alice); got != "3550559550308353647" {
		t.Fatalf("alice 的转存目录不对：%q", got)
	}
	if got := a.userTransferCID(bob); got != "" {
		t.Fatalf("bob 不该继承 alice 的转存目录：%q", got)
	}

	if err := a.st.SetUserBool(alice, store.KeyBotEnabled, false); err != nil {
		t.Fatal(err)
	}
	if a.userEnabled(alice) {
		t.Fatal("alice 的开关没生效")
	}
	if !a.userEnabled(bob) {
		t.Fatal("bob 的开关被 alice 连坐")
	}

	// 用一个非默认的档位，才能和「走内置默认」区分开
	profiles := p115.UAProfiles()
	chosen := profiles[len(profiles)-1]
	if chosen.UA == a.cfg.DefaultUA {
		t.Fatalf("档位 %q 与默认 UA 相同，测不出隔离", chosen.ID)
	}
	if err := a.saveUserUA(alice, chosen.UA); err != nil {
		t.Fatal(err)
	}
	if a.userUAProfileID(alice) != chosen.ID {
		t.Fatalf("alice 的 UA 档位没落库：%q", a.userUAProfileID(alice))
	}
	if a.userUA(bob) == chosen.UA {
		t.Fatal("bob 拿到了 alice 的 UA")
	}
	if a.userUA(bob) != a.cfg.DefaultUA {
		t.Fatalf("bob 应走内置默认 UA，实际 %q", a.userUA(bob))
	}

	if a.userSnapshot(bob).HasCookie {
		t.Fatal("bob 的快照里出现了别人的 cookie")
	}
	if !a.userSnapshot(alice).UploaderReady {
		t.Fatal("alice 有 cookie，上传器应可用")
	}
}
