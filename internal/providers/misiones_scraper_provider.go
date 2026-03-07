package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"flota/internal/core"
	"flota/internal/providers/captchasolver"
)

const (
	misionesAPIURL  = "https://api.monitoreovialmisiones.info/api/infracciones"
	misionesSiteKey = "6LeH2TogAAAAAA6fxVix89KwIHlhkuU_TFVK40Ox"
	misionesSiteURL = "https://monitoreovialmisiones.info/"
	// Bearer token extracted from the frontend JS bundle. Rotate if the API returns 401.
	misionesAuthToken = "5a49/AaqwnY-BFHJu-fNoYhW2q39is8=EOOgeP-soK2!M-73MADLwLQUPBdKHrZ!rynfOGF/ji5ykmbBoreT-yO!/nA7vymR/PdJTaGh4VVCc412q?eH1EAYA45VduBNbGYib8bC1qmJvEG?/d8ryiNUggzUEki86GQuM5=095r3etYmie4Yp59j4pVm2?5YULIuF5P!YUqPb0pe8LNLz7JkEBN9TMpG9kQ7HRZbrrycP9QjEzgbAM!v2drsy6vXRtBIhj?llXmqFHeXvWCYUxB4p6-JH!j-143tUq?wMZIr6k7WUzA0JjuTt/JBl0OunudtlKeidKkcGx!spUlCRWitnQDfPEaFti/xLavb97XWXmtwaOF2vnv69DncJfu1EOjrEX-?ZTBL?zi6v/4H7-EqsZ?TIpgj40ZiZ-ria9LIhDnbdbxP?xzngzgxmOsaHBd9Jru=Uc1evzaKz8Q2!C60Q-uuvv0JXFvd?VJ=eCFZDHm24H"

	// consulta values
	misionesTipoDominio   = "1"
	misionesTipoDocumento = "0"
)

type MisionesScraperProvider struct {
	client      *http.Client
	solver      captchasolver.Solver
	cachedToken string
	tokenExpiry time.Time
	mu          sync.Mutex
}

func NewMisionesScraperProvider(solver captchasolver.Solver) *MisionesScraperProvider {
	return &MisionesScraperProvider{
		client: &http.Client{Timeout: 10 * time.Second},
		solver: solver,
	}
}

func (p *MisionesScraperProvider) Name() string  { return "misiones_scraper" }
func (p *MisionesScraperProvider) Priority() int { return 32 }
func (p *MisionesScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "misiones" {
		return false
	}
	return strings.TrimSpace(query.Plate) != "" || strings.TrimSpace(query.Document) != ""
}

func (p *MisionesScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	values := url.Values{}
	if plate := strings.ToUpper(strings.TrimSpace(query.Plate)); plate != "" {
		values.Set("consulta", misionesTipoDominio)
		values.Set("id", plate)
	} else {
		values.Set("consulta", misionesTipoDocumento)
		values.Set("id", strings.TrimSpace(query.Document))
	}
	values.Set("pagina", "1")
	values.Set("page", "1")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, misionesAPIURL, strings.NewReader(values.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build misiones request"}
	}
	// Token priority: per-request override → cached/solved → hardcoded fallback
	token := strings.TrimSpace(query.MisionesCaptchaToken)
	if token == "" {
		token = p.resolveToken()
	}
	if token == "" {
		token = misionesAuthToken
	}

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("Origin", "https://monitoreovialmisiones.info")
	req.Header.Set("Referer", "https://monitoreovialmisiones.info/")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FlotaBot/1.0)")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "misiones request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "misiones request failed"}
	}
	defer resp.Body.Close()

	// 401 means the token has rotated — return empty gracefully.
	if resp.StatusCode == http.StatusUnauthorized {
		return core.FineResult{
			Fines:      []core.Fine{},
			Total:      0,
			Source:     p.Name(),
			Confidence: "low",
			FetchedAt:  time.Now(),
		}, nil
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to read misiones response"}
	}

	fines := parseMisionesResponse(body, strings.ToUpper(strings.TrimSpace(query.Plate)))
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

// resolveToken returns a valid reCAPTCHA token, using the cache when possible.
func (p *MisionesScraperProvider) resolveToken() string {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cachedToken != "" && time.Now().Before(p.tokenExpiry) {
		return p.cachedToken
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	token, err := p.solver.Solve(ctx, misionesSiteKey, misionesSiteURL)
	if err != nil || token == "" {
		return ""
	}
	p.cachedToken = token
	p.tokenExpiry = time.Now().Add(90 * time.Second)
	return token
}

// — response structs —

type misionesEnvelope struct {
	Datos *struct {
		Data  []misionesFine `json:"data"`
		Total int            `json:"total"`
	} `json:"datos"`
}

type misionesFine struct {
	IDInfraccion    int     `json:"idInfraccion"`
	IDTipoInfraccion int    `json:"idTipoInfraccion"`
	Dominio         string  `json:"dominio"`
	FechaInfraccion string  `json:"fecha_infraccion"`
	Estado          string  `json:"estado"`
	MontoAPagar     float64 `json:"monto_a_pagar"`
	Descripcion     string  `json:"descripcion"`
	NumeroActa      string  `json:"numero_acta"`
	Vto             string  `json:"vto"`
	Zona            string  `json:"zona"`
}

func parseMisionesResponse(body []byte, plate string) []core.Fine {
	var env misionesEnvelope
	if err := json.Unmarshal(body, &env); err != nil || env.Datos == nil {
		return []core.Fine{}
	}

	out := make([]core.Fine, 0, len(env.Datos.Data))
	for _, row := range env.Datos.Data {
		jurisdiction := "Misiones"
		if z := strings.TrimSpace(row.Zona); z != "" {
			jurisdiction = "Misiones - " + z
		}

		status := strings.ToUpper(strings.TrimSpace(row.Estado))
		if status == "" {
			status = "PENDIENTE"
		}

		p := plate
		if p == "" {
			p = strings.ToUpper(strings.TrimSpace(row.Dominio))
		}

		issuedAt := misionesParseDate(row.FechaInfraccion)
		dueAt := misionesParseDate(row.Vto)

		out = append(out, core.Fine{
			Vehicle:      core.VehicleInfo{Plate: p},
			Jurisdiction: jurisdiction,
			Offense:      strings.TrimSpace(row.Descripcion),
			Amount:       row.MontoAPagar,
			Currency:     "ARS",
			Status:       status,
			IssuedAt:     issuedAt,
			DueAt:        dueAt,
			Source:       "misiones_scraper",
			SourceRef:    misionesFormatRef(row.IDInfraccion, row.NumeroActa),
		})
	}
	return out
}

func misionesFormatRef(id int, nroActa string) string {
	if nroActa = strings.TrimSpace(nroActa); nroActa != "" {
		return nroActa
	}
	if id != 0 {
		return strconv.Itoa(id)
	}
	return ""
}

func misionesParseDate(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}
	}
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		loc = time.FixedZone("-03", -3*60*60)
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, raw, loc); err == nil {
			return t
		}
	}
	return time.Time{}
}
