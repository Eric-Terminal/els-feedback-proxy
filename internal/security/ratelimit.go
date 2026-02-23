package security

import (
	"sync"
	"time"
)

type fixedWindowRecord struct {
	WindowStart time.Time
	Count       int
}

// FixedWindowLimiter 固定窗口限流器
type FixedWindowLimiter struct {
	mu      sync.Mutex
	records map[string]fixedWindowRecord
}

func NewFixedWindowLimiter() *FixedWindowLimiter {
	return &FixedWindowLimiter{records: make(map[string]fixedWindowRecord)}
}

func (l *FixedWindowLimiter) Allow(key string, limit int, window time.Duration) bool {
	if limit <= 0 {
		return false
	}

	now := time.Now()

	l.mu.Lock()
	defer l.mu.Unlock()

	record, exists := l.records[key]
	if !exists || now.Sub(record.WindowStart) >= window {
		l.records[key] = fixedWindowRecord{WindowStart: now, Count: 1}
		l.cleanupExpired(window)
		return true
	}

	if record.Count >= limit {
		return false
	}

	record.Count++
	l.records[key] = record
	return true
}

func (l *FixedWindowLimiter) cleanupExpired(window time.Duration) {
	now := time.Now()
	for key, record := range l.records {
		if now.Sub(record.WindowStart) >= window*2 {
			delete(l.records, key)
		}
	}
}
