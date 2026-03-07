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

// ⚠️ Boldt platform — confirm exact URL, form fields, and whether CAPTCHA is required
// via DevTools on https://venadotuerto-infracciones.boldt.com.ar/secretariavirtual/
// If reCAPTCHA is present, this provider will fail gracefully (ErrProviderFailed).
const (
	venadoTuertoBaseURL = "https://venadotuerto-infracciones.boldt.com.ar/secretariavirtual/"
	venadoTuertoAPIURL  = "https://venadotuerto-infracciones.boldt.com.ar/secretariavirtual/consulta"
	venadoTuertoUA      = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

type VenadoTuertoScraperProvider struct {
	client *http.Client
}

func NewVenadoTuertoScraperProvider() *VenadoTuertoScraperProvider {
	return &VenadoTuertoScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *VenadoTuertoScraperProvider) Name() string     { return "venadotuerto_scraper" }
func (p *VenadoTuertoScraperProvider) Priority() int    { return 47 }
func (p *VenadoTuertoScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "venadotuerto") && strings.TrimSpace(q.Plate) != ""
}

func (p *VenadoTuertoScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))

	// ⚠️ Boldt form fields to confirm with DevTools
	form := url.Values{}
	form.Set("dominio", plate)
	form.Set("patente", plate)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, venadoTuertoAPIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "venadotuerto: failed to build request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", venadoTuertoUA)
	req.Header.Set("Origin", venadoTuertoBaseURL)
	req.Header.Set("Referer", venadoTuertoBaseURL)

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "venadotuerto: request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "venadotuerto: request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "venadotuerto: failed to read response"}
	}

	fines, err := parseGenericJSONFines(body, plate, "Venado Tuerto", "venadotuerto_scraper")
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "venadotuerto: failed to parse response"}
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
