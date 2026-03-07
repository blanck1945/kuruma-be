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
	lomasDeZamoraBaseURL = "https://webextra.lomasdezamora.gov.ar/infracciones/ConsultaFaltasNuevoMP.aspx"
)

type LomasDeZamoraScraperProvider struct {
	client *http.Client
}

func NewLomasDeZamoraScraperProvider() *LomasDeZamoraScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &LomasDeZamoraScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second, Jar: jar},
	}
}

func (p *LomasDeZamoraScraperProvider) Name() string     { return "lomasdezamora_scraper" }
func (p *LomasDeZamoraScraperProvider) Priority() int    { return 42 }
func (p *LomasDeZamoraScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "lomasdezamora") && strings.TrimSpace(q.Plate) != ""
}

func (p *LomasDeZamoraScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))
	// Lomas de Zamora uses ASP.NET WebForms — same ViewState + POST pattern as SIGEIN.
	// ⚠️ The plate field name (tbPatente / txtPatente / TextBoxDominio) needs DevTools confirmation.
	fines, err := fetchSIGEIN(ctx, p.client, lomasDeZamoraBaseURL, plate, "Lomas de Zamora")
	if err != nil {
		return core.FineResult{}, err
	}
	// Override the Source field set by the helper.
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
