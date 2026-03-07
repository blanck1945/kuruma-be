package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"flota/internal/core"
)

const (
	erConsultaURL = "https://api.monitoreovialentrerios.ar/api/entre_rios/infracciones_v1"
	erAuthToken   = "3cWREV3JLU3E3ZEpwMlE9PSIsInZhbHVlIjoiS2"
	// consulta=1 → dominio (patente), consulta=0 → CUIL/DNI
	erTipoDominio   = "1"
	erTipoDocumento = "0"
)

type EntreRiosScraperProvider struct {
	client *http.Client
}

func NewEntreRiosScraperProvider() *EntreRiosScraperProvider {
	return &EntreRiosScraperProvider{
		client: &http.Client{Timeout: 10 * time.Second},
	}
}

func (p *EntreRiosScraperProvider) Name() string  { return "entrerios_scraper" }
func (p *EntreRiosScraperProvider) Priority() int { return 35 }
func (p *EntreRiosScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "entrerios" {
		return false
	}
	return strings.TrimSpace(query.Plate) != "" || strings.TrimSpace(query.Document) != ""
}

func (p *EntreRiosScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	values := url.Values{}
	if plate := strings.ToUpper(strings.TrimSpace(query.Plate)); plate != "" {
		values.Set("consulta", erTipoDominio)
		values.Set("id", plate)
	} else {
		values.Set("consulta", erTipoDocumento)
		values.Set("id", strings.TrimSpace(query.Document))
	}
	values.Set("page", "1")
	values.Set("pagina", "100")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, erConsultaURL, strings.NewReader(values.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build entrerios request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded;charset=UTF-8")
	req.Header.Set("Authorization", erAuthToken)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FlotaBot/1.0)")
	req.Header.Set("Origin", "https://monitoreovialentrerios.info")
	req.Header.Set("Referer", "https://monitoreovialentrerios.info/")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "entrerios request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "entrerios request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to read entrerios response"}
	}

	// Empty body means no results for this plate in the ER database.
	if len(strings.TrimSpace(string(body))) == 0 {
		return core.FineResult{
			Fines:      []core.Fine{},
			Total:      0,
			Source:     p.Name(),
			Confidence: "medium",
			FetchedAt:  time.Now(),
		}, nil
	}

	fines, plate := parseEntreRiosResponse(body, strings.ToUpper(strings.TrimSpace(query.Plate)))
	confidence := "medium"
	if len(fines) > 0 {
		confidence = "high"
	}
	_ = plate
	return core.FineResult{
		Fines:      fines,
		Total:      len(fines),
		Source:     p.Name(),
		Confidence: confidence,
		FetchedAt:  time.Now(),
	}, nil
}

// — response structs —

type erEnvelope struct {
	Datos *struct {
		Data  []erFine `json:"data"`
		Total int      `json:"total"`
	} `json:"datos"`
	MontoTotal *string `json:"montoTotal"`
	Message    string  `json:"message"`
}

type erFine struct {
	IDInfraccion    int     `json:"idInfraccion"`
	Dominio         string  `json:"dominio"`
	FechaInfraccion string  `json:"fecha_infraccion"`
	HoraInfraccion  string  `json:"hora_infraccion"`
	Descripcion     string  `json:"descripcion"`
	MontoAPagar     float64 `json:"monto_a_pagar"`
	Multa           float64 `json:"multa"`
	Estado          string  `json:"estado"`
	Zona            string  `json:"zona"`
	Vto1            string  `json:"vto1"`
}

func parseEntreRiosResponse(body []byte, plate string) ([]core.Fine, string) {
	var env erEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Datos == nil {
		return []core.Fine{}, plate
	}

	out := make([]core.Fine, 0, len(env.Datos.Data))
	for _, row := range env.Datos.Data {
		amount := row.MontoAPagar
		if amount == 0 {
			amount = row.Multa
		}

		jurisdiction := "Entre Ríos"
		if z := strings.TrimSpace(row.Zona); z != "" {
			jurisdiction = "Entre Ríos - " + z
		}

		status := strings.ToUpper(strings.TrimSpace(row.Estado))
		if status == "" {
			status = "PENDING"
		}

		p := plate
		if p == "" {
			p = strings.ToUpper(strings.TrimSpace(row.Dominio))
		}

		issuedAt := erParseDate(row.FechaInfraccion, row.HoraInfraccion)
		dueAt := erParseDate(row.Vto1, "")

		out = append(out, core.Fine{
			Vehicle:      core.VehicleInfo{Plate: p},
			Jurisdiction: jurisdiction,
			Offense:      strings.TrimSpace(row.Descripcion),
			Amount:       amount,
			Currency:     "ARS",
			Status:       status,
			IssuedAt:     issuedAt,
			DueAt:        dueAt,
			Source:       "entrerios_scraper",
			SourceRef:    erFormatRef(row.IDInfraccion),
		})
	}
	return out, plate
}

func erFormatRef(id int) string {
	if id == 0 {
		return ""
	}
	return strconv.Itoa(id)
}

func erParseDate(date, hora string) time.Time {
	date = strings.TrimSpace(date)
	if date == "" {
		return time.Time{}
	}
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		loc = time.FixedZone("-03", -3*60*60)
	}
	hora = strings.TrimSpace(hora)
	if hora != "" {
		raw := date + " " + hora
		for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04"} {
			if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
				return t
			}
		}
	}
	if t, err := time.ParseInLocation("2006-01-02", date, loc); err == nil {
		return t
	}
	return time.Time{}
}
