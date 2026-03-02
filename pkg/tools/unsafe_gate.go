package tools

import (
	"strings"
	"sync"
	"time"
)

// UnsafeToolGate tracks per-session approvals for running unsafe_* tools.
//
// This is intended to provide a lightweight "human-in-the-loop" mechanism:
// unsafe tools are blocked by default, and become available only after the user
// explicitly approves them (typically for a short TTL).
type UnsafeToolGate struct {
	mu         sync.RWMutex
	approvals  map[string]time.Time // sessionKey -> expiry
	defaultTTL time.Duration
}

func NewUnsafeToolGate(defaultTTL time.Duration) *UnsafeToolGate {
	if defaultTTL <= 0 {
		defaultTTL = 10 * time.Minute
	}
	return &UnsafeToolGate{
		approvals:  make(map[string]time.Time),
		defaultTTL: defaultTTL,
	}
}

func (g *UnsafeToolGate) Approve(sessionKey string, ttl time.Duration) time.Duration {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return 0
	}
	if ttl <= 0 {
		ttl = g.defaultTTL
	}

	expiresAt := time.Now().Add(ttl)
	g.mu.Lock()
	if g.approvals == nil {
		g.approvals = make(map[string]time.Time)
	}
	g.approvals[sessionKey] = expiresAt
	g.mu.Unlock()
	return ttl
}

func (g *UnsafeToolGate) Revoke(sessionKey string) {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return
	}

	g.mu.Lock()
	delete(g.approvals, sessionKey)
	g.mu.Unlock()
}

func (g *UnsafeToolGate) IsApproved(sessionKey string) bool {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return false
	}

	now := time.Now()
	g.mu.RLock()
	expiresAt, ok := g.approvals[sessionKey]
	g.mu.RUnlock()
	if !ok {
		return false
	}
	if !expiresAt.IsZero() && now.Before(expiresAt) {
		return true
	}

	// Expired: best-effort cleanup.
	g.mu.Lock()
	if cur, ok := g.approvals[sessionKey]; ok {
		if cur.IsZero() || !now.Before(cur) {
			delete(g.approvals, sessionKey)
		}
	}
	g.mu.Unlock()

	return false
}

func (g *UnsafeToolGate) Remaining(sessionKey string) time.Duration {
	sessionKey = strings.TrimSpace(sessionKey)
	if sessionKey == "" {
		return 0
	}

	now := time.Now()
	g.mu.RLock()
	expiresAt, ok := g.approvals[sessionKey]
	g.mu.RUnlock()
	if !ok {
		return 0
	}
	if expiresAt.IsZero() {
		return 0
	}
	if now.After(expiresAt) {
		return 0
	}
	return time.Until(expiresAt)
}
