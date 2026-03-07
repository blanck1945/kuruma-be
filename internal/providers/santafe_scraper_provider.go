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
	sfBaseURL  = "https://tribunalweb.santafeciudad.gov.ar/"
	sfActaURL  = "https://tribunalweb.santafeciudad.gov.ar/acta.do"
	sfReferer  = "https://tribunalweb.santafeciudad.gov.ar/"
	sfUserAgent = "Mozilla/5.0 (compatible; FlotaBot/1.0)"
)

var (
	sfNoFinesRx  = regexp.MustCompile(`(?i)No se encontraron multas`)
	sfRowRx      = regexp.MustCompile(`itemActa\[(\d+)\]\.check`)
	sfHiddenRx   = regexp.MustCompile(`name="itemActa\[\d+\]\.(\w+)"\s+value="([^"]*)"`)
	sfCellRx     = regexp.MustCompile(`class="(?:datos|celda)"[^>]*>\s*([^<\s][^<]*?)\s*<`)
	sfFechaRx    = regexp.MustCompile(`\b(\d{2}/\d{2}/\d{4})\b`)
	sfImporteRx  = regexp.MustCompile(`\$\s*([\d.,]+)`)
	sfEstadoRx   = regexp.MustCompile(`(?i)(PENDIENTE|PAGADO|ANULADO|JUDICIAL|LIBRE DE MULTA)`)
)

type SantaFeScraperProvider struct {
	client *http.Client
}

func NewSantaFeScraperProvider() *SantaFeScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &SantaFeScraperProvider{
		client: &http.Client{Timeout: 10 * time.Second, Jar: jar},
	}
}

func (p *SantaFeScraperProvider) Name() string  { return "santafe_scraper" }
func (p *SantaFeScraperProvider) Priority() int { return 28 }
func (p *SantaFeScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "santafe" {
		return false
	}
	return strings.TrimSpace(query.Plate) != "" || strings.TrimSpace(query.Document) != ""
}

func (p *SantaFeScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	// Step 1: GET base URL to establish JSESSIONID cookie.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, sfBaseURL, nil)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build santafe session request"}
	}
	getReq.Header.Set("User-Agent", sfUserAgent)
	resp, err := p.client.Do(getReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "santafe session request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "santafe session request failed"}
	}
	resp.Body.Close()

	// Step 2: POST to search for fines.
	values := url.Values{}
	values.Set("accion", "buscarActas")
	if doc := strings.TrimSpace(query.Document); doc != "" {
		values.Set("tipoBusqueda", "1")
		values.Set("documento", doc)
		values.Set("dominio", "")
	} else {
		values.Set("tipoBusqueda", "2")
		values.Set("documento", "")
		values.Set("dominio", strings.ToUpper(strings.TrimSpace(query.Plate)))
	}

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sfActaURL, strings.NewReader(values.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build santafe search request"}
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Referer", sfReferer)
	postReq.Header.Set("User-Agent", sfUserAgent)

	postResp, err := p.client.Do(postReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "santafe search request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "santafe search request failed"}
	}
	defer postResp.Body.Close()

	body, err := io.ReadAll(postResp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to read santafe response"}
	}

	// The server responds in iso-8859-1; html.UnescapeString handles HTML entities.
	htmlBody := html.UnescapeString(string(body))

	if sfNoFinesRx.MatchString(htmlBody) {
		return core.FineResult{
			Fines:      []core.Fine{},
			Total:      0,
			Source:     p.Name(),
			Confidence: "medium",
			FetchedAt:  time.Now(),
		}, nil
	}

	fines := parseSantaFeActas(htmlBody)
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

func parseSantaFeActas(page string) []core.Fine {
	// Find positions of each row marker (one per fine).
	rowIdxs := sfRowRx.FindAllStringIndex(page, -1)
	if len(rowIdxs) == 0 {
		return []core.Fine{}
	}

	out := make([]core.Fine, 0, len(rowIdxs))

	for i, start := range rowIdxs {
		end := len(page)
		if i+1 < len(rowIdxs) {
			end = rowIdxs[i+1][0]
		}
		segment := page[start[0]:end]

		fine := parseSantaFeSegment(segment)
		out = append(out, fine)
	}
	return out
}

func parseSantaFeSegment(segment string) core.Fine {
	// Extract hidden inputs (nroActa, ref, etc.).
	sourceRef := ""
	hiddenMatches := sfHiddenRx.FindAllStringSubmatch(segment, -1)
	for _, m := range hiddenMatches {
		if len(m) < 3 {
			continue
		}
		field := strings.ToLower(m[1])
		val := strings.TrimSpace(m[2])
		if field == "nroacta" || field == "ref" || field == "id" {
			if val != "" {
				sourceRef = val
			}
		}
	}

	// Extract fecha.
	issuedAt := time.Now()
	if fechaM := sfFechaRx.FindStringSubmatch(segment); len(fechaM) >= 2 {
		issuedAt = parseSantaFeFecha(fechaM[1])
	}

	// Extract importe.
	amount := 0.0
	if importeM := sfImporteRx.FindStringSubmatch(segment); len(importeM) >= 2 {
		amount = parseAmount(importeM[1])
	}

	// Extract estado.
	status := "PENDIENTE"
	if estadoM := sfEstadoRx.FindStringSubmatch(segment); len(estadoM) >= 2 {
		status = strings.ToUpper(estadoM[1])
	}

	// Extract description from table cells.
	offense := ""
	cells := sfCellRx.FindAllStringSubmatch(segment, -1)
	for _, c := range cells {
		if len(c) < 2 {
			continue
		}
		val := strings.TrimSpace(c[1])
		// Skip values that look like dates, amounts, or status keywords.
		if sfFechaRx.MatchString(val) || sfImporteRx.MatchString(val) || sfEstadoRx.MatchString(val) {
			continue
		}
		if len(val) > 5 {
			offense = val
			break
		}
	}

	return core.Fine{
		Vehicle:      core.VehicleInfo{Plate: ""},
		Jurisdiction: "Santa Fe",
		Offense:      offense,
		Amount:       amount,
		Currency:     "ARS",
		Status:       status,
		IssuedAt:     issuedAt,
		Source:       "santafe_scraper",
		SourceRef:    sourceRef,
	}
}

func parseSantaFeFecha(raw string) time.Time {
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
