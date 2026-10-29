package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"time"

	_ "modernc.org/sqlite"
)

// Store 封装 SQLite 数据访问。
type Store struct {
	db *sql.DB
}

const schema = `
CREATE TABLE IF NOT EXISTS app_config (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- 用户级配置：每人一份 115 凭据与落盘目录，互不覆盖
CREATE TABLE IF NOT EXISTS user_config (
    user_id    INTEGER NOT NULL,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    PRIMARY KEY (user_id, key)
);

CREATE TABLE IF NOT EXISTS task (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    INTEGER NOT NULL DEFAULT 0,
    kind       TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT '',
    name       TEXT NOT NULL DEFAULT '',
    status     TEXT NOT NULL,
    cid        TEXT NOT NULL DEFAULT '',
    err        TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

-- idx_task_user 不能写在这里：旧库的 task 还没有 user_id 列，
-- 建索引会直接报 no such column，整个启动就挂了。索引在迁移补列之后再建。
CREATE INDEX IF NOT EXISTS idx_task_status  ON task(status);
CREATE INDEX IF NOT EXISTS idx_task_created ON task(created_at);
`

// Open 打开（必要时创建）数据库并完成建表。
func Open(path string) (*Store, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("创建数据库目录失败: %w", err)
		}
	}
	dsn := path + "?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	// SQLite 单写者模型：限制连接数，避免 SQLITE_BUSY。
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("初始化表结构失败: %w", err)
	}

	st := &Store{db: db}
	if err := st.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return st, nil
}

// migrate 补齐旧库缺少的列。
//
// user_id 是后来加的：早期只有一个人用，配置与任务都没有用户维度。
// 直接 ALTER 加列并默认 0（0 视为「迁移前的历史数据」），
// 老任务仍能查得到，不会因为是旧库就打不开。
func (s *Store) migrate() error {
	has, err := s.hasColumn("task", "user_id")
	if err != nil {
		return err
	}
	if !has {
		if _, err := s.db.Exec(`ALTER TABLE task ADD COLUMN user_id INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("升级 task 表失败: %w", err)
		}
	}
	if _, err := s.db.Exec(`CREATE INDEX IF NOT EXISTS idx_task_user ON task(user_id)`); err != nil {
		return fmt.Errorf("建立 task 用户索引失败: %w", err)
	}
	return nil
}

// hasColumn 判断表里是否已有该列。
func (s *Store) hasColumn(table, column string) (bool, error) {
	rows, err := s.db.Query(fmt.Sprintf("PRAGMA table_info(%s)", table))
	if err != nil {
		return false, err
	}
	defer rows.Close()

	for rows.Next() {
		var cid int
		var name, ctype string
		var notNull, pk int
		var def sql.NullString
		if err := rows.Scan(&cid, &name, &ctype, &notNull, &def, &pk); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	return false, rows.Err()
}

// Close 关闭数据库。
func (s *Store) Close() error { return s.db.Close() }

func now() string { return time.Now().Format(time.RFC3339) }
