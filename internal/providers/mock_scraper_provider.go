package providers

import (
	"context"
	"time"

	"flota/internal/core"
)

type MockScraperProvider struct {
	enabled bool
}

func NewMockScraperProvider(enabled bool) *MockScraperProvider {
	return &MockScraperProvider{enabled: enabled}
}

func (p *MockScraperProvider) Name() string { return "public_web_scraper_mock" }
func (p *MockScraperProvider) Priority() int { return 100 }
func (p *MockScraperProvider) Supports(query core.Query) bool {
	return p.enabled && query.Plate != ""
}

func (p *MockScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	select {
	case <-ctx.Done():
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "scraper timeout"}
	case <-time.After(220 * time.Millisecond):
	}

	return core.FineResult{
		Fines:      []core.Fine{},
		Total:      0,
		Source:     p.Name(),
		Confidence: "low",
		FetchedAt:  time.Now(),
	}, nil
}

