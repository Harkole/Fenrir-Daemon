package api

import (
	"log"
	"net/http"
	"strings"
	"time"
)

// responseWriterInterceptor intercepts HTTP status codes for logging.
type responseWriterInterceptor struct {
	http.ResponseWriter
	statusCode int
}

func (w *responseWriterInterceptor) WriteHeader(code int) {
	w.statusCode = code
	w.ResponseWriter.WriteHeader(code)
}

// LoggingMiddleware logs incoming HTTP requests with latency and response status code.
func LoggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		interceptor := &responseWriterInterceptor{
			ResponseWriter: w,
			statusCode:     http.StatusOK,
		}

		next.ServeHTTP(interceptor, r)

		log.Printf("[HTTP] %s %s | %d | %s",
			r.Method,
			r.URL.Path,
			interceptor.statusCode,
			time.Since(start).Round(time.Microsecond),
		)
	})
}

// CORSMiddleware adds Cross-Origin headers according to configured origins.
func CORSMiddleware(allowedOrigins []string) func(http.Handler) http.Handler {
	originsMap := make(map[string]bool)
	allowAll := false
	for _, o := range allowedOrigins {
		trimmed := strings.TrimSpace(o)
		if trimmed == "*" {
			allowAll = true
			break
		}
		originsMap[trimmed] = true
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			if allowAll {
				w.Header().Set("Access-Control-Allow-Origin", "*")
			} else if origin != "" && originsMap[origin] {
				w.Header().Set("Access-Control-Allow-Origin", origin)
			}

			w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
			w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Requested-With")

			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// RecoveryMiddleware catches panics in HTTP handlers and responds with 500.
func RecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("[PANIC] recovered from: %v", rec)
				writeError(w, http.StatusInternalServerError, "internal server error")
			}
		}()
		next.ServeHTTP(w, r)
	})
}
