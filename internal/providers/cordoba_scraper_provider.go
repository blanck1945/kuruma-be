package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"flota/internal/core"
)

const cordobaCaminera = "https://app.rentascordoba.gob.ar/WSRestDeudaAnt/public/all/caminera/dominio/"

type CordobaScraperProvider struct {
	client *http.Client
}

func NewCordobaScraperProvider() *CordobaScraperProvider {
	return &CordobaScraperProvider{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *CordobaScraperProvider) Name() string  { return "cordoba_scraper" }
func (p *CordobaScraperProvider) Priority() int { return 25 }
func (p *CordobaScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "cordoba" {
		return false
	}
	return strings.TrimSpace(query.Plate) != ""
}

func (p *CordobaScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(query.Plate))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, cordobaCaminera+plate, nil)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build cordoba request"}
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FlotaBot/1.0)")
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "cordoba request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "cordoba request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to read cordoba response"}
	}

	fines, err := parseCordobaResponse(body, plate)
	if err != nil {
		return core.FineResult{}, err
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

type cordobaEnvelope struct {
	Status struct {
		Success string `json:"success"`
	} `json:"status"`
	Data *struct {
		Contribuyentes []cordobaContribuyente `json:"contribuyentes"`
	} `json:"data"`
}

type cordobaContribuyente struct {
	Objetos []cordobaObjeto `json:"objetos"`
	Juicios []struct {
		Objetos []cordobaObjeto `json:"objetos"`
	} `json:"juicios"`
}

type cordobaObjeto struct {
	Referencia1  string              `json:"referencia1"`
	Obligaciones []cordobaObligacion `json:"obligaciones"`
}

type cordobaObligacion struct {
	IdObligacion     string  `json:"idObligacion"`
	Concepto         string  `json:"concepto"`
	SaldoTotal       float64 `json:"saldoTotal"`
	Estado           string  `json:"estado"`
	FechaVencimiento string  `json:"fechaVencimiento"`
	FechaLabrado     string  `json:"fechaLabrado"`
	FechaEmision     string  `json:"fechaEmision"`
	InstanciaGestion string  `json:"instanciaGestion"`
}

// — parser —

func parseCordobaResponse(body []byte, plate string) ([]core.Fine, error) {
	var env cordobaEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "invalid cordoba response"}
	}
	if env.Status.Success != "TRUE" {
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "cordoba api error"}
	}
	if env.Data == nil {
		return []core.Fine{}, nil
	}

	var out []core.Fine
	for _, contrib := range env.Data.Contribuyentes {
		for _, obj := range contrib.Objetos {
			for _, obl := range obj.Obligaciones {
				out = append(out, cordobaToFine(obl, obj.Referencia1, plate, false))
			}
		}
		for _, juicio := range contrib.Juicios {
			for _, obj := range juicio.Objetos {
				for _, obl := range obj.Obligaciones {
					out = append(out, cordobaToFine(obl, obj.Referencia1, plate, true))
				}
			}
		}
	}
	return out, nil
}

func cordobaToFine(obl cordobaObligacion, ref, plate string, judicial bool) core.Fine {
	offense := strings.TrimSpace(obl.Concepto)
	if offense == "" {
		offense = "Infracción de tránsito"
	}

	status := strings.ToUpper(strings.TrimSpace(obl.Estado))
	if judicial && status != "" {
		status = fmt.Sprintf("JUDICIAL - %s", status)
	}
	if status == "" {
		status = "PENDING"
	}

	sourceRef := strings.TrimSpace(ref)
	if sourceRef == "" {
		sourceRef = obl.IdObligacion
	}

	issuedAt := cordobaParseDate(obl.FechaLabrado)
	if issuedAt.IsZero() {
		issuedAt = cordobaParseDate(obl.FechaEmision)
	}
	if issuedAt.IsZero() {
		issuedAt = time.Now()
	}

	return core.Fine{
		Vehicle:      core.VehicleInfo{Plate: plate},
		Jurisdiction: "Córdoba",
		Offense:      offense,
		Amount:       obl.SaldoTotal,
		Currency:     "ARS",
		Status:       status,
		Controller:   obl.InstanciaGestion,
		IssuedAt:     issuedAt,
		DueAt:        cordobaParseDate(obl.FechaVencimiento),
		Source:       "cordoba_scraper",
		SourceRef:    sourceRef,
	}
}

func cordobaParseDate(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}
	}
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		loc = time.FixedZone("-03", -3*60*60)
	}
	for _, layout := range []string{
		"2006-01-02T15:04:05.000-07:00",
		"2006-01-02T15:04:05-07:00",
		time.RFC3339,
		"2006-01-02",
	} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t
		}
	}
	return time.Time{}
}
