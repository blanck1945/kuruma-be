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

// ⚠️ SiJAI endpoint — confirm exact URL and form field names with DevTools on https://infracciones.escobar.gob.ar/
const (
	escobarBaseURL = "https://infracciones.escobar.gob.ar/"
	escobarAPIURL  = "https://infracciones.escobar.gob.ar/consulta"
	escobarUA      = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

type EscobarScraperProvider struct {
	client *http.Client
}

func NewEscobarScraperProvider() *EscobarScraperProvider {
	return &EscobarScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *EscobarScraperProvider) Name() string     { return "escobar_scraper" }
func (p *EscobarScraperProvider) Priority() int    { return 45 }
func (p *EscobarScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "escobar") && strings.TrimSpace(q.Plate) != ""
}

func (p *EscobarScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))

	// ⚠️ SiJAI form fields to confirm with DevTools
	form := url.Values{}
	form.Set("dominio", plate)
	form.Set("patente", plate)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, escobarAPIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "escobar: failed to build request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", escobarUA)
	req.Header.Set("Origin", escobarBaseURL)
	req.Header.Set("Referer", escobarBaseURL)

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "escobar: request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "escobar: request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "escobar: failed to read response"}
	}

	fines, err := parseGenericJSONFines(body, plate, "Escobar", "escobar_scraper")
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "escobar: failed to parse response"}
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
