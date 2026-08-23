package httpapi

import (
	"net"
	"net/http"
	"sync"
	"time"
)

type attemptLimiter struct {
	mu      sync.Mutex
	entries map[string]attemptEntry
	maximum int
	window  time.Duration
}

type attemptEntry struct {
	count   int
	resetAt time.Time
}

func newAttemptLimiter(maximum int, window time.Duration) *attemptLimiter {
	return &attemptLimiter{entries: make(map[string]attemptEntry), maximum: maximum, window: window}
}

func (l *attemptLimiter) allowed(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.removeExpired(now)
	entry, exists := l.entries[key]
	if !exists && len(l.entries) >= 4096 {
		key = "__overflow__"
		entry, exists = l.entries[key]
	}
	if !exists || !now.Before(entry.resetAt) {
		return true
	}
	return entry.count < l.maximum
}

func (l *attemptLimiter) failed(key string, now time.Time) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.removeExpired(now)
	entry, exists := l.entries[key]
	if !exists && len(l.entries) >= 4096 {
		key = "__overflow__"
		entry, exists = l.entries[key]
	}
	if !exists || !now.Before(entry.resetAt) {
		entry = attemptEntry{resetAt: now.Add(l.window)}
	}
	entry.count++
	l.entries[key] = entry
}

func (l *attemptLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.entries, key)
	if _, exists := l.entries[key]; !exists && len(l.entries) >= 4096 {
		delete(l.entries, "__overflow__")
	}
	l.mu.Unlock()
}

func (l *attemptLimiter) removeExpired(now time.Time) {
	for key, entry := range l.entries {
		if !now.Before(entry.resetAt) {
			delete(l.entries, key)
		}
	}
}

func requestIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err == nil {
		return host
	}
	return r.RemoteAddr
}
