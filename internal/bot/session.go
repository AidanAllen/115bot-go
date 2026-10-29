package bot

import (
	"sync"
	"time"
)

// Action 表示「等待用户下一条消息输入」的动作类型。
type Action string

const (
	ActionCookie      Action = "cookie"
	ActionUA          Action = "ua"
	ActionTransferCID Action = "transfercid"
	ActionOfflineCID  Action = "offlinecid"
	ActionSetID       Action = "setid"
)

// actionPrompt 返回该动作对应的输入提示语。
func actionPrompt(a Action) string {
	switch a {
	case ActionCookie:
		return TxtAskCookie
	case ActionUA:
		return TxtAskUA
	case ActionTransferCID:
		return TxtAskTransferCID
	case ActionOfflineCID:
		return TxtAskOfflineCID
	case ActionSetID:
		return TxtAskSetID
	default:
		return ""
	}
}

type sessionEntry struct {
	action Action
	expire time.Time
}

// sessionManager 管理「两步式输入」的会话状态。
// 按 chatID 记录，一次消费即失效，并带 TTL 防止状态残留。
type sessionManager struct {
	mu  sync.Mutex
	ttl time.Duration
	m   map[int64]sessionEntry
	now func() time.Time
}

func newSessionManager(ttl time.Duration) *sessionManager {
	if ttl <= 0 {
		ttl = 5 * time.Minute
	}
	return &sessionManager{
		ttl: ttl,
		m:   make(map[int64]sessionEntry),
		now: time.Now,
	}
}

// Set 记录一次待输入动作。
func (s *sessionManager) Set(chatID int64, a Action) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m[chatID] = sessionEntry{action: a, expire: s.now().Add(s.ttl)}
}

// Take 取出并消费待输入动作。
func (s *sessionManager) Take(chatID int64) (Action, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.m[chatID]
	if !ok {
		return "", false
	}
	delete(s.m, chatID)
	if s.now().After(e.expire) {
		return "", false
	}
	return e.action, true
}
