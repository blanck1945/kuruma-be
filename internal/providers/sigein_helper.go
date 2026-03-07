package providers

import (
	"context"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"flota/internal/core"
)

const sigeinUserAgent = "Mozilla/5.0 (compatible; FlotaBot/1.0)"

var (
	sigeinViewStateRx = regexp.MustCompile(`id="__VIEWSTATE"\s+value="([^"]*)"`)
	sigeinEvValidRx   = regexp.MustCompile(`id="__EVENTVALIDATION"\s+value="([^"]*)"`)
	sigeinVSGenRx     = regexp.MustCompile(`id="__VIEWSTATEGENERATOR"\s+value="([^"]*)"`)
	sigeinNoFinesRx   = regexp.MustCompile(`(?i)No (se encontraron|registra|posee) infrac`)
	sigeinRowRx       = regexp.MustCompile(`(?s)<tr[^>]*class="[^"]*(?:fila|row|item|GridRow|alt)[^"]*"[^>]*>(.*?)</tr>`)
	sigeinCellRx      = regexp.MustCompile(`<td[^>]*>\s*([^<\s][^<]*?)\s*</td>`)
	sigeinFechaRx     = regexp.MustCompile(`\b(\d{2}/\d{2}/\d{4})\b`)
	sigeinImporteRx   = regexp.MustCompile(`\$?\s*([\d.,]+)`)
	sigeinEstadoRx    = regexp.MustCompile(`(?i)(PENDIENTE|PAGADO?|ANULAD[AO]|JUDICIAL|LIBRE)`)
)

// fetchSIGEIN performs the GET (ViewState) + POST (search) flow for any SIGEIN subdomain.
// baseURL example: "https://riotercero.sigein.net/home.aspx"
func fetchSIGEIN(ctx context.Context, client *http.Client, baseURL, plate, jurisdiction string) ([]core.Fine, error) {
	// Step 1: GET to obtain ASP.NET hidden fields.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL, nil)
	if err != nil {
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "sigein: failed to build GET request for " + jurisdiction}
	}
	getReq.Header.Set("User-Agent", sigeinUserAgent)

	resp, err := client.Do(getReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, core.DomainError{Code: core.ErrProviderTimeout, Message: "sigein: GET timeout for " + jurisdiction}
		}
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "sigein: GET failed for " + jurisdiction}
	}
	pageBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	pageBody := string(pageBytes)

	viewState := sigeinExtractField(sigeinViewStateRx, pageBody)
	eventValid := sigeinExtractField(sigeinEvValidRx, pageBody)
	vsGenerator := sigeinExtractField(sigeinVSGenRx, pageBody)
	if vsGenerator == "" {
		vsGenerator = "8D0E13E6"
	}

	// Step 2: POST with form-encoded data.
	values := url.Values{}
	values.Set("__EVENTTARGET", "")
	values.Set("__EVENTARGUMENT", "")
	values.Set("__VIEWSTATE", viewState)
	values.Set("__VIEWSTATEGENERATOR", vsGenerator)
	values.Set("__EVENTVALIDATION", eventValid)
	values.Set("tbPatente", plate)
	values.Set("btnConsultaDominio", "REALIZAR CONSULTA")
	values.Set("ddlTipoDni", "")
	values.Set("tbNumero", "")
	values.Set("group1", "chkMasculino")

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, strings.NewReader(values.Encode()))
	if err != nil {
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "sigein: failed to build POST request for " + jurisdiction}
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Referer", baseURL)
	postReq.Header.Set("User-Agent", sigeinUserAgent)

	postResp, err := client.Do(postReq)
	if err != nil {
		if ctx.Err() != nil {
			return nil, core.DomainError{Code: core.ErrProviderTimeout, Message: "sigein: POST timeout for " + jurisdiction}
		}
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "sigein: POST failed for " + jurisdiction}
	}
	defer postResp.Body.Close()

	body, err := io.ReadAll(postResp.Body)
	if err != nil {
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "sigein: failed to read response for " + jurisdiction}
	}

	htmlBody := html.UnescapeString(string(body))

	if sigeinNoFinesRx.MatchString(htmlBody) {
		return []core.Fine{}, nil
	}

	fines := parseSIGEINActas(htmlBody, plate, jurisdiction)
	return fines, nil
}

func sigeinExtractField(rx *regexp.Regexp, htmlStr string) string {
	if m := rx.FindStringSubmatch(htmlStr); len(m) > 1 {
		return m[1]
	}
	return ""
}

func parseSIGEINActas(page, plate, jurisdiction string) []core.Fine {
	rows := sigeinRowRx.FindAllStringSubmatch(page, -1)
	if len(rows) == 0 {
		return []core.Fine{}
	}
	out := make([]core.Fine, 0, len(rows))
	for _, row := range rows {
		if len(row) < 2 {
			continue
		}
		fine := parseSIGEINRow(row[1], plate, jurisdiction)
		out = append(out, fine)
	}
	return out
}

func parseSIGEINRow(segment, plate, jurisdiction string) core.Fine {
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
		Vehicle:      core.VehicleInfo{Plate: plate},
		Jurisdiction: jurisdiction,
		Offense:      offense,
		Amount:       amount,
		Currency:     "ARS",
		Status:       status,
		IssuedAt:     issuedAt,
		Source:       strings.ToLower(strings.ReplaceAll(jurisdiction, " ", "")) + "_scraper",
	}
}

func parseSIGEINFecha(raw string) time.Time {
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
