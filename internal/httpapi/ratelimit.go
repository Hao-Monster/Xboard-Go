package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strconv"
	"sync"
	"time"
)

type requestLimiter struct {
	mu      sync.Mutex
	entries map[string]attemptEntry
	maximum int
	window  time.Duration
	nextGC  time.Time
}

type requestLimitGroup struct {
	byIP         *requestLimiter
	byCredential *requestLimiter
}

func newRequestLimiter(maximum int, window time.Duration) *requestLimiter {
	return &requestLimiter{entries: make(map[string]attemptEntry), maximum: maximum, window: window}
}

func newRequestLimitGroup(perIP, perCredential int) *requestLimitGroup {
	return &requestLimitGroup{
		byIP:         &requestLimiter{entries: make(map[string]attemptEntry), maximum: perIP, window: time.Minute},
		byCredential: &requestLimiter{entries: make(map[string]attemptEntry), maximum: perCredential, window: time.Minute},
	}
}

func (g *requestLimitGroup) allow(r *http.Request, machineID int64, now time.Time) bool {
	digest := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	credentialKey := hex.EncodeToString(digest[:]) + ":" + strconv.FormatInt(machineID, 10)
	return g.byIP.take(requestIP(r), now) && g.byCredential.take(credentialKey, now)
}

func (l *requestLimiter) take(key string, now time.Time) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.nextGC.IsZero() || !now.Before(l.nextGC) {
		for entryKey, entry := range l.entries {
			if !now.Before(entry.resetAt) {
				delete(l.entries, entryKey)
			}
		}
		l.nextGC = now.Add(l.window)
	}
	entry, exists := l.entries[key]
	if !exists && len(l.entries) >= 4096 {
		key = "__overflow__"
		entry, exists = l.entries[key]
	}
	if !exists || !now.Before(entry.resetAt) {
		entry = attemptEntry{resetAt: now.Add(l.window)}
	}
	if entry.count >= l.maximum {
		return false
	}
	entry.count++
	l.entries[key] = entry
	return true
}

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
		if address := net.ParseIP(host); address != nil {
			return address.String()
		}
		return host
	}
	if address := net.ParseIP(r.RemoteAddr); address != nil {
		return address.String()
	}
	return r.RemoteAddr
}
