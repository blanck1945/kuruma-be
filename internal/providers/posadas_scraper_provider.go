package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"flota/internal/core"
)

// ⚠️ Confirm exact endpoint and JSON payload with DevTools on
// https://sistema.posadas.gov.ar/mp_sistemas/autogestion/consultarmultadominio
const (
	posadasBaseURL = "https://sistema.posadas.gov.ar/"
	posadasAPIURL  = "https://sistema.posadas.gov.ar/mp_sistemas/autogestion/consultarmultadominio"
	posadasUA      = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

type PosadasScraperProvider struct {
	client *http.Client
}

func NewPosadasScraperProvider() *PosadasScraperProvider {
	return &PosadasScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *PosadasScraperProvider) Name() string     { return "posadas_scraper" }
func (p *PosadasScraperProvider) Priority() int    { return 46 }
func (p *PosadasScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "posadas") && strings.TrimSpace(q.Plate) != ""
}

func (p *PosadasScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))

	// ⚠️ JSON payload keys to confirm with DevTools
	payload, _ := json.Marshal(map[string]interface{}{
		"dominio": plate,
		"patente": plate,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, posadasAPIURL, bytes.NewReader(payload))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "posadas: failed to build request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", posadasUA)
	req.Header.Set("Origin", posadasBaseURL)
	req.Header.Set("Referer", posadasBaseURL)

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "posadas: request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "posadas: request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "posadas: failed to read response"}
	}

	fines, err := parseGenericJSONFines(body, plate, "Posadas", "posadas_scraper")
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "posadas: failed to parse response"}
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
