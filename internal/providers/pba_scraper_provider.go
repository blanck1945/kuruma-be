package providers

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"flota/internal/core"
	"flota/internal/providers/captchasolver"
)

const (
	pbaConsultaURL         = "https://infraccionesba.gba.gob.ar/consulta-infraccion"
	pbaConsultarRestURL    = "https://infraccionesba.gba.gob.ar/rest/consultar-infraccion"
	pbaDefaultCaptchaToken = "test"
)

var (
	pbaCSRFTokenRx  = regexp.MustCompile(`<meta name="_csrf"\s+content="([^"]+)"`)
	pbaCSRFHeaderRx = regexp.MustCompile(`<meta name="_csrf_header"\s+content="([^"]+)"`)
	pbaSiteKeyRx    = regexp.MustCompile(`(?:data-sitekey|captchaSiteKey)="([^"]+)"`)
)

type PBAScraperProvider struct {
	client       *http.Client
	captchaToken string
	solver       captchasolver.Solver
	siteKey      string
	cachedToken  string
	tokenExpiry  time.Time
	mu           sync.Mutex
}

func NewPBAScraperProvider(captchaToken, siteKey string, solver captchasolver.Solver) *PBAScraperProvider {
	jar, _ := cookiejar.New(nil)
	p := &PBAScraperProvider{
		client: &http.Client{
			Timeout: 10 * time.Second,
			Jar:     jar,
		},
		captchaToken: strings.TrimSpace(captchaToken),
		siteKey:      strings.TrimSpace(siteKey),
		solver:       solver,
	}
	return p
}

func (p *PBAScraperProvider) Name() string  { return "pba_scraper" }
func (p *PBAScraperProvider) Priority() int { return 30 }
func (p *PBAScraperProvider) Supports(query core.Query) bool {
	source := strings.ToLower(strings.TrimSpace(query.Source))
	if source != "all" && source != "pba" {
		return false
	}
	return strings.TrimSpace(query.Plate) != "" || strings.TrimSpace(query.Document) != ""
}

func (p *PBAScraperProvider) Fetch(ctx context.Context, query core.Query) (core.FineResult, error) {
	csrfHeader, csrfToken, extractedSiteKey, err := p.bootstrapSession(ctx)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to bootstrap pba session"}
	}

	// Token priority: per-request override → cached/solved → static env → default
	captchaToken := strings.TrimSpace(query.PBACaptchaToken)
	if captchaToken == "" {
		captchaToken = p.resolveToken(extractedSiteKey)
	}
	if captchaToken == "" {
		captchaToken = pbaDefaultCaptchaToken
	}

	values := url.Values{}
	values.Set("reCaptcha", captchaToken)
	if plate := strings.ToUpper(strings.TrimSpace(query.Plate)); plate != "" {
		values.Set("dominio", plate)
	} else {
		values.Set("tipoDocumento", "DNI")
		values.Set("nroDocumento", strings.TrimSpace(query.Document))
		values.Set("genero", "N")
	}
	values.Set("cantPorPagina", "100")
	values.Set("paginaActual", "1")

	reqURL := pbaConsultarRestURL + "?" + values.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to build pba request"}
	}
	req.Header.Set("Accept", "application/json, text/plain, */*")
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FlotaBot/1.0)")
	req.Header.Set("Referer", pbaConsultaURL)
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	if csrfHeader != "" && csrfToken != "" {
		req.Header.Set(csrfHeader, csrfToken)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "pba request timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "pba request failed"}
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "pba request returned bad status"}
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "failed to read pba response"}
	}

	fines, providerErr := parsePBAResponse(body)
	if providerErr != nil {
		return core.FineResult{}, providerErr
	}

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

// bootstrapSession fetches the PBA page to obtain the CSRF header/token and the reCAPTCHA sitekey.
func (p *PBAScraperProvider) bootstrapSession(ctx context.Context) (header, token, siteKey string, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, pbaConsultaURL, nil)
	if err != nil {
		return "", "", "", err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; FlotaBot/1.0)")

	resp, err := p.client.Do(req)
	if err != nil {
		return "", "", "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", "", "", err
	}
	raw := string(body)
	header = "X-CSRF-TOKEN"
	if m := pbaCSRFHeaderRx.FindStringSubmatch(raw); len(m) > 1 {
		header = strings.TrimSpace(m[1])
	}
	if m := pbaCSRFTokenRx.FindStringSubmatch(raw); len(m) > 1 {
		token = strings.TrimSpace(m[1])
	}
	if m := pbaSiteKeyRx.FindStringSubmatch(raw); len(m) > 1 {
		siteKey = strings.TrimSpace(m[1])
	}
	return header, token, siteKey, nil
}

// resolveToken returns a valid reCAPTCHA token, using the cache when possible.
func (p *PBAScraperProvider) resolveToken(extractedSiteKey string) string {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.cachedToken != "" && time.Now().Before(p.tokenExpiry) {
		return p.cachedToken
	}

	sk := extractedSiteKey
	if sk == "" {
		sk = p.siteKey
	}
	if sk == "" {
		return p.captchaToken // fallback to static token
	}

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	token, err := p.solver.Solve(ctx, sk, pbaConsultaURL)
	if err != nil || token == "" {
		return p.captchaToken // fallback if solver fails
	}
	p.cachedToken = token
	p.tokenExpiry = time.Now().Add(90 * time.Second)
	return token
}

type pbaEnvelope struct {
	Error        bool             `json:"error"`
	Infracciones []map[string]any `json:"infracciones"`
	Actas        []map[string]any `json:"actas"`
	Data         struct {
		Infracciones []map[string]any `json:"infracciones"`
		Actas        []map[string]any `json:"actas"`
	} `json:"data"`
}

func parsePBAResponse(body []byte) ([]core.Fine, error) {
	trimmed := strings.TrimSpace(string(body))
	if trimmed == "" {
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "empty pba response"}
	}

	var env pbaEnvelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "invalid pba response format"}
	}

	items := env.Infracciones
	if len(items) == 0 {
		items = env.Actas
	}
	if len(items) == 0 {
		items = env.Data.Infracciones
	}
	if len(items) == 0 {
		items = env.Data.Actas
	}

	if len(items) == 0 && env.Error {
		// El endpoint público de PBA exige captcha válido para responder datos.
		return nil, core.DomainError{Code: core.ErrProviderFailed, Message: "pba endpoint requires captcha token"}
	}
	if len(items) == 0 {
		return []core.Fine{}, nil
	}

	out := make([]core.Fine, 0, len(items))
	for _, row := range items {
		ref := pbaPickString(row, "nroCausa", "numeroCausa", "nroActa", "numeroActa", "id")
		offense := pbaPickString(row, "descripcionInfraccion", "infraccion", "descripcion", "detalle", "motivo")
		if offense == "" {
			if nested := pbaPickMapArray(row, "infracciones"); len(nested) > 0 {
				offense = pbaPickString(nested[0], "descripcion", "desc", "infraccion", "detalle")
			}
		}

		jur := pbaPickString(row, "autoridadAplicacion", "jurisdiccion", "partido", "municipio", "localidad", "juzgado")
		jur = strings.TrimSpace(jur)
		if jur == "" {
			jur = "PBA"
		} else {
			jur = "PBA - " + jur
		}

		status := pbaPickNestedStatus(row)
		if status == "" {
			status = strings.ToUpper(strings.TrimSpace(pbaPickString(row, "estadoDesc", "estado", "situacion", "estadoActa")))
		}
		if status == "" {
			status = "PENDING"
		}

		amount := pbaPickAmount(row, "importeTotal", "monto", "importe", "deuda", "total", "valor")
		issuedAt := pbaPickDate(row, "fechaInfraccion", "fecha", "fechaActa", "fechaEmision")
		controller := pbaPickString(row, "autoridadAplicacion", "juzgado", "juez", "controlador")

		hasPhoto := pbaPickBool(row, "tieneFoto", "hasPhoto", "fotoDisponible")
		out = append(out, core.Fine{
			Vehicle:      core.VehicleInfo{Plate: ""},
			Jurisdiction: jur,
			Offense:      offense,
			Amount:       amount,
			Currency:     "ARS",
			Status:       status,
			Controller:   controller,
			HasPhoto:     hasPhoto,
			IssuedAt:     issuedAt,
			Source:       "pba_scraper",
			SourceRef:    ref,
		})
	}

	return out, nil
}

func pbaPickMapArray(row map[string]any, keys ...string) []map[string]any {
	for _, k := range keys {
		raw, ok := row[k]
		if !ok || raw == nil {
			continue
		}
		arr, ok := raw.([]any)
		if !ok {
			continue
		}
		out := make([]map[string]any, 0, len(arr))
		for _, it := range arr {
			if m, ok := it.(map[string]any); ok {
				out = append(out, m)
			}
		}
		if len(out) > 0 {
			return out
		}
	}
	return nil
}

func pbaPickString(row map[string]any, keys ...string) string {
	for _, k := range keys {
		raw, ok := row[k]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case int:
			return strconv.Itoa(v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	return ""
}

func pbaPickBool(row map[string]any, keys ...string) bool {
	for _, k := range keys {
		raw, ok := row[k]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case bool:
			return v
		case string:
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "1", "true", "si", "sí", "yes":
				return true
			case "0", "false", "no":
				return false
			}
		case float64:
			return v != 0
		}
	}
	return false
}

func pbaPickAmount(row map[string]any, keys ...string) float64 {
	for _, k := range keys {
		raw, ok := row[k]
		if !ok || raw == nil {
			continue
		}
		switch v := raw.(type) {
		case float64:
			return v
		case string:
			return pbaParseAmount(v)
		}
	}
	return 0
}

func pbaParseAmount(raw string) float64 {
	s := strings.TrimSpace(raw)
	s = strings.ReplaceAll(s, "$", "")
	s = strings.ReplaceAll(s, " ", "")
	if strings.Contains(s, ".") && strings.Contains(s, ",") {
		s = strings.ReplaceAll(s, ".", "")
		s = strings.ReplaceAll(s, ",", ".")
	} else {
		s = strings.ReplaceAll(s, ",", ".")
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return 0
	}
	return v
}

// pbaPickNestedStatus extracts status from estadoCausaPublico.descripcion
func pbaPickNestedStatus(row map[string]any) string {
	raw, ok := row["estadoCausaPublico"]
	if !ok || raw == nil {
		return ""
	}
	nested, ok := raw.(map[string]any)
	if !ok {
		return ""
	}
	desc, _ := nested["descripcion"].(string)
	return strings.ToUpper(strings.TrimSpace(desc))
}

func pbaPickDate(row map[string]any, keys ...string) time.Time {
	for _, k := range keys {
		raw, ok := row[k]
		if !ok || raw == nil {
			continue
		}
		// Unix timestamp in milliseconds (float64 from JSON)
		if ms, ok := raw.(float64); ok && ms > 0 {
			return time.Unix(int64(ms)/1000, 0)
		}
		if s, ok := raw.(string); ok {
			if t := pbaParseDate(s); !t.IsZero() {
				return t
			}
		}
	}
	return time.Now()
}

func pbaParseDate(raw string) time.Time {
	s := strings.TrimSpace(raw)
	if s == "" {
		return time.Time{}
	}
	loc, err := time.LoadLocation("America/Argentina/Buenos_Aires")
	if err != nil {
		loc = time.FixedZone("-03", -3*60*60)
	}
	layouts := []string{
		"2006-01-02 15:04:05",
		"2006-01-02 15:04",
		"2006-01-02",
		"02/01/2006 15:04:05",
		"02/01/2006 15:04",
		"02/01/2006",
		time.RFC3339,
	}
	for _, layout := range layouts {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t
		}
	}
	return time.Time{}
}
