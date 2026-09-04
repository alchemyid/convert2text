package handler

import (
	"net/http"
	"runtime"
	"time"

	"convert2text/internal/middleware"
)

var appStartTime = time.Now()

// HealthHandler returns runtime compute, memory, and application health metrics.
func HealthHandler(limiter *middleware.ConcurrencyLimiter) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var m runtime.MemStats
		runtime.ReadMemStats(&m)

		activeWorkers, maxWorkers := limiter.Stats()

		stats := map[string]interface{}{
			"status": "healthy",
			"uptime": time.Since(appStartTime).String(),
			"compute": map[string]interface{}{
				"num_cpu":        runtime.NumCPU(),
				"num_goroutines": runtime.NumGoroutine(),
				"active_workers": activeWorkers,
				"max_workers":    maxWorkers,
			},
			"memory": map[string]interface{}{
				"alloc_mb":       m.Alloc / 1024 / 1024,
				"total_alloc_mb": m.TotalAlloc / 1024 / 1024,
				"sys_mb":         m.Sys / 1024 / 1024,
				"num_gc":         m.NumGC,
			},
		}

		JSONSuccess(w, http.StatusOK, stats)
	}
}
