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

// ⚠️ Confirm exact endpoint and payload with DevTools on https://brown.gob.ar/tribunal/consulta-deuda
const (
	almBrownBaseURL = "https://brown.gob.ar/tribunal/consulta-deuda"
	almBrownAPIURL  = "https://brown.gob.ar/tribunal/api/consulta"
	almBrownUA      = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

type AlmiranteBrownScraperProvider struct {
	client *http.Client
}

func NewAlmiranteBrownScraperProvider() *AlmiranteBrownScraperProvider {
	return &AlmiranteBrownScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *AlmiranteBrownScraperProvider) Name() string     { return "almirante_brown_scraper" }
func (p *AlmiranteBrownScraperProvider) Priority() int    { return 44 }
func (p *AlmiranteBrownScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "almirante_brown") && strings.TrimSpace(q.Plate) != ""
}

func (p *AlmiranteBrownScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))

	// ⚠️ Payload structure to confirm with DevTools
	form := url.Values{}
	form.Set("dominio", plate)
	form.Set("patente", plate)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, almBrownAPIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "almirante_brown: failed to build request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", almBrownUA)
	req.Header.Set("Origin", "https://brown.gob.ar")
	req.Header.Set("Referer", almBrownBaseURL)

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "almirante_brown: request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "almirante_brown: request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "almirante_brown: failed to read response"}
	}

	fines, err := parseGenericJSONFines(body, plate, "Almirante Brown", "almirante_brown_scraper")
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "almirante_brown: failed to parse response"}
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
