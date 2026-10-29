package store

import (
	"database/sql"
	"errors"
	"strconv"
)

// 配置键常量。集中定义，避免各处硬编码字符串。
const (
	KeyBotEnabled = "bot.enabled"
	// KeyAutoTransfer 是「自动转存」开关：开启后发给机器人的每个文件自动进 115，
	// 不必逐条回复 /d。按用户独立存储。
	KeyAutoTransfer = "bot.autoTransfer"
	// KeyBotOwnerID 是机器人所有者：不是「唯一能用的人」，
	// 而是后续 web 控制台 / 用户管理的凭证。
	KeyBotOwnerID = "bot.ownerId"
	// KeyBotAdminID 是旧版单管理员模型的键，仅用于一次性迁移到 ownerId。
	KeyBotAdminID = "bot.adminId"
	// KeySetupCode 是首次绑定 owner 的一次性口令，绑定成功后清除。
	KeySetupCode     = "bot.setupCode"
	KeyCookie        = "account.cookie"
	KeyUserAgent     = "account.ua"
	KeyUploadChannel = "account.channel" // 转存通道（上传时使用的客户端身份）
	KeyOfflineCID    = "account.offlineCid"
	KeyTransferCID   = "account.transferCid"
	KeyAccountStatus = "account.status"   // ok / invalid / unchecked
	KeyAccountNick   = "account.nickname" // 115 昵称
	KeyCheckedAt     = "account.checkedAt"
)

// SetConfig 写入配置（存在则覆盖）。
func (s *Store) SetConfig(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO app_config(key, value, updated_at) VALUES(?,?,?)
		 ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		key, value, now())
	return err
}

// GetConfig 读取配置，不存在时返回空字符串与 nil。
func (s *Store) GetConfig(key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM app_config WHERE key=?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// GetConfigDefault 读取配置，不存在时返回默认值。
func (s *Store) GetConfigDefault(key, def string) string {
	v, err := s.GetConfig(key)
	if err != nil || v == "" {
		return def
	}
	return v
}

// GetBool 读取布尔配置。
func (s *Store) GetBool(key string, def bool) bool {
	v, err := s.GetConfig(key)
	if err != nil || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// SetBool 写入布尔配置。
func (s *Store) SetBool(key string, v bool) error {
	return s.SetConfig(key, strconv.FormatBool(v))
}

// GetInt64 读取整数配置。
func (s *Store) GetInt64(key string, def int64) int64 {
	v, err := s.GetConfig(key)
	if err != nil || v == "" {
		return def
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return def
	}
	return n
}

// SetInt64 写入整数配置。
func (s *Store) SetInt64(key string, v int64) error {
	return s.SetConfig(key, strconv.FormatInt(v, 10))
}

// ---- 用户级配置 ----
//
// 每个 Telegram 用户有自己的 115 账号：cookie、UA、通道、落盘目录都不共享。
// 所以这些键必须带 user_id 维度，不能塞进全局 app_config——
// 否则后登录的人会覆盖先登录的人的凭据，等于把 A 的文件传到 B 的网盘。

// SetUserConfig 写入某个用户的配置。
func (s *Store) SetUserConfig(userID int64, key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO user_config(user_id, key, value, updated_at) VALUES(?,?,?,?)
		 ON CONFLICT(user_id, key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at`,
		userID, key, value, now())
	return err
}

// GetUserConfig 读取某个用户的配置，不存在返回空串。
func (s *Store) GetUserConfig(userID int64, key string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM user_config WHERE user_id=? AND key=?`, userID, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return v, err
}

// GetUserConfigDefault 读取用户配置，不存在返回默认值。
func (s *Store) GetUserConfigDefault(userID int64, key, def string) string {
	v, err := s.GetUserConfig(userID, key)
	if err != nil || v == "" {
		return def
	}
	return v
}

// GetUserBool 读取用户的布尔配置。
func (s *Store) GetUserBool(userID int64, key string, def bool) bool {
	v, err := s.GetUserConfig(userID, key)
	if err != nil || v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}

// SetUserBool 写入用户的布尔配置。
func (s *Store) SetUserBool(userID int64, key string, v bool) error {
	return s.SetUserConfig(userID, key, strconv.FormatBool(v))
}

// HasUserConfig 判断该用户是否写入过这个键（有值才算）。
func (s *Store) HasUserConfig(userID int64, key string) bool {
	return s.GetUserConfigDefault(userID, key, "") != ""
}

// legacyAccountKeys 是单用户时期存在全局 app_config 里的键。
//
// 多用户之后它们必须带 user_id，否则后登录的人会覆盖先登录的人的凭据。
var legacyAccountKeys = []string{
	KeyCookie, KeyUserAgent, KeyUploadChannel,
	KeyTransferCID, KeyOfflineCID,
	KeyAccountStatus, KeyAccountNick, KeyCheckedAt,
	KeyBotEnabled,
}

// MigrateLegacyGlobalAccount 把全局旧配置迁到 owner 名下，返回迁移的键数。
//
// 幂等：目标用户已写过该键就不动它，避免覆盖用户迁移之后自己改过的值。
// 迁完删除全局键——同一份 cookie 不该在两个地方各留一份。
func (s *Store) MigrateLegacyGlobalAccount(owner int64) (int, error) {
	if owner <= 0 {
		return 0, nil
	}
	moved := 0
	for _, key := range legacyAccountKeys {
		old, err := s.GetConfig(key)
		if err != nil {
			return moved, err
		}
		if old == "" {
			continue
		}
		if cur, err := s.GetUserConfig(owner, key); err != nil {
			return moved, err
		} else if cur != "" {
			// 用户已经在新结构里写过，旧值不再有资格覆盖
			if err := s.deleteConfig(key); err != nil {
				return moved, err
			}
			continue
		}
		if err := s.SetUserConfig(owner, key, old); err != nil {
			return moved, err
		}
		if err := s.deleteConfig(key); err != nil {
			return moved, err
		}
		moved++
	}
	return moved, nil
}

func (s *Store) deleteConfig(key string) error {
	_, err := s.db.Exec(`DELETE FROM app_config WHERE key=?`, key)
	return err
}

// ListUserIDs 列出配置过指定键的全部用户 id，用于迁移与后台管理。
func (s *Store) ListUserIDs(key string) ([]int64, error) {
	rows, err := s.db.Query(`SELECT user_id FROM user_config WHERE key=? ORDER BY user_id`, key)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
