package middleware

import (
	"encoding/json"
	"net/http"
	"time"

	"flota/internal/core"
)

type authErrorResponse struct {
	Success   bool           `json:"success"`
	Error     authErrorBody  `json:"error"`
	TraceID   string         `json:"trace_id"`
	Timestamp string         `json:"timestamp"`
}

type authErrorBody struct {
	Code    core.ErrorCode `json:"code"`
	Message string         `json:"message"`
}

func writeAuthError(w http.ResponseWriter, r *http.Request, code core.ErrorCode, msg string) {
	status := http.StatusUnauthorized
	switch code {
	case core.ErrForbidden:
		status = http.StatusForbidden
	case core.ErrRateLimited:
		status = http.StatusTooManyRequests
	}

	resp := authErrorResponse{
		Success: false,
		Error: authErrorBody{
			Code:    code,
			Message: msg,
		},
		TraceID:   TraceID(r.Context()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(resp)
}

