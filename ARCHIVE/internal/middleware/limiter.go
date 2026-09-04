package middleware

import (
	"encoding/json"
	"net/http"
	"sync/atomic"
	"time"
)

// ConcurrencyLimiter controls the maximum simultaneous compute-intensive extractions.
type ConcurrencyLimiter struct {
	sem         chan struct{}
	activeCount int64
	maxSlots    int
}

// NewConcurrencyLimiter creates a new limiter with the given slot capacity.
func NewConcurrencyLimiter(maxConcurrent int) *ConcurrencyLimiter {
	if maxConcurrent <= 0 {
		maxConcurrent = 4
	}
	return &ConcurrencyLimiter{
		sem:      make(chan struct{}, maxConcurrent),
		maxSlots: maxConcurrent,
	}
}

// Limit limits handler execution, rejecting with 503 if all slots are occupied after wait timeout.
func (l *ConcurrencyLimiter) Limit(waitTimeout time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			select {
			case l.sem <- struct{}{}:
				atomic.AddInt64(&l.activeCount, 1)
				defer func() {
					atomic.AddInt64(&l.activeCount, -1)
					<-l.sem
				}()
				next.ServeHTTP(w, r)
			case <-time.After(waitTimeout):
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusServiceUnavailable)
				json.NewEncoder(w).Encode(map[string]interface{}{
					"success": false,
					"error":   "Server compute busy. Maximum concurrent extractions reached, please retry in a moment.",
				})
			}
		})
	}
}

// Stats returns the active and maximum concurrent extraction slots.
func (l *ConcurrencyLimiter) Stats() (active int64, max int) {
	return atomic.LoadInt64(&l.activeCount), l.maxSlots
}
