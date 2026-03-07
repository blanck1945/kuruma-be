package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"flota/internal/core"
)

const (
	chacoAPIURL    = "https://policiacaminera.chaco.gov.ar/api/v1/traffic_fines/"
	chacoUserAgent = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

type ChacoScraperProvider struct {
	client *http.Client
}

func NewChacoScraperProvider() *ChacoScraperProvider {
	return &ChacoScraperProvider{
		client: &http.Client{Timeout: 12 * time.Second},
	}
}

func (p *ChacoScraperProvider) Name() string  { return "chaco_scraper" }
func (p *ChacoScraperProvider) Priority() int { return 34 }
func (p *ChacoScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "chaco" {
		return false
	}
	return strings.TrimSpace(query.Plate) != "" || strings.TrimSpace(query.Document) != ""
}

func (p *ChacoScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	params := url.Values{}
	if plate := strings.ToUpper(strings.TrimSpace(query.Plate)); plate != "" {
		params.Set("dominio", plate)
	} else {
		params.Set("dni", strings.TrimSpace(query.Document))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		chacoAPIURL+"?"+params.Encode(), nil)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed,
			Message: "failed to build chaco request"}
	}
	req.Header.Set("User-Agent", chacoUserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout,
				Message: "chaco request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed,
			Message: "chaco request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed,
			Message: "failed to read chaco response"}
	}

	fines, err := parseChacoResponse(body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed,
			Message: "failed to parse chaco response"}
	}

	if plate := strings.ToUpper(strings.TrimSpace(query.Plate)); plate != "" {
		for i := range fines {
			fines[i].Vehicle.Plate = plate
		}
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

// — response structs —

type chacoAPIResponse struct {
	Fotomultas []chacoFine `json:"fotomultas"`
	Caminera   []chacoFine `json:"caminera"`
}

// chacoFine uses any for money fields that may arrive as string or float.
type chacoFine struct {
	Acta        string `json:"acta"`
	Fecha       string `json:"fecha"`
	Importe     any    `json:"importe"`
	Estado      string `json:"estado"`
	Infraccion  string `json:"infraccion"`
	Dominio     string `json:"dominio"`
	Descripcion string `json:"descripcion"`
	Monto       any    `json:"monto"`
}

// — parser —

func parseChacoResponse(body []byte) ([]core.Fine, error) {
	var apiResp chacoAPIResponse
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, err
	}

	out := make([]core.Fine, 0)
	for _, f := range apiResp.Fotomultas {
		out = append(out, mapChacoFine(f, "fotomulta"))
	}
	for _, f := range apiResp.Caminera {
		out = append(out, mapChacoFine(f, "caminera"))
	}
	return out, nil
}

func mapChacoFine(f chacoFine, fineType string) core.Fine {
	_ = fineType
	issuedAt := parseChacoFecha(f.Fecha)

	amount := chacoParseAmount(f.Importe)
	if amount == 0 {
		amount = chacoParseAmount(f.Monto)
	}

	status := strings.ToUpper(strings.TrimSpace(f.Estado))
	if status == "" {
		status = "PENDIENTE"
	}

	offense := f.Infraccion
	if offense == "" {
		offense = f.Descripcion
	}

	return core.Fine{
		Vehicle:      core.VehicleInfo{Plate: f.Dominio},
		Jurisdiction: "Chaco",
		Offense:      offense,
		Amount:       amount,
		Currency:     "ARS",
		Status:       status,
		IssuedAt:     issuedAt,
		Source:       "chaco_scraper",
		SourceRef:    f.Acta,
	}
}

// — helpers —

func parseChacoFecha(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now()
	}
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		loc = time.FixedZone("-03", -3*60*60)
	}
	for _, layout := range []string{"02/01/2006", "2006-01-02", "2006-01-02T15:04:05"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t
		}
	}
	return time.Now()
}

// chacoParseAmount handles string or float JSON values for money fields.
func chacoParseAmount(v any) float64 {
	if v == nil {
		return 0
	}
	switch val := v.(type) {
	case float64:
		return val
	case string:
		return parseAmount(val) // reuse caba's parseAmount (same package)
	}
	return 0
}
