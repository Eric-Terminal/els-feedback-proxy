package guide

import (
	"errors"
	"strings"
	"sync"
)

var (
	ErrSessionBusy = errors.New("这个向导会话已有请求正在生成")
	ErrIPBusy      = errors.New("当前网络的向导并发请求过多")
)

// ConcurrencyLimiter 只记录正在运行的请求；取消或完成后立即释放，不形成用户画像。
type ConcurrencyLimiter struct {
	mu           sync.Mutex
	perIP        map[string]int
	perSession   map[string]int
	ipLimit      int
	sessionLimit int
}

func NewConcurrencyLimiter(ipLimit int) *ConcurrencyLimiter {
	if ipLimit < 1 {
		ipLimit = 1
	}
	return &ConcurrencyLimiter{
		perIP:        make(map[string]int),
		perSession:   make(map[string]int),
		ipLimit:      ipLimit,
		sessionLimit: 1,
	}
}

func (l *ConcurrencyLimiter) Acquire(clientIP, sessionID string) (func(), error) {
	clientIP = strings.TrimSpace(clientIP)
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, ErrSessionBusy
	}

	l.mu.Lock()
	if l.perSession[sessionID] >= l.sessionLimit {
		l.mu.Unlock()
		return nil, ErrSessionBusy
	}
	if l.perIP[clientIP] >= l.ipLimit {
		l.mu.Unlock()
		return nil, ErrIPBusy
	}
	l.perSession[sessionID]++
	l.perIP[clientIP]++
	l.mu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			defer l.mu.Unlock()
			l.perSession[sessionID]--
			if l.perSession[sessionID] == 0 {
				delete(l.perSession, sessionID)
			}
			l.perIP[clientIP]--
			if l.perIP[clientIP] == 0 {
				delete(l.perIP, clientIP)
			}
		})
	}, nil
}
