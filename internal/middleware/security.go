package middleware

import (
	"net/http"
)

// SecurityMiddleware adds security headers, CORS, and limits incoming request body size.
func SecurityMiddleware(maxUploadBytes int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// Security Headers
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-XSS-Protection", "1; mode=block")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")

			// CORS
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			// Apply request body boundary limit to prevent DoS / Memory exhaustion
			if r.Body != nil && maxUploadBytes > 0 {
				r.Body = http.MaxBytesReader(w, r.Body, maxUploadBytes)
			}

			next.ServeHTTP(w, r)
		})
	}
}
