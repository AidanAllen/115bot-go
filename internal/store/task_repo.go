package store

import "time"

// 任务类型。
const (
	KindOffline  = "offline"  // 磁力 / ed2k 离线下载
	KindTransfer = "transfer" // Telegram 文件转存到 115
)

// 任务状态。
const (
	StatusPending = "pending"
	StatusRunning = "running"
	StatusSuccess = "success"
	StatusFailed  = "failed"
)

// Task 一条任务记录。
type Task struct {
	ID        int64
	Kind      string
	Source    string // 磁力链 / ed2k / TG file_id
	Name      string
	Status    string
	CID       string
	Err       string
	CreatedAt time.Time
	UpdatedAt time.Time
}

// CreateTask 新建任务并返回自增 ID。任务归属 userID，统计与查询都按用户隔离。
func (s *Store) CreateTask(userID int64, kind, source, name, cid string) (int64, error) {
	ts := now()
	res, err := s.db.Exec(
		`INSERT INTO task(user_id, kind, source, name, status, cid, err, created_at, updated_at)
		 VALUES(?,?,?,?,?,?,'',?,?)`,
		userID, kind, source, name, StatusPending, cid, ts, ts)
	if err != nil {
		return 0, err
	}
	return res.LastInsertId()
}

// UpdateTask 更新任务状态与错误信息。
func (s *Store) UpdateTask(id int64, status, errMsg string) error {
	_, err := s.db.Exec(
		`UPDATE task SET status=?, err=?, updated_at=? WHERE id=?`,
		status, errMsg, now(), id)
	return err
}

// CountByStatus 统计某个用户、指定类型的各状态数量。
func (s *Store) CountByStatus(userID int64, kind string) (map[string]int, error) {
	rows, err := s.db.Query(`SELECT status, COUNT(*) FROM task WHERE user_id=? AND kind=? GROUP BY status`, userID, kind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := map[string]int{
		StatusPending: 0,
		StatusRunning: 0,
		StatusSuccess: 0,
		StatusFailed:  0,
	}
	for rows.Next() {
		var st string
		var n int
		if err := rows.Scan(&st, &n); err != nil {
			return nil, err
		}
		out[st] = n
	}
	return out, rows.Err()
}

// CountAll 统计某个用户的全部任务数。
func (s *Store) CountAll(userID int64) (int, error) {
	var n int
	err := s.db.QueryRow(`SELECT COUNT(*) FROM task WHERE user_id=?`, userID).Scan(&n)
	return n, err
}

// TaskNames 取某用户在指定目录里用过的文件名（去重，最多 limit 个）。
//
// 用途：给重名文件自动加 (1)(2) 后缀。同名文件传上去会被网盘自行改名或覆盖，
// 用户回头找不到「我发的那个名字」，所以宁可自己先排开。
func (s *Store) TaskNames(userID int64, cid string, limit int) ([]string, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := s.db.Query(
		`SELECT DISTINCT name FROM task WHERE user_id=? AND cid=? AND name<>'' LIMIT ?`,
		userID, cid, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

// RecentTasks 取某个用户最近 N 条任务。
func (s *Store) RecentTasks(userID int64, limit int) ([]Task, error) {
	if limit <= 0 {
		limit = 5
	}
	rows, err := s.db.Query(
		`SELECT id, kind, source, name, status, cid, err, created_at, updated_at
		 FROM task WHERE user_id=? ORDER BY id DESC LIMIT ?`, userID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Task
	for rows.Next() {
		var t Task
		var created, updated string
		if err := rows.Scan(&t.ID, &t.Kind, &t.Source, &t.Name, &t.Status, &t.CID, &t.Err, &created, &updated); err != nil {
			return nil, err
		}
		t.CreatedAt, _ = time.Parse(time.RFC3339, created)
		t.UpdatedAt, _ = time.Parse(time.RFC3339, updated)
		out = append(out, t)
	}
	return out, rows.Err()
}
