package providers

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"flota/internal/core"
)

// ⚠️ Confirm exact endpoint and payload with DevTools on https://multas.mda.gob.ar/
const (
	avellanedaBaseURL = "https://multas.mda.gob.ar/"
	avellanedaAPIURL  = "https://multas.mda.gob.ar/api/consulta"
	avellanedaUA      = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

type AvellanedaScraperProvider struct {
	client *http.Client
}

func NewAvellanedaScraperProvider() *AvellanedaScraperProvider {
	return &AvellanedaScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *AvellanedaScraperProvider) Name() string     { return "avellaneda_scraper" }
func (p *AvellanedaScraperProvider) Priority() int    { return 43 }
func (p *AvellanedaScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "avellaneda") && strings.TrimSpace(q.Plate) != ""
}

func (p *AvellanedaScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))

	// ⚠️ Payload structure to confirm with DevTools
	form := url.Values{}
	form.Set("dominio", plate)
	form.Set("patente", plate)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, avellanedaAPIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "avellaneda: failed to build request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", avellanedaUA)
	req.Header.Set("Origin", avellanedaBaseURL)
	req.Header.Set("Referer", avellanedaBaseURL)

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "avellaneda: request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "avellaneda: request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "avellaneda: failed to read response"}
	}

	fines, err := parseGenericJSONFines(body, plate, "Avellaneda", "avellaneda_scraper")
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "avellaneda: failed to parse response"}
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
