package providers

import (
	"context"

	"flota/internal/core"
)

type FineProvider interface {
	Name() string
	Priority() int
	Supports(query core.Query) bool
	Fetch(ctx context.Context, query core.Query) (core.FineResult, error)
}

