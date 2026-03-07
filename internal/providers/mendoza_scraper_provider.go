package providers

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"sync"
	"time"

	"flota/internal/core"
	"flota/internal/providers/captchasolver"
)

const (
	mendozaBaseURL = "https://sistemas.seguridad.mendoza.gov.ar/webvialcaminera/servlet/com.pagosdeuda.wpdeudaonline"
	mendozaSiteKey = "6LfeW24UAAAAAMK3zvl5B4MfXM2-CSnVcxUNtWxm"
	mendozaSiteURL = "https://sistemas.seguridad.mendoza.gov.ar/"
	mendozaUA      = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

var (
	mendozaGXStateRx = regexp.MustCompile(`name="GXState"\s+value='({.*?})'`)
	mendozaNoFinesRx = regexp.MustCompile(`(?i)no (se encontr|registra|posee|tiene).{0,30}multa`)
)

type MendozaScraperProvider struct {
	client      *http.Client
	solver      captchasolver.Solver
	cachedToken string
	tokenExpiry time.Time
	mu          sync.Mutex
}

func NewMendozaScraperProvider(solver captchasolver.Solver) *MendozaScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &MendozaScraperProvider{
		client: &http.Client{Timeout: 45 * time.Second, Jar: jar},
		solver: solver,
	}
}

func (p *MendozaScraperProvider) Name() string  { return "mendoza_scraper" }
func (p *MendozaScraperProvider) Priority() int { return 48 }
func (p *MendozaScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "mendoza" {
		return false
	}
	return strings.TrimSpace(query.Plate) != "" || strings.TrimSpace(query.Document) != ""
}

func (p *MendozaScraperProvider) resolveToken(override string) string {
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
	token, err := p.solver.Solve(ctx, mendozaSiteKey, mendozaSiteURL)
	if err != nil || token == "" {
		return ""
	}
	p.cachedToken = token
	p.tokenExpiry = time.Now().Add(90 * time.Second)
	return token
}

func (p *MendozaScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(query.Plate))
	doc := strings.TrimSpace(query.Document)

	identifier := plate
	eleccion := "DOMINIO"
	if identifier == "" {
		identifier = doc
		eleccion = "DNI"
	}

	// Step 1: GET initial page to extract GXState.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, mendozaBaseURL, nil)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "mendoza: failed to build GET request"}
	}
	getReq.Header.Set("User-Agent", mendozaUA)

	getResp, err := p.client.Do(getReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "mendoza: GET timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "mendoza: GET failed"}
	}
	pageBytes, _ := io.ReadAll(getResp.Body)
	getResp.Body.Close()

	gxStateRaw := mendozaExtractGXState(string(pageBytes))
	if gxStateRaw == "" {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "mendoza: could not extract GXState"}
	}

	// Step 2: Solve captcha.
	captchaToken := p.resolveToken(query.MendozaCaptchaToken)
	if captchaToken == "" {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "mendoza: failed to solve captcha"}
	}

	// Step 3: Inject captcha token and search params into GXState.
	var gxState map[string]interface{}
	if err := json.Unmarshal([]byte(gxStateRaw), &gxState); err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "mendoza: failed to parse GXState"}
	}
	gxState["GPXRECAPTCHA1_Response"] = captchaToken
	gxState["vELECCION"] = eleccion
	gxState["vOJTIDENTIFICADOR1"] = identifier

	gxStateJSON, err := json.Marshal(gxState)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "mendoza: failed to serialize GXState"}
	}

	// Step 4: POST form with modified GXState.
	form := url.Values{}
	form.Set("GXState", string(gxStateJSON))
	form.Set("vOJTIDENTIFICADOR1", identifier)
	form.Set("CONSULTARDEUDA", "CONSULTAR DEUDA")

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, mendozaBaseURL, strings.NewReader(form.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "mendoza: failed to build POST request"}
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("User-Agent", mendozaUA)
	postReq.Header.Set("Referer", mendozaBaseURL)
	postReq.Header.Set("Origin", mendozaSiteURL)

	postResp, err := p.client.Do(postReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "mendoza: POST timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "mendoza: POST failed"}
	}
	defer postResp.Body.Close()

	body, err := io.ReadAll(postResp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "mendoza: failed to read response"}
	}

	fines := parseMendozaHTML(body, identifier)
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

func mendozaExtractGXState(pageHTML string) string {
	if m := mendozaGXStateRx.FindStringSubmatch(pageHTML); len(m) > 1 {
		return m[1]
	}
	return ""
}

func parseMendozaHTML(body []byte, identifier string) []core.Fine {
	htmlBody := html.UnescapeString(string(body))
	if mendozaNoFinesRx.MatchString(htmlBody) {
		return []core.Fine{}
	}

	rows := sigeinRowRx.FindAllStringSubmatch(htmlBody, -1)
	if len(rows) == 0 {
		return []core.Fine{}
	}

	out := make([]core.Fine, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		out = append(out, parseMendozaRow(row[1], identifier))
	}
	return out
}

func parseMendozaRow(segment, identifier string) core.Fine {
	issuedAt := time.Now()
	if m := sigeinFechaRx.FindStringSubmatch(segment); len(m) >= 2 {
		issuedAt = parseSIGEINFecha(m[1])
	}

	amount := 0.0
	if m := sigeinImporteRx.FindStringSubmatch(segment); len(m) >= 2 {
		amount = parseAmount(m[1])
	}

	status := "PENDIENTE"
	if m := sigeinEstadoRx.FindStringSubmatch(segment); len(m) >= 2 {
		status = strings.ToUpper(m[1])
	}

	offense := ""
	cells := sigeinCellRx.FindAllStringSubmatch(segment, -1)
	for _, c := range cells {
		if len(c) < 2 {
			continue
		}
		val := strings.TrimSpace(c[1])
		if sigeinFechaRx.MatchString(val) || sigeinImporteRx.MatchString(val) || sigeinEstadoRx.MatchString(val) {
			continue
		}
		if len(val) > 5 {
			offense = val
			break
		}
	}

	return core.Fine{
		Vehicle:      core.VehicleInfo{Plate: identifier},
		Jurisdiction: "Mendoza",
		Offense:      offense,
		Amount:       amount,
		Currency:     "ARS",
		Status:       status,
		IssuedAt:     issuedAt,
		Source:       "mendoza_scraper",
	}
}
