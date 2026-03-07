package providers

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"flota/internal/core"
	"flota/internal/providers/captchasolver"
)

const (
	saltaAPIURL    = "https://rentas.dgrmsalta.gov.ar/api/automotores/multas"
	saltaSiteKey   = "6LcO31EpAAAAACskh5BK2bB86lwBjRxTp5leeiz4"
	saltaSiteURL   = "https://rentas.dgrmsalta.gov.ar/"
	saltaUserAgent = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

type SaltaScraperProvider struct {
	client      *http.Client
	solver      captchasolver.Solver
	cachedToken string
	tokenExpiry time.Time
	mu          sync.Mutex
}

func NewSaltaScraperProvider(solver captchasolver.Solver) *SaltaScraperProvider {
	return &SaltaScraperProvider{
		client: &http.Client{Timeout: 30 * time.Second},
		solver: solver,
	}
}

func (p *SaltaScraperProvider) Name() string  { return "salta_scraper" }
func (p *SaltaScraperProvider) Priority() int { return 36 }
func (p *SaltaScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "salta" {
		return false
	}
	return strings.TrimSpace(query.Plate) != "" // solo por patente
}

func (p *SaltaScraperProvider) resolveToken(override string) string {
	if t := strings.TrimSpace(override); t != "" {
		return t
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cachedToken != "" && time.Now().Before(p.tokenExpiry) {
		return p.cachedToken
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	token, err := p.solver.Solve(ctx, saltaSiteKey, saltaSiteURL)
	if err != nil || token == "" {
		return ""
	}
	p.cachedToken = token
	p.tokenExpiry = time.Now().Add(90 * time.Second)
	return token
}

func (p *SaltaScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(query.Plate))
	token := p.resolveToken(query.SaltaCaptchaToken)

	payload, _ := json.Marshal(map[string]interface{}{
		"acta":      nil,
		"dominio":   plate,
		"recaptcha": token,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, saltaAPIURL, bytes.NewReader(payload))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build salta request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", saltaUserAgent)
	req.Header.Set("Origin", "https://rentas.dgrmsalta.gov.ar")
	req.Header.Set("Referer", "https://rentas.dgrmsalta.gov.ar/")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "salta request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "salta request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to read salta response"}
	}

	fines, err := parseSaltaResponse(body, plate)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to parse salta response"}
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

type saltaEnvelope struct {
	Message      string      `json:"message"`
	Multas       []saltaFine `json:"multas"`
	Data         []saltaFine `json:"data"`
	Infracciones []saltaFine `json:"infracciones"`
}

type saltaFine struct {
	Acta        string `json:"acta"`
	NumeroActa  string `json:"numero_acta"`
	Fecha       string `json:"fecha"`
	FechaActa   string `json:"fecha_acta"`
	Importe     any    `json:"importe"`
	Monto       any    `json:"monto"`
	Estado      string `json:"estado"`
	Infraccion  string `json:"infraccion"`
	Dominio     string `json:"dominio"`
	Descripcion string `json:"descripcion"`
	Vencimiento string `json:"vencimiento"`
}

// — parser —

func parseSaltaResponse(body []byte, plate string) ([]core.Fine, error) {
	var env saltaEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}

	// "El dominio indicado no poseé multas pendientes" → vacío sin error
	msg := strings.ToLower(env.Message)
	if strings.Contains(msg, "no pos") || strings.Contains(msg, "no tiene") || strings.Contains(msg, "sin multa") {
		return []core.Fine{}, nil
	}

	// Intentar los posibles campos de array
	rows := env.Multas
	if len(rows) == 0 {
		rows = env.Data
	}
	if len(rows) == 0 {
		rows = env.Infracciones
	}

	out := make([]core.Fine, 0, len(rows))
	for _, f := range rows {
		out = append(out, mapSaltaFine(f, plate))
	}
	return out, nil
}

func mapSaltaFine(f saltaFine, plate string) core.Fine {
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
		Jurisdiction: "Salta",
		Offense:      offense,
		Amount:       amount,
		Currency:     "ARS",
		Status:       status,
		IssuedAt:     issuedAt,
		DueAt:        parseSaltaFecha(f.Vencimiento),
		Source:       "salta_scraper",
		SourceRef:    ref,
	}
}

func parseSaltaFecha(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
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
	return time.Time{}
}
