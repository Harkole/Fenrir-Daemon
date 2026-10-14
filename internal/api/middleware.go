package api

import (
	"crypto/subtle"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/Harkole/fenrir-daemon/internal/config"
	"github.com/Harkole/fenrir-daemon/internal/database"
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

// APIKeyAuthMiddleware verifies incoming requests against keys configured or stored in the database layer.
func APIKeyAuthMiddleware(cfg *config.Config, db *database.DB) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Method == http.MethodOptions {
				next.ServeHTTP(w, r)
				return
			}

			key := extractAPIKey(r)
			if key == "" {
				writeError(w, http.StatusUnauthorized, "unauthorized: missing API key")
				return
			}

			authenticated := false

			// 1. Verify against static/pre-shared key in configuration
			if cfg != nil && cfg.APIKey != "" {
				if subtle.ConstantTimeCompare([]byte(key), []byte(cfg.APIKey)) == 1 {
					authenticated = true
				}
			}

			// 2. Verify against database stored keys
			if !authenticated && db != nil {
				valid, err := db.ValidateAPIKey(r.Context(), key)
				if err == nil && valid {
					authenticated = true
				}
			}

			if !authenticated {
				writeError(w, http.StatusUnauthorized, "unauthorized: invalid API key")
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// extractAPIKey retrieves the API key from headers (X-API-Key, Authorization) or query parameter.
func extractAPIKey(r *http.Request) string {
	if key := r.Header.Get("X-API-Key"); key != "" {
		return strings.TrimSpace(key)
	}

	auth := strings.TrimSpace(r.Header.Get("Authorization"))
	if auth != "" {
		lower := strings.ToLower(auth)
		if strings.HasPrefix(lower, "bearer ") {
			return strings.TrimSpace(auth[7:])
		}
		if strings.HasPrefix(lower, "apikey ") {
			return strings.TrimSpace(auth[7:])
		}
		return auth
	}

	if qKey := r.URL.Query().Get("api_key"); qKey != "" {
		return strings.TrimSpace(qKey)
	}

	return ""
}

