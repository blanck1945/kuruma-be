package core

import "time"

type Query struct {
	Plate                string
	Document             string
	Source               string
	PBACaptchaToken      string
	MisionesCaptchaToken string
	SaltaCaptchaToken    string
	JujuyCaptchaToken    string
	MendozaCaptchaToken  string
}

type Fine struct {
	Vehicle      VehicleInfo `json:"vehicle"`
	Jurisdiction string      `json:"jurisdiction"`
	Offense      string      `json:"offense"`
	Amount       float64     `json:"amount"`
	Currency     string      `json:"currency"`
	Status       string      `json:"status"`
	Controller   string      `json:"controller,omitempty"`
	HasPhoto     bool        `json:"has_photo"`
	IssuedAt     time.Time   `json:"issued_at"`
	DueAt        time.Time   `json:"due_at,omitempty"`
	Source       string      `json:"source"`
	SourceRef    string      `json:"source_ref"`
}

type VehicleInfo struct {
	Plate string `json:"plate"`
}

type VehicleProfile struct {
	Plate      string    `json:"plate"`
	Make       string    `json:"make,omitempty"`
	Model      string    `json:"model,omitempty"`
	Year       int       `json:"year,omitempty"`
	Type       string    `json:"type,omitempty"`
	Fuel       string    `json:"fuel,omitempty"`
	Source     string    `json:"source"`
	Confidence string    `json:"confidence"`
	FetchedAt  time.Time `json:"fetched_at"`
}

type FineResult struct {
	Fines      []Fine    `json:"fines"`
	Total      int       `json:"total"`
	Source     string    `json:"source"`
	Confidence string    `json:"confidence"`
	FetchedAt  time.Time `json:"fetched_at"`
}

type ErrorCode string

const (
	ErrInvalidPlate          ErrorCode = "INVALID_PLATE"
	ErrNotFound              ErrorCode = "NOT_FOUND"
	ErrProviderTimeout       ErrorCode = "PROVIDER_TIMEOUT"
	ErrProviderFailed        ErrorCode = "PROVIDER_FAILED"
	ErrUnauthorized          ErrorCode = "UNAUTHORIZED"
	ErrForbidden             ErrorCode = "FORBIDDEN"
	ErrRateLimited           ErrorCode = "RATE_LIMITED"
	ErrInternal              ErrorCode = "INTERNAL_ERROR"
	ErrSubscriptionRequired  ErrorCode = "SUBSCRIPTION_REQUIRED"
)

type DomainError struct {
	Code    ErrorCode
	Message string
}

func (e DomainError) Error() string {
	return e.Message
}
