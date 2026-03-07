package providers

import (
	"context"
	"strings"
	"time"

	"flota/internal/core"
)

type MockAPIProvider struct{}

func NewMockAPIProvider() *MockAPIProvider {
	return &MockAPIProvider{}
}

func (p *MockAPIProvider) Name() string { return "official_api_mock" }
func (p *MockAPIProvider) Priority() int { return 10 }
func (p *MockAPIProvider) Supports(query core.Query) bool {
	return query.Plate != ""
}

func (p *MockAPIProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	select {
	case <-ctx.Done():
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "provider timeout"}
	case <-time.After(80 * time.Millisecond):
	}

	if strings.EqualFold(query.Plate, "AAA000") {
		return core.FineResult{
			Fines: []core.Fine{
				{
					Vehicle:      core.VehicleInfo{Plate: query.Plate},
					Jurisdiction: "CABA",
					Offense:      "Exceso de velocidad",
					Amount:       45000,
					Currency:     "ARS",
					Status:       "PENDING",
					IssuedAt:     time.Now().AddDate(0, -1, 0),
					DueAt:        time.Now().AddDate(0, 0, 10),
					Source:       p.Name(),
					SourceRef:    "api-123",
				},
			},
			Total:      1,
			Source:     p.Name(),
			Confidence: "high",
			FetchedAt:  time.Now(),
		}, nil
	}

	return core.FineResult{}, core.DomainError{Code: core.ErrNotFound, Message: "no fines for plate"}
}

