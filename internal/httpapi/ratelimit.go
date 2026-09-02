package httpapi

import (
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
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

type nodeRequestLimitGroup struct {
	byClient       *requestLimiter
	byPeer         *requestLimiter
	byCredential   *requestLimiter
	trustedProxies []netip.Prefix
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

func newNodeRequestLimitGroup(perClient, perPeer, perCredential int, trustedProxies []netip.Prefix) *nodeRequestLimitGroup {
	return &nodeRequestLimitGroup{
		byClient:       newRequestLimiter(perClient, time.Minute),
		byPeer:         newRequestLimiter(perPeer, time.Minute),
		byCredential:   newRequestLimiter(perCredential, time.Minute),
		trustedProxies: append([]netip.Prefix(nil), trustedProxies...),
	}
}

func (g *requestLimitGroup) allow(r *http.Request, machineID int64, now time.Time) bool {
	digest := sha256.Sum256([]byte(r.Header.Get("Authorization")))
	credentialKey := hex.EncodeToString(digest[:]) + ":" + strconv.FormatInt(machineID, 10)
	return g.byIP.take(requestIP(r), now) && g.byCredential.take(credentialKey, now)
}

func (g *nodeRequestLimitGroup) allow(r *http.Request, machineID int64, now time.Time) bool {
	client, peer := nodeRequestAddresses(r, g.trustedProxies)
	clientAllowed := g.byClient.take(client, now)
	peerAllowed := g.byPeer.take(peer, now)
	credentialAllowed := g.byCredential.take(nodeCredentialLimitKey(r, machineID), now)
	return clientAllowed && peerAllowed && credentialAllowed
}

func nodeCredentialLimitKey(r *http.Request, machineID int64) string {
	authorization := r.Header.Get("Authorization")
	parts := strings.Fields(authorization)
	if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") {
		authorization = parts[1]
	}
	digest := sha256.Sum256([]byte(authorization))
	return hex.EncodeToString(digest[:]) + ":" + strconv.FormatInt(machineID, 10)
}

const (
	maxNodeForwardedForBytes = 4 << 10
	maxNodeForwardedHops     = 32
)

func nodeRequestAddresses(r *http.Request, trustedProxies []netip.Prefix) (string, string) {
	peerAddress, ok := parseNodePeerAddress(r.RemoteAddr)
	if !ok {
		peer := requestIP(r)
		return peer, peer
	}
	peerAddress = peerAddress.Unmap().WithZone("")
	peer := peerAddress.String()
	if !nodeTrustedProxy(peerAddress, trustedProxies) {
		return peer, peer
	}
	values := r.Header.Values("X-Forwarded-For")
	if len(values) != 1 || len(values[0]) == 0 || len(values[0]) > maxNodeForwardedForBytes {
		return peer, peer
	}
	rawAddresses := strings.Split(values[0], ",")
	if len(rawAddresses) == 0 || len(rawAddresses) > maxNodeForwardedHops {
		return peer, peer
	}
	addresses := make([]netip.Addr, len(rawAddresses))
	for index, raw := range rawAddresses {
		address, err := netip.ParseAddr(strings.TrimSpace(raw))
		if err != nil || address.Zone() != "" {
			return peer, peer
		}
		addresses[index] = address.Unmap()
	}
	for index := len(addresses) - 1; index >= 0; index-- {
		if !nodeTrustedProxy(addresses[index], trustedProxies) {
			return addresses[index].String(), peer
		}
	}
	return addresses[0].String(), peer
}

func parseNodePeerAddress(value string) (netip.Addr, bool) {
	value = strings.TrimSpace(value)
	if addressPort, err := netip.ParseAddrPort(value); err == nil {
		return addressPort.Addr(), true
	}
	address, err := netip.ParseAddr(value)
	return address, err == nil
}

func nodeTrustedProxy(address netip.Addr, trustedProxies []netip.Prefix) bool {
	if !address.IsValid() {
		return false
	}
	address = address.Unmap().WithZone("")
	for _, prefix := range trustedProxies {
		if prefix.Contains(address) {
			return true
		}
	}
	return false
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
