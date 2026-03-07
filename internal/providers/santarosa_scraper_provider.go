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

// ⚠️ Confirm exact endpoint and payload with DevTools on https://fotomultas.santarosa.gob.ar/
const (
	santaRosaBaseURL  = "https://fotomultas.santarosa.gob.ar/"
	santaRosaAPIURL   = "https://fotomultas.santarosa.gob.ar/api/infracciones/consulta"
	santaRosaUA       = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

type SantaRosaScraperProvider struct {
	client *http.Client
}

func NewSantaRosaScraperProvider() *SantaRosaScraperProvider {
	return &SantaRosaScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second},
	}
}

func (p *SantaRosaScraperProvider) Name() string     { return "santarosa_scraper" }
func (p *SantaRosaScraperProvider) Priority() int    { return 41 }
func (p *SantaRosaScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "santarosa") && strings.TrimSpace(q.Plate) != ""
}

func (p *SantaRosaScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))

	// ⚠️ Payload structure to confirm with DevTools
	form := url.Values{}
	form.Set("dominio", plate)
	form.Set("patente", plate)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, santaRosaAPIURL, strings.NewReader(form.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "santarosa: failed to build request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", santaRosaUA)
	req.Header.Set("Origin", santaRosaBaseURL)
	req.Header.Set("Referer", santaRosaBaseURL)

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "santarosa: request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "santarosa: request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "santarosa: failed to read response"}
	}

	fines, err := parseGenericJSONFines(body, plate, "Santa Rosa", "santarosa_scraper")
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "santarosa: failed to parse response"}
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

// genericFineEnvelope tries common JSON shapes returned by municipal fine APIs.
type genericFineEnvelope struct {
	Message      string            `json:"message"`
	Multas       []genericFineLine `json:"multas"`
	Data         []genericFineLine `json:"data"`
	Infracciones []genericFineLine `json:"infracciones"`
	Items        []genericFineLine `json:"items"`
	Result       []genericFineLine `json:"result"`
}

type genericFineLine struct {
	Acta        string `json:"acta"`
	NumeroActa  string `json:"numero_acta"`
	Fecha       string `json:"fecha"`
	FechaActa   string `json:"fecha_acta"`
	Importe     any    `json:"importe"`
	Monto       any    `json:"monto"`
	Estado      string `json:"estado"`
	Infraccion  string `json:"infraccion"`
	Descripcion string `json:"descripcion"`
	Dominio     string `json:"dominio"`
	Vencimiento string `json:"vencimiento"`
}

func parseGenericJSONFines(body []byte, plate, jurisdiction, sourceName string) ([]core.Fine, error) {
	var env genericFineEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}

	msg := strings.ToLower(env.Message)
	if strings.Contains(msg, "no pos") || strings.Contains(msg, "no tiene") || strings.Contains(msg, "sin multa") || strings.Contains(msg, "no se encontr") {
		return []core.Fine{}, nil
	}

	rows := env.Multas
	if len(rows) == 0 {
		rows = env.Data
	}
	if len(rows) == 0 {
		rows = env.Infracciones
	}
	if len(rows) == 0 {
		rows = env.Items
	}
	if len(rows) == 0 {
		rows = env.Result
	}

	out := make([]core.Fine, 0, len(rows))
	for _, f := range rows {
		out = append(out, mapGenericFine(f, plate, jurisdiction, sourceName))
	}
	return out, nil
}

func mapGenericFine(f genericFineLine, plate, jurisdiction, sourceName string) core.Fine {
	issuedAt := parseSaltaFecha(f.Fecha)
	if issuedAt.IsZero() {
		issuedAt = parseSaltaFecha(f.FechaActa)
	}
	if issuedAt.IsZero() {
		issuedAt = time.Now()
	}

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

	ref := f.Acta
	if ref == "" {
		ref = f.NumeroActa
	}

	p := plate
	if p == "" {
		p = strings.ToUpper(strings.TrimSpace(f.Dominio))
	}

	return core.Fine{
		Vehicle:      core.VehicleInfo{Plate: p},
		Jurisdiction: jurisdiction,
		Offense:      offense,
		Amount:       amount,
		Currency:     "ARS",
		Status:       status,
		IssuedAt:     issuedAt,
		DueAt:        parseSaltaFecha(f.Vencimiento),
		Source:       sourceName,
		SourceRef:    ref,
	}
}
