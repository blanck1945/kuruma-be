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
	// ⚠️ Confirm exact POST endpoint with DevTools (Network tab) on the real site.
	jujuyAPIURL   = "https://rentas.sansalvadordejujuy.gob.ar/Impuestos/BuscarInfraccion"
	jujuySiteKey  = "6LfZ0vEpAAAAACJA2cqqRDzJib3jGnGZ2G-fAh4I"
	jujuySiteURL  = "https://rentas.sansalvadordejujuy.gob.ar/"
	jujuyUserAgent = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

type JujuyScraperProvider struct {
	client      *http.Client
	solver      captchasolver.Solver
	cachedToken string
	tokenExpiry time.Time
	mu          sync.Mutex
}

func NewJujuyScraperProvider(solver captchasolver.Solver) *JujuyScraperProvider {
	return &JujuyScraperProvider{
		client: &http.Client{Timeout: 30 * time.Second},
		solver: solver,
	}
}

func (p *JujuyScraperProvider) Name() string  { return "jujuy_scraper" }
func (p *JujuyScraperProvider) Priority() int { return 37 }
func (p *JujuyScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "jujuy" {
		return false
	}
	return strings.TrimSpace(query.Plate) != ""
}

func (p *JujuyScraperProvider) resolveToken(override string) string {
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
	token, err := p.solver.Solve(ctx, jujuySiteKey, jujuySiteURL)
	if err != nil || token == "" {
		return ""
	}
	p.cachedToken = token
	p.tokenExpiry = time.Now().Add(90 * time.Second)
	return token
}

func (p *JujuyScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(query.Plate))
	token := p.resolveToken(query.JujuyCaptchaToken)

	payload, _ := json.Marshal(map[string]interface{}{
		"dominio":   plate,
		"recaptcha": token,
	})

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, jujuyAPIURL, bytes.NewReader(payload))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build jujuy request"}
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", jujuyUserAgent)
	req.Header.Set("Origin", "https://rentas.sansalvadordejujuy.gob.ar")
	req.Header.Set("Referer", "https://rentas.sansalvadordejujuy.gob.ar/")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "jujuy request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "jujuy request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to read jujuy response"}
	}

	fines, err := parseJujuyResponse(body, plate)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to parse jujuy response"}
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

type jujuyEnvelope struct {
	Message      string      `json:"message"`
	Multas       []saltaFine `json:"multas"`
	Data         []saltaFine `json:"data"`
	Infracciones []saltaFine `json:"infracciones"`
}

// — parser —

func parseJujuyResponse(body []byte, plate string) ([]core.Fine, error) {
	var env jujuyEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, err
	}

	msg := strings.ToLower(env.Message)
	if strings.Contains(msg, "no pos") || strings.Contains(msg, "no tiene") || strings.Contains(msg, "sin multa") {
		return []core.Fine{}, nil
	}

	rows := env.Multas
	if len(rows) == 0 {
		rows = env.Data
	}
	if len(rows) == 0 {
		rows = env.Infracciones
	}

	out := make([]core.Fine, 0, len(rows))
	for _, f := range rows {
		out = append(out, mapJujuyFine(f, plate))
	}
	return out, nil
}

func mapJujuyFine(f saltaFine, plate string) core.Fine {
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
		Jurisdiction: "San Salvador de Jujuy",
		Offense:      offense,
		Amount:       amount,
		Currency:     "ARS",
		Status:       status,
		IssuedAt:     issuedAt,
		DueAt:        parseSaltaFecha(f.Vencimiento),
		Source:       "jujuy_scraper",
		SourceRef:    ref,
	}
}
