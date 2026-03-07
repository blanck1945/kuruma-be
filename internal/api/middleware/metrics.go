package middleware

import (
	"fmt"
	"net/http"
	"time"

	"flota/internal/storage/metrics"
)

type responseRecorder struct {
	http.ResponseWriter
	status int
}

func (r *responseRecorder) WriteHeader(code int) {
	r.status = code
	r.ResponseWriter.WriteHeader(code)
}

func Metrics(rec *metrics.Recorder) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rr := &responseRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rr, r)
			rec.Observe(r.URL.Path, r.Method, fmt.Sprintf("%d", rr.status), time.Since(start))
		})
	}
}

