package security

import (
	"sync"
	"time"
)

// DuplicateDetector 用于拦截短时间重复内容提交
type DuplicateDetector struct {
	mu      sync.Mutex
	records map[string]time.Time
}

func NewDuplicateDetector() *DuplicateDetector {
	return &DuplicateDetector{records: make(map[string]time.Time)}
}

// SeenRecently 在窗口内重复返回 true，否则记录并返回 false
func (d *DuplicateDetector) SeenRecently(key string, window time.Duration) bool {
	now := time.Now()

	d.mu.Lock()
	defer d.mu.Unlock()

	if expireAt, exists := d.records[key]; exists {
		if now.Before(expireAt) {
			return true
		}
	}

	d.records[key] = now.Add(window)
	for recordKey, expireAt := range d.records {
		if now.After(expireAt) {
			delete(d.records, recordKey)
		}
	}

	return false
}
