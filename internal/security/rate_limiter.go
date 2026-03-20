package security

import (
	"sync"
	"time"
)

type RateLimiter struct {
	mu            sync.Mutex
	lastExecution time.Time
	cooldown      time.Duration
}

func NewRateLimiter(seconds int) *RateLimiter {
	return &RateLimiter{
		cooldown: time.Duration(seconds) * time.Second,
	}
}

func (r *RateLimiter) Allow() bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	if time.Since(r.lastExecution) < r.cooldown {
		return false
	}

	r.lastExecution = time.Now()
	return true
}
