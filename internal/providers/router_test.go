package providers

import (
	"context"
	"errors"
	"testing"
	"time"

	"flota/internal/core"
)

type staticProvider struct {
	name     string
	priority int
	result   core.FineResult
	err      error
}

func (s staticProvider) Name() string                            { return s.name }
func (s staticProvider) Priority() int                           { return s.priority }
func (s staticProvider) Supports(_ core.Query) bool              { return true }
func (s staticProvider) Fetch(_ context.Context, _ core.Query) (core.FineResult, error) {
	return s.result, s.err
}

func TestRouterPrefersSuccessfulProvider(t *testing.T) {
	r := NewProviderRouter(2*time.Second,
		staticProvider{
			name:     "api",
			priority: 10,
			result: core.FineResult{
				Fines: []core.Fine{{Vehicle: core.VehicleInfo{Plate: "AAA000"}, Currency: "ars", Status: "pending"}},
				Source:     "api",
				Confidence: "HIGH",
				FetchedAt:  time.Now(),
			},
		},
		staticProvider{name: "scraper", priority: 100, err: errors.New("down")},
	)

	out, err := r.Fetch(context.Background(), core.Query{Plate: "AAA000"})
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if out.Source != "api" {
		t.Fatalf("expected source api, got %s", out.Source)
	}
	if out.Fines[0].Currency != "ARS" {
		t.Fatalf("expected normalized currency ARS, got %s", out.Fines[0].Currency)
	}
}

