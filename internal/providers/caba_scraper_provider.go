package providers

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"flota/internal/core"
)

const (
	cabaFormURL = "https://buenosaires.gob.ar/licenciasdeconducir/consulta-de-infracciones/index.php"
	cabaReferer = "https://buenosaires.gob.ar/licenciasdeconducir/consulta-de-infracciones/?actas=transito"
)

var (
	rowCheckboxJSONRx = regexp.MustCompile(`data-json="([^"]+)"`)
	noFinesRx         = regexp.MustCompile(`(?i)No registr[aá]s infracciones`)
	actaHeaderRx      = regexp.MustCompile(`(?s)<span class="collapse-label">Acta N°\s*([^<\s]+)\s*-\s*([^<]+)</span>\s*<h4 class="collapse-title">([^<]*)</h4>`)
	subtitleAmountRx  = regexp.MustCompile(`(?s)<span class="collapse-subtitle">([^<]+)</span>`)
	lugarRx           = regexp.MustCompile(`(?s)<h5>\s*Lugar:\s*</h5>\s*<span>\s*([^<]+)\s*</span>`)
	controllerRx      = regexp.MustCompile(`(?s)<h5>\s*Controlador:\s*</h5>\s*<span>\s*([^<]*)\s*</span>`)
)

type CABAScraperProvider struct {
	client *http.Client
}

func NewCABAScraperProvider() *CABAScraperProvider {
	return &CABAScraperProvider{
		client: &http.Client{
			Timeout: 8 * time.Second,
		},
	}
}

func (p *CABAScraperProvider) Name() string  { return "caba_scraper" }
func (p *CABAScraperProvider) Priority() int { return 20 }
func (p *CABAScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "caba" {
		return false
	}
	return strings.TrimSpace(query.Plate) != "" || strings.TrimSpace(query.Document) != ""
}

func (p *CABAScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	values := url.Values{}
	values.Set("filtro_acta", "transito")
	// El endpoint actualmente responde sin token válido en pruebas.
	values.Set("g-recaptcha-response", "test")

	if strings.TrimSpace(query.Plate) != "" {
		values.Set("tipo_consulta", "Dominio")
		values.Set("dominio", strings.ToUpper(strings.TrimSpace(query.Plate)))
	} else {
		values.Set("tipo_consulta", "Documento")
		values.Set("tipo_doc", "DNI")
		values.Set("doc", strings.TrimSpace(query.Document))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, cabaFormURL, strings.NewReader(values.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build caba request"}
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FlotaBot/1.0)")
	req.Header.Set("Referer", cabaReferer)

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "caba request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "caba request failed"}
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to read caba response"}
	}
	htmlBody := string(body)
	if noFinesRx.MatchString(htmlBody) {
		return core.FineResult{
			Fines:      []core.Fine{},
			Total:      0,
			Source:     p.Name(),
			Confidence: "medium",
			FetchedAt:  time.Now(),
		}, nil
	}

	fines, err := parseCABAActas(htmlBody)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to parse caba response"}
	}
	if len(fines) == 0 {
		return core.FineResult{
			Fines:      []core.Fine{},
			Total:      0,
			Source:     p.Name(),
			Confidence: "low",
			FetchedAt:  time.Now(),
		}, nil
	}
	if plate := strings.ToUpper(strings.TrimSpace(query.Plate)); plate != "" {
		for i := range fines {
			fines[i].Vehicle.Plate = plate
		}
	}
	return core.FineResult{
		Fines:      fines,
		Total:      len(fines),
		Source:     p.Name(),
		Confidence: "high",
		FetchedAt:  time.Now(),
	}, nil
}

type cabaActaJSON struct {
	NumeroActa   string `json:"numeroActa"`
	FechaActa    string `json:"fechaActa"`
	MontoActa    string `json:"montoActa"`
	Estado       string `json:"estadoReducidoActa"`
	Infracciones []struct {
		Desc  string `json:"desc"`
		Lugar string `json:"lugar"`
	} `json:"infracciones"`
}

type cabaActaMeta struct {
	Controller string
	HasPhoto   bool
}

func parseCABAActas(page string) ([]core.Fine, error) {
	out := make([]core.Fine, 0, 16)
	seen := map[string]struct{}{}
	metaByRef := map[string]cabaActaMeta{}

	headerIdx := actaHeaderRx.FindAllStringSubmatchIndex(page, -1)
	for i, idx := range headerIdx {
		if len(idx) < 8 {
			continue
		}
		ref := strings.TrimSpace(html.UnescapeString(page[idx[2]:idx[3]]))
		start := idx[0]
		end := len(page)
		if i+1 < len(headerIdx) {
			end = headerIdx[i+1][0]
		}
		segment := page[start:end]
		metaByRef[ref] = cabaActaMeta{
			Controller: extractController(segment),
			HasPhoto:   strings.Contains(segment, `descargar_imagen_pdf`) && strings.Contains(segment, `data-acta="`+ref+`"`),
		}
	}

	// 1) Actas con data-json (seleccionables).
	jsonMatches := rowCheckboxJSONRx.FindAllStringSubmatch(page, -1)
	for _, m := range jsonMatches {
		if len(m) < 2 {
			continue
		}
		raw := html.UnescapeString(m[1])
		var acta cabaActaJSON
		if err := json.Unmarshal([]byte(raw), &acta); err != nil {
			continue
		}

		amount := parseAmount(acta.MontoActa)
		issuedAt := parseCABAFecha(acta.FechaActa)
		offense := ""
		jurisdiction := "CABA"
		if len(acta.Infracciones) > 0 {
			offense = strings.TrimSpace(acta.Infracciones[0].Desc)
			if l := strings.TrimSpace(acta.Infracciones[0].Lugar); l != "" {
				jurisdiction = "CABA - " + l
			}
		}

		ref := strings.TrimSpace(acta.NumeroActa)
		meta := metaByRef[ref]
		status := strings.ToUpper(strings.TrimSpace(acta.Estado))
		hasPhoto := meta.HasPhoto
		if strings.Contains(status, "PAGO VOLUNTARIO") {
			hasPhoto = true
		}
		out = append(out, core.Fine{
			Vehicle:      core.VehicleInfo{Plate: ""},
			Jurisdiction: jurisdiction,
			Offense:      offense,
			Amount:       amount,
			Currency:     "ARS",
			Status:       status,
			Controller:   meta.Controller,
			HasPhoto:     hasPhoto,
			IssuedAt:     issuedAt,
			Source:       "caba_scraper",
			SourceRef:    ref,
		})
		if ref != "" {
			seen[ref] = struct{}{}
		}
	}

	// 2) Actas no seleccionables (ej: "Se debe resolver con un controlador").
	for i, idx := range headerIdx {
		if len(idx) < 8 {
			continue
		}
		ref := strings.TrimSpace(html.UnescapeString(page[idx[2]:idx[3]]))
		if ref != "" {
			if _, ok := seen[ref]; ok {
				continue
			}
		}

		fecha := strings.TrimSpace(html.UnescapeString(page[idx[4]:idx[5]]))
		offense := strings.TrimSpace(html.UnescapeString(page[idx[6]:idx[7]]))

		start := idx[0]
		end := len(page)
		if i+1 < len(headerIdx) {
			end = headerIdx[i+1][0]
		}
		segment := page[start:end]

		jurisdiction := "CABA"
		if place := lugarRx.FindStringSubmatch(segment); len(place) >= 2 {
			jurisdiction = "CABA - " + strings.TrimSpace(html.UnescapeString(place[1]))
		}
		status := "PENDING"
		if strings.Contains(strings.ToLower(segment), "se debe resolver con un controlador") {
			status = "CONTROLLER_REQUIRED"
		}
		meta := metaByRef[ref]
		amount := 0.0
		if amt := subtitleAmountRx.FindStringSubmatch(segment); len(amt) >= 2 {
			amount = parseAmount(html.UnescapeString(amt[1]))
		}

		out = append(out, core.Fine{
			Vehicle:      core.VehicleInfo{Plate: ""},
			Jurisdiction: jurisdiction,
			Offense:      offense,
			Amount:       amount,
			Currency:     "ARS",
			Status:       status,
			Controller:   meta.Controller,
			HasPhoto:     meta.HasPhoto,
			IssuedAt:     parseCABAFecha(fecha),
			Source:       "caba_scraper",
			SourceRef:    ref,
		})
		if ref != "" {
			seen[ref] = struct{}{}
		}
	}

	return out, nil
}

func extractController(segment string) string {
	match := controllerRx.FindStringSubmatch(segment)
	if len(match) < 2 {
		return ""
	}
	value := strings.TrimSpace(html.UnescapeString(match[1]))
	if value == "" {
		return ""
	}
	return value
}

func parseAmount(raw string) float64 {
	clean := strings.TrimSpace(raw)
	clean = strings.ReplaceAll(clean, "$", "")
	clean = strings.ReplaceAll(clean, " ", "")
	if strings.Contains(clean, ".") && strings.Contains(clean, ",") {
		clean = strings.ReplaceAll(clean, ".", "")
		clean = strings.ReplaceAll(clean, ",", ".")
	} else {
		clean = strings.ReplaceAll(clean, ",", ".")
	}
	v, err := strconv.ParseFloat(clean, 64)
	if err != nil {
		return 0
	}
	return v
}

func parseCABAFecha(raw string) time.Time {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Now()
	}
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		loc = time.FixedZone("-03", -3*60*60)
	}
	t, err := time.ParseInLocation("2006-01-02 15:04", raw, loc)
	if err != nil {
		return time.Now()
	}
	return t
}
