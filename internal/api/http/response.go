package httpapi

import (
	"encoding/json"
	"net/http"
	"time"

	"flota/internal/api/middleware"
	"flota/internal/core"
)

type envelope struct {
	Success   bool         `json:"success"`
	Data      any          `json:"data,omitempty"`
	Error     *errorBody   `json:"error,omitempty"`
	Meta      *meta        `json:"meta,omitempty"`
	TraceID   string       `json:"trace_id"`
	Timestamp string       `json:"timestamp"`
}

type errorBody struct {
	Code    core.ErrorCode `json:"code"`
	Message string         `json:"message"`
}

type meta struct {
	Role   string `json:"role,omitempty"`
	Client string `json:"client,omitempty"`
}

func writeJSON(w http.ResponseWriter, status int, payload envelope) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

func okResponse(w http.ResponseWriter, r *http.Request, data any) {
	writeJSON(w, http.StatusOK, envelope{
		Success: true,
		Data:    data,
		Meta: &meta{
			Role:   middleware.Role(r.Context()),
			Client: middleware.Client(r.Context()),
		},
		TraceID:   middleware.TraceID(r.Context()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

func errorResponse(w http.ResponseWriter, r *http.Request, status int, code core.ErrorCode, message string) {
	writeJSON(w, status, envelope{
		Success: false,
		Error: &errorBody{
			Code:    code,
			Message: message,
		},
		TraceID:   middleware.TraceID(r.Context()),
		Timestamp: time.Now().UTC().Format(time.RFC3339),
	})
}

