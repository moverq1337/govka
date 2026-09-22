package api

import (
	"context"
	"sync"
	"time"
)

// limiter spaces requests evenly at rps per second. A token bucket with
// burst 1 is enough for VK: the limit is a plain per-second cap.
type limiter struct {
	mu       sync.Mutex
	interval time.Duration
	next     time.Time
	now      func() time.Time
	sleep    func(ctx context.Context, d time.Duration) error
}

func newLimiter(rps int) *limiter {
	return &limiter{
		interval: time.Second / time.Duration(rps),
		now:      time.Now,
		sleep:    sleep,
	}
}

func (l *limiter) wait(ctx context.Context) error {
	l.mu.Lock()
	now := l.now()
	if l.next.Before(now) {
		l.next = now
	}
	at := l.next
	l.next = at.Add(l.interval)
	l.mu.Unlock()
	if d := at.Sub(now); d > 0 {
		return l.sleep(ctx, d)
	}
	return nil
}
