package api

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"log/slog"
	"net/http"
	"regexp"
	"runtime/debug"
	"slices"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
)

var reqIDRe = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

func requestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := r.Header.Get("X-Request-Id")
		if !reqIDRe.MatchString(id) {
			var b [8]byte
			rand.Read(b[:])
			id = hex.EncodeToString(b[:])
		}
		w.Header().Set("X-Request-Id", id)
		next.ServeHTTP(w, r)
	})
}

// observe records the access log and metrics. It wraps recover so panics are logged as 500.
func observe(log *slog.Logger, m *Metrics, nets func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			m.start()
			next.ServeHTTP(ww, r)
			route := "unmatched"
			if rc := chi.RouteContext(r.Context()); rc != nil && rc.RoutePattern() != "" {
				route = rc.RoutePattern()
			}
			code, d := ww.Status(), time.Since(start)
			if code == 0 {
				code = http.StatusOK
			}
			m.done(r.Method, route, code, d)
			lvl := slog.LevelInfo
			if code >= 500 {
				lvl = slog.LevelError
			} else if r.URL.Path == "/healthz" {
				lvl = slog.LevelDebug
			}
			log.Log(r.Context(), lvl, "request", "request_id", w.Header().Get("X-Request-Id"), "method", r.Method,
				"path", r.URL.Path, "route", route, "status", code, "bytes", ww.BytesWritten(), "duration_ms", d.Milliseconds(), "ip", nets(r))
		})
	}
}

func nosniff(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		next.ServeHTTP(w, r)
	})
}

func recoverer(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if v := recover(); v != nil {
					if v == http.ErrAbortHandler {
						panic(v)
					}
					log.Error("panic", "request_id", w.Header().Get("X-Request-Id"), "panic", v, "stack", string(debug.Stack()))
					writeProblem(w, http.StatusInternalServerError, "")
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

func timeout(d time.Duration) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx, cancel := context.WithTimeout(r.Context(), d)
			defer cancel()
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// cors sets the CORS response headers; it sits before the rate limiter so 429s are readable by browsers.
func cors(origins []string) func(http.Handler) http.Handler {
	any := slices.Contains(origins, "*")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			o := r.Header.Get("Origin")
			h := w.Header()
			if !any {
				h.Add("Vary", "Origin")
			}
			if o != "" && (any || slices.Contains(origins, o)) {
				if any {
					h.Set("Access-Control-Allow-Origin", "*")
				} else {
					h.Set("Access-Control-Allow-Origin", o)
				}
				h.Set("Access-Control-Expose-Headers", "ETag, X-Data-Version, X-Request-Id, Retry-After")
			}
			next.ServeHTTP(w, r)
		})
	}
}

// preflight answers CORS preflights for allowed origins (cors has already set the origin header).
// It sits after the rate limiter so preflights are counted too.
func preflight(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" && h.Get("Access-Control-Allow-Origin") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, HEAD")
			h.Set("Access-Control-Allow-Headers", "If-None-Match, Content-Type")
			h.Set("Access-Control-Max-Age", "86400")
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}
