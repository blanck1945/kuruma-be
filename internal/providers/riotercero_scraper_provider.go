package providers

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"flota/internal/core"
)

const rioTerceroSIGEINURL = "https://riotercero.sigein.net/home.aspx"

type RioTerceroScraperProvider struct {
	client *http.Client
}

func NewRioTerceroScraperProvider() *RioTerceroScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &RioTerceroScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second, Jar: jar},
	}
}

func (p *RioTerceroScraperProvider) Name() string     { return "riotercero_scraper" }
func (p *RioTerceroScraperProvider) Priority() int    { return 38 }
func (p *RioTerceroScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "riotercero") && strings.TrimSpace(q.Plate) != ""
}

func (p *RioTerceroScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))
	fines, err := fetchSIGEIN(ctx, p.client, rioTerceroSIGEINURL, plate, "Río Tercero")
	if err != nil {
		return core.FineResult{}, err
	}
	confidence := "medium"
	if len(fines) > 0 {
		confidence = "high"
	}
	return core.FineResult{
		Fines:      fines,
		Total:      len(fines),
		Source:     p.Name(),
		Confidence: confidence,
		FetchedAt:  time.Now(),
	}, nil
}
