package middleware

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"net/http"
)

type contextKey string

const (
	traceIDKey contextKey = "trace_id"
	roleKey    contextKey = "auth_role"
	clientKey  contextKey = "auth_client"
)

func Trace(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		traceID := r.Header.Get("X-Trace-Id")
		if traceID == "" {
			traceID = newTraceID()
		}
		ctx := context.WithValue(r.Context(), traceIDKey, traceID)
		w.Header().Set("X-Trace-Id", traceID)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func newTraceID() string {
	buf := make([]byte, 16)
	_, _ = rand.Read(buf)
	return hex.EncodeToString(buf)
}

func TraceID(ctx context.Context) string {
	v, _ := ctx.Value(traceIDKey).(string)
	return v
}

func SetAuth(ctx context.Context, role, client string) context.Context {
	ctx = context.WithValue(ctx, roleKey, role)
	ctx = context.WithValue(ctx, clientKey, client)
	return ctx
}

func Role(ctx context.Context) string {
	v, _ := ctx.Value(roleKey).(string)
	return v
}

func Client(ctx context.Context) string {
	v, _ := ctx.Value(clientKey).(string)
	return v
}

