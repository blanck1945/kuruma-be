package providers

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"flota/internal/core"
)

// ⚠️ Confirm form field names with DevTools on the real ASP.NET page.
const (
	tresDeFebreroBaseURL = "https://mistramites.tresdefebrero.gov.ar/multas"
)

type TresDeFebreroScraperProvider struct {
	client *http.Client
}

func NewTresDeFebreroScraperProvider() *TresDeFebreroScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &TresDeFebreroScraperProvider{
		client: &http.Client{Timeout: 20 * time.Second, Jar: jar},
	}
}

func (p *TresDeFebreroScraperProvider) Name() string  { return "tresdefebrero_scraper" }
func (p *TresDeFebreroScraperProvider) Priority() int { return 49 }
func (p *TresDeFebreroScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "tresdefebrero") &&
		(strings.TrimSpace(q.Plate) != "" || strings.TrimSpace(q.Document) != "")
}

func (p *TresDeFebreroScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))
	// Tres de Febrero uses ASP.NET WebForms — same ViewState + POST pattern as SIGEIN.
	// ⚠️ The plate field name (Dominio / tbPatente / txtDominio) needs DevTools confirmation.
	fines, err := fetchSIGEIN(ctx, p.client, tresDeFebreroBaseURL, plate, "Tres de Febrero")
	if err != nil {
		return core.FineResult{}, err
	}
	for i := range fines {
		fines[i].Source = p.Name()
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
