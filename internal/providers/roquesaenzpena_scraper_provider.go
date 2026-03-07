package providers

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"flota/internal/core"
)

const roqueSaenzPenaSIGEINURL = "https://rsp.sigein.net/home.aspx"

type RoqueSaenzPenaScraperProvider struct {
	client *http.Client
}

func NewRoqueSaenzPenaScraperProvider() *RoqueSaenzPenaScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &RoqueSaenzPenaScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second, Jar: jar},
	}
}

func (p *RoqueSaenzPenaScraperProvider) Name() string     { return "roquesaenzpena_scraper" }
func (p *RoqueSaenzPenaScraperProvider) Priority() int    { return 39 }
func (p *RoqueSaenzPenaScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "roquesaenzpena") && strings.TrimSpace(q.Plate) != ""
}

func (p *RoqueSaenzPenaScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))
	fines, err := fetchSIGEIN(ctx, p.client, roqueSaenzPenaSIGEINURL, plate, "Roque Sáenz Peña")
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
