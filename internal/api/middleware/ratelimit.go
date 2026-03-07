package middleware

import (
	"net/http"
	"sync"
	"time"

	"flota/internal/core"
)

type visitor struct {
	count    int
	windowAt time.Time
	lastSeen time.Time
}

type RateLimiter struct {
	mu       sync.Mutex
	visitors map[string]*visitor
	limit    int
	window   time.Duration
}

func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		visitors: map[string]*visitor{},
		limit:    limit,
		window:   window,
	}
	go rl.cleanupEvery(time.Minute)
	return rl
}

func (rl *RateLimiter) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		clientID := Client(r.Context())
		if clientID == "" {
			clientID = r.RemoteAddr
		}

		if !rl.allow(clientID) {
			writeAuthError(w, r, core.ErrRateLimited, "too many requests")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (rl *RateLimiter) allow(clientID string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	v, ok := rl.visitors[clientID]
	if !ok {
		rl.visitors[clientID] = &visitor{
			count:    1,
			windowAt: time.Now(),
			lastSeen: time.Now(),
		}
		return true
	}
	v.lastSeen = time.Now()
	if time.Since(v.windowAt) > rl.window {
		v.windowAt = time.Now()
		v.count = 1
		return true
	}
	if v.count >= rl.limit {
		return false
	}
	v.count++
	return true
}

func (rl *RateLimiter) cleanupEvery(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for now := range ticker.C {
		rl.mu.Lock()
		for k, v := range rl.visitors {
			if now.Sub(v.lastSeen) > 3*time.Minute {
				delete(rl.visitors, k)
			}
		}
		rl.mu.Unlock()
	}
}

