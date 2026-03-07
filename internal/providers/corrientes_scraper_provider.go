package providers

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"flota/internal/core"
)

const (
	corrHomeURL   = "https://corrientes.sigein.net/home.aspx"
	corrUserAgent = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

var (
	corrViewStateRx = regexp.MustCompile(`id="__VIEWSTATE"\s+value="([^"]*)"`)
	corrEvValidRx   = regexp.MustCompile(`id="__EVENTVALIDATION"\s+value="([^"]*)"`)
	corrVSGenRx     = regexp.MustCompile(`id="__VIEWSTATEGENERATOR"\s+value="([^"]*)"`)
	corrNoFinesRx   = regexp.MustCompile(`(?i)No (se encontraron|registra|posee) infrac`)
	corrRowRx       = regexp.MustCompile(`(?s)<tr[^>]*class="[^"]*(?:fila|row|item|GridRow|alt)[^"]*"[^>]*>(.*?)</tr>`)
	corrCellRx      = regexp.MustCompile(`<td[^>]*>\s*([^<\s][^<]*?)\s*</td>`)
	corrFechaRx     = regexp.MustCompile(`\b(\d{2}/\d{2}/\d{4})\b`)
	corrImporteRx   = regexp.MustCompile(`\$?\s*([\d.,]+)`)
	corrEstadoRx    = regexp.MustCompile(`(?i)(PENDIENTE|PAGADO?|ANULAD[AO]|JUDICIAL|LIBRE)`)
)

type CorrientesScraperProvider struct {
	client *http.Client
}

func NewCorrientesScraperProvider() *CorrientesScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &CorrientesScraperProvider{
		client: &http.Client{Timeout: 12 * time.Second, Jar: jar},
	}
}

func (p *CorrientesScraperProvider) Name() string  { return "corrientes_scraper" }
func (p *CorrientesScraperProvider) Priority() int { return 33 }
func (p *CorrientesScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "corrientes" {
		return false
	}
	return strings.TrimSpace(query.Plate) != "" || strings.TrimSpace(query.Document) != ""
}

func (p *CorrientesScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	// Step 1: GET to obtain ASP.NET ViewState / EventValidation.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, corrHomeURL, nil)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build corrientes session request"}
	}
	getReq.Header.Set("User-Agent", corrUserAgent)

	resp, err := p.client.Do(getReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "corrientes session request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "corrientes session request failed"}
	}
	pageBody, _ := io.ReadAll(resp.Body)
	resp.Body.Close()

	viewState   := corrExtractField(corrViewStateRx, string(pageBody))
	eventValid  := corrExtractField(corrEvValidRx, string(pageBody))
	vsGenerator := corrExtractField(corrVSGenRx, string(pageBody))
	if vsGenerator == "" {
		vsGenerator = "8D0E13E6"
	}

	// Step 2: POST with form data.
	values := url.Values{}
	values.Set("__EVENTTARGET", "")
	values.Set("__EVENTARGUMENT", "")
	values.Set("__VIEWSTATE", viewState)
	values.Set("__VIEWSTATEGENERATOR", vsGenerator)
	values.Set("__EVENTVALIDATION", eventValid)

	if plate := strings.ToUpper(strings.TrimSpace(query.Plate)); plate != "" {
		values.Set("tbPatente", plate)
		values.Set("btnConsultaDominio", "REALIZAR CONSULTA")
		values.Set("ddlTipoDni", "")
		values.Set("tbNumero", "")
		values.Set("group1", "chkMasculino")
	} else {
		values.Set("tbPatente", "")
		values.Set("ddlTipoDni", "DNI")
		values.Set("tbNumero", strings.TrimSpace(query.Document))
		values.Set("group1", "chkMasculino")
		values.Set("btnConsultaDni", "REALIZAR CONSULTA")
	}

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, corrHomeURL, strings.NewReader(values.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build corrientes search request"}
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Referer", corrHomeURL)
	postReq.Header.Set("User-Agent", corrUserAgent)

	postResp, err := p.client.Do(postReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "corrientes search request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "corrientes search request failed"}
	}
	defer postResp.Body.Close()

	body, err := io.ReadAll(postResp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to read corrientes response"}
	}

	htmlBody := html.UnescapeString(string(body))

	if corrNoFinesRx.MatchString(htmlBody) {
		return core.FineResult{
			Fines:      []core.Fine{},
			Total:      0,
			Source:     p.Name(),
			Confidence: "medium",
			FetchedAt:  time.Now(),
		}, nil
	}

	fines := parseCorrientesActas(htmlBody)
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

func corrExtractField(rx *regexp.Regexp, htmlStr string) string {
	if m := rx.FindStringSubmatch(htmlStr); len(m) > 1 {
		return m[1]
	}
	return ""
}

func parseCorrientesActas(page string) []core.Fine {
	rows := corrRowRx.FindAllStringSubmatch(page, -1)
	if len(rows) == 0 {
		return []core.Fine{}
	}

	out := make([]core.Fine, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		fine := parseCorrientesRow(row[1])
		out = append(out, fine)
	}
	return out
}

func parseCorrientesRow(segment string) core.Fine {
	issuedAt := time.Now()
	if m := corrFechaRx.FindStringSubmatch(segment); len(m) >= 2 {
		issuedAt = parseCorrientesFecha(m[1])
	}

	amount := 0.0
	if m := corrImporteRx.FindStringSubmatch(segment); len(m) >= 2 {
		amount = parseAmount(m[1])
	}

	status := "PENDIENTE"
	if m := corrEstadoRx.FindStringSubmatch(segment); len(m) >= 2 {
		status = strings.ToUpper(m[1])
	}

	offense := ""
	cells := corrCellRx.FindAllStringSubmatch(segment, -1)
	for _, c := range cells {
		if len(c) < 2 {
			continue
		}
		val := strings.TrimSpace(c[1])
		if corrFechaRx.MatchString(val) || corrImporteRx.MatchString(val) || corrEstadoRx.MatchString(val) {
			continue
		}
		if len(val) > 5 {
			offense = val
			break
		}
	}

	return core.Fine{
		Vehicle:      core.VehicleInfo{Plate: ""},
		Jurisdiction: "Corrientes",
		Offense:      offense,
		Amount:       amount,
		Currency:     "ARS",
		Status:       status,
		IssuedAt:     issuedAt,
		Source:       "corrientes_scraper",
	}
}

func parseCorrientesFecha(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now()
	}
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		loc = time.FixedZone("-03", -3*60*60)
	}
	t, err := time.ParseInLocation("02/01/2006", raw, loc)
	if err != nil {
		return time.Now()
	}
	return t
}
