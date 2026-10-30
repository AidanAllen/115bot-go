package store

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "test.db"))
	if err != nil {
		t.Fatalf("打开数据库失败: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestConfigRoundTrip(t *testing.T) {
	s := newTestStore(t)

	// 未设置时返回空与默认值
	if v, err := s.GetConfig(KeyCookie); err != nil || v != "" {
		t.Fatalf("未设置时期望空值，实际 %q err=%v", v, err)
	}
	if v := s.GetConfigDefault(KeyUserAgent, "def-ua"); v != "def-ua" {
		t.Fatalf("期望默认值 def-ua，实际 %q", v)
	}

	// 写入
	if err := s.SetConfig(KeyCookie, "USERSESSIONID=abc"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetConfig(KeyCookie); v != "USERSESSIONID=abc" {
		t.Fatalf("读取不一致：%q", v)
	}

	// 覆盖写
	if err := s.SetConfig(KeyCookie, "USERSESSIONID=xyz"); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.GetConfig(KeyCookie); v != "USERSESSIONID=xyz" {
		t.Fatalf("覆盖后读取不一致：%q", v)
	}

	// 布尔与整数
	if err := s.SetBool(KeyBotEnabled, false); err != nil {
		t.Fatal(err)
	}
	if s.GetBool(KeyBotEnabled, true) {
		t.Fatal("布尔值写入失败")
	}
	if err := s.SetInt64(KeyBotOwnerID, 12345); err != nil {
		t.Fatal(err)
	}
	if got := s.GetInt64(KeyBotOwnerID, 0); got != 12345 {
		t.Fatalf("整数写入失败：%d", got)
	}
}

// TestUserConfigIsolated 同一个键在不同用户之间必须互不影响。
//
// 这是多用户模型的地基：一旦串了，就等于把 A 的 cookie 拿去操作 B 的网盘。
func TestUserConfigIsolated(t *testing.T) {
	s := newTestStore(t)
	const alice, bob = int64(111), int64(222)

	if err := s.SetUserConfig(alice, KeyCookie, "UID=alice"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetUserConfig(bob, KeyCookie, "UID=bob"); err != nil {
		t.Fatal(err)
	}
	if got := s.GetUserConfigDefault(alice, KeyCookie, ""); got != "UID=alice" {
		t.Fatalf("alice 的 cookie 被覆盖：%q", got)
	}
	if got := s.GetUserConfigDefault(bob, KeyCookie, ""); got != "UID=bob" {
		t.Fatalf("bob 的 cookie 不对：%q", got)
	}
	// 没配过的用户应拿到默认值，而不是别人的值
	if got := s.GetUserConfigDefault(999, KeyCookie, ""); got != "" {
		t.Fatalf("未配置用户不应读到别人的值：%q", got)
	}
	// 用户级开关独立
	if err := s.SetUserBool(alice, KeyBotEnabled, false); err != nil {
		t.Fatal(err)
	}
	if s.GetUserBool(alice, KeyBotEnabled, true) {
		t.Fatal("alice 的开关没关掉")
	}
	if !s.GetUserBool(bob, KeyBotEnabled, true) {
		t.Fatal("bob 的开关被 alice 连坐")
	}

	ids, err := s.ListUserIDs(KeyCookie)
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != alice || ids[1] != bob {
		t.Fatalf("用户列表不符：%v", ids)
	}
}

// TestOpenLegacyDBAddsUserID 旧库（task 表没有 user_id）必须能直接打开。
//
// 曾经把 idx_task_user 写进建表脚本，而旧表的 user_id 是迁移时才补的，
// 结果建索引先于加列执行，线上直接启动失败、容器重启循环。
func TestOpenLegacyDBAddsUserID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "legacy.db")

	// 造一个旧版本库：只有 app_config 与旧结构的 task
	old, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE app_config (key TEXT PRIMARY KEY, value TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`CREATE TABLE task (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		kind TEXT NOT NULL, source TEXT NOT NULL DEFAULT '', name TEXT NOT NULL DEFAULT '',
		status TEXT NOT NULL, cid TEXT NOT NULL DEFAULT '', err TEXT NOT NULL DEFAULT '',
		created_at TEXT NOT NULL, updated_at TEXT NOT NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO task(kind, source, name, status, cid, err, created_at, updated_at)
		VALUES('offline','magnet:?xt=a','','success','9','','2026-01-01T00:00:00Z','2026-01-01T00:00:00Z')`); err != nil {
		t.Fatal(err)
	}
	_ = old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("旧库打不开：%v", err)
	}
	defer func() { _ = s.Close() }()

	// 老任务仍在（user_id 为 0），且新写入带 user_id 的任务要能统计
	if total, _ := s.CountAll(12345); total != 0 {
		t.Fatalf("历史任务不应归到某个用户下，实际 %d", total)
	}
	if _, err := s.CreateTask(12345, KindOffline, "magnet:?xt=b", "", "9"); err != nil {
		t.Fatalf("迁移后写入失败：%v", err)
	}
	if total, _ := s.CountAll(12345); total != 1 {
		t.Fatalf("新任务应可统计，实际 %d", total)
	}
	// 用户表也要建出来
	if err := s.SetUserConfig(12345, KeyCookie, "UID=1"); err != nil {
		t.Fatalf("user_config 不可用：%v", err)
	}
}

// TestMigrateLegacyGlobalAccount 老库的全局 115 配置要迁到 owner 名下，
// 而且不能覆盖 owner 迁移之后自己改过的值。
func TestMigrateLegacyGlobalAccount(t *testing.T) {
	s := newTestStore(t)
	const owner = int64(5251918892)

	if err := s.SetConfig(KeyCookie, "UID=old"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConfig(KeyTransferCID, "3550559550308353647"); err != nil {
		t.Fatal(err)
	}
	// 用户已经在新的结构里配了自己的转存目录，旧值不该覆盖它
	if err := s.SetUserConfig(owner, KeyTransferCID, "111"); err != nil {
		t.Fatal(err)
	}

	n, err := s.MigrateLegacyGlobalAccount(owner)
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应迁移 1 个键（转存目录被跳过），实际 %d", n)
	}
	if got := s.GetUserConfigDefault(owner, KeyCookie, ""); got != "UID=old" {
		t.Fatalf("cookie 没迁过去：%q", got)
	}
	if got := s.GetUserConfigDefault(owner, KeyTransferCID, ""); got != "111" {
		t.Fatalf("旧值覆盖了用户自己的配置：%q", got)
	}
	// 全局键要删掉，同一份 cookie 不能留两个地方
	if got := s.GetConfigDefault(KeyCookie, ""); got != "" {
		t.Fatalf("全局 cookie 未清理：%q", got)
	}

	// 幂等：再跑一次不应报错，也不该把刚迁过去的值又搬回来
	if n2, err := s.MigrateLegacyGlobalAccount(owner); err != nil || n2 != 0 {
		t.Fatalf("重复迁移应为空操作，实际 n=%d err=%v", n2, err)
	}

	// 没有 owner 时不能瞎迁，否则凭据会变成无主数据
	if n3, err := s.MigrateLegacyGlobalAccount(0); err != nil || n3 != 0 {
		t.Fatalf("owner 为 0 时不应迁移，实际 n=%d err=%v", n3, err)
	}
}

func TestTaskLifecycle(t *testing.T) {
	s := newTestStore(t)
	const me, other = int64(111), int64(222)

	id, err := s.CreateTask(me, KindOffline, "magnet:?xt=urn:btih:aaa", "", "123")
	if err != nil {
		t.Fatal(err)
	}
	if id <= 0 {
		t.Fatalf("自增 id 异常：%d", id)
	}

	counts, err := s.CountByStatus(me, KindOffline)
	if err != nil {
		t.Fatal(err)
	}
	if counts[StatusPending] != 1 {
		t.Fatalf("待处理数期望 1，实际 %d", counts[StatusPending])
	}

	if err := s.UpdateTask(id, StatusSuccess, ""); err != nil {
		t.Fatal(err)
	}
	counts, _ = s.CountByStatus(me, KindOffline)
	if counts[StatusSuccess] != 1 || counts[StatusPending] != 0 {
		t.Fatalf("状态更新后统计不符：%+v", counts)
	}

	// 不同类型的任务互不影响
	if _, err := s.CreateTask(me, KindTransfer, "fileid", "a.mp4", "456"); err != nil {
		t.Fatal(err)
	}
	off, _ := s.CountByStatus(me, KindOffline)
	tf, _ := s.CountByStatus(me, KindTransfer)
	if off[StatusSuccess] != 1 || tf[StatusPending] != 1 {
		t.Fatalf("跨类型统计被污染：offline=%+v transfer=%+v", off, tf)
	}

	total, err := s.CountAll(me)
	if err != nil || total != 2 {
		t.Fatalf("总数期望 2，实际 %d err=%v", total, err)
	}

	recent, err := s.RecentTasks(me, 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(recent) != 2 || recent[0].Kind != KindTransfer {
		t.Fatalf("最近任务排序不符：%+v", recent)
	}
}

// TestTasksArePerUser 别人的任务不能出现在我的统计里。
func TestTasksArePerUser(t *testing.T) {
	s := newTestStore(t)
	const me, other = int64(111), int64(222)

	if _, err := s.CreateTask(other, KindOffline, "magnet:?xt=urn:btih:other", "", "9"); err != nil {
		t.Fatal(err)
	}
	if total, _ := s.CountAll(me); total != 0 {
		t.Fatalf("我不该看到别人的任务，实际 %d", total)
	}
	if recent, _ := s.RecentTasks(me, 5); len(recent) != 0 {
		t.Fatalf("最近任务串号：%+v", recent)
	}
	if _, err := s.CreateTask(me, KindOffline, "magnet:?xt=urn:btih:mine", "", "9"); err != nil {
		t.Fatal(err)
	}
	if total, _ := s.CountAll(me); total != 1 {
		t.Fatalf("自己的任务应可见，实际 %d", total)
	}
	if total, _ := s.CountAll(other); total != 1 {
		t.Fatalf("别人的统计被影响，实际 %d", total)
	}
}

// TestFailStaleRunning 进程被杀后残留的 running 任务应在启动时收敛为 failed。
//
// 否则用户会看到一条永远「进行中」的记录（2026-10-30 线上就出现过：
// 发版重建容器时正在跑的任务停在 running）。
func TestFailStaleRunning(t *testing.T) {
	s := newTestStore(t)
	me := int64(333)

	runningID, err := s.CreateTask(me, KindTransfer, "fid-1", "a.mp4", "1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTask(runningID, StatusRunning, ""); err != nil {
		t.Fatal(err)
	}
	doneID, err := s.CreateTask(me, KindTransfer, "fid-2", "b.mp4", "1")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateTask(doneID, StatusSuccess, ""); err != nil {
		t.Fatal(err)
	}

	n, err := s.FailStaleRunning("服务重启，任务已中断")
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("应只收敛 1 条 running 任务，实际 %d", n)
	}

	counts, err := s.CountByStatus(me, KindTransfer)
	if err != nil {
		t.Fatal(err)
	}
	if counts[StatusRunning] != 0 {
		t.Fatalf("不应再有 running：%+v", counts)
	}
	if counts[StatusFailed] != 1 || counts[StatusSuccess] != 1 {
		t.Fatalf("已有的成功记录被误改：%+v", counts)
	}

	recent, err := s.RecentTasks(me, 10)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range recent {
		if r.ID == runningID && r.Err != "服务重启，任务已中断" {
			t.Fatalf("失败原因未写入：%q", r.Err)
		}
	}
}
