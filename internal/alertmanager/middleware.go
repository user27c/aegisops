package alertmanager

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/go-logr/logr"
)

type contextKey string

const requestIDKey contextKey = "request_id"

// withRequestID 注入并透传 X-Request-ID。
func withRequestID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rid := r.Header.Get("X-Request-ID")
		if rid == "" {
			rid = newRequestID()
		}
		w.Header().Set("X-Request-ID", rid)
		ctx := context.WithValue(r.Context(), requestIDKey, rid)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// withRecover 捕获 panic 并返回 500，避免进程崩溃。
func withRecover(next http.Handler, logger logr.Logger) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				logger.Error(fmt.Errorf("panic: %v", rec),
					"handler panic",
					"stack", string(debug.Stack()),
					"path", r.URL.Path,
				)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "内部错误"})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// withBodyLimit 限制请求体大小（额外一层，DecodeWebhook 内部也有 LimitReader）。
func withBodyLimit(next http.Handler, maxBytes int64) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		next.ServeHTTP(w, r)
	})
}

// withBearerAuth 校验 Authorization: Bearer <token>。
func withBearerAuth(next http.Handler, auth TokenValidator) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		header := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(header, prefix) || !auth.Validate(strings.TrimPrefix(header, prefix)) {
			writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "未授权"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func newRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte(time.Now().Format("150405.000")))
	}
	return hex.EncodeToString(b[:])
}
