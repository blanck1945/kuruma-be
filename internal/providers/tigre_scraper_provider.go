package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strings"
	"time"

	"flota/internal/core"
)

// ⚠️ Tigre portal uses Cuenta+Clave fields (account-based system, not plate lookup).
// The plate is sent as "Cuenta" — results will likely be empty until DevTools confirms
// an alternative plate-based endpoint or the correct Cuenta field name.
const tigreBaseURL = "https://ingresospublicos.tigre.gob.ar/ConsultaTributos/"

type TigreScraperProvider struct {
	client *http.Client
}

func NewTigreScraperProvider() *TigreScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &TigreScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second, Jar: jar},
	}
}

func (p *TigreScraperProvider) Name() string  { return "tigre_scraper" }
func (p *TigreScraperProvider) Priority() int { return 51 }
func (p *TigreScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "tigre") && strings.TrimSpace(q.Plate) != ""
}

func (p *TigreScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))

	// Step 1: GET to obtain ASP.NET hidden fields.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, tigreBaseURL, nil)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "tigre: failed to build GET request"}
	}
	getReq.Header.Set("User-Agent", sigeinUserAgent)

	resp, err := p.client.Do(getReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "tigre: GET timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "tigre: GET failed"}
	}
	pageBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	pageHTML := string(pageBytes)

	viewState := sigeinExtractField(sigeinViewStateRx, pageHTML)
	eventValid := sigeinExtractField(sigeinEvValidRx, pageHTML)
	vsGenerator := sigeinExtractField(sigeinVSGenRx, pageHTML)
	if vsGenerator == "" {
		vsGenerator = "8D0E13E6"
	}

	// Step 2: POST with plate as "Cuenta" (field name to be confirmed with DevTools).
	values := url.Values{}
	values.Set("__EVENTTARGET", "")
	values.Set("__EVENTARGUMENT", "")
	values.Set("__VIEWSTATE", viewState)
	values.Set("__VIEWSTATEGENERATOR", vsGenerator)
	values.Set("__EVENTVALIDATION", eventValid)
	values.Set("txtCuenta", plate)
	values.Set("txtClave", "")

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, tigreBaseURL, strings.NewReader(values.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "tigre: failed to build POST request"}
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Referer", tigreBaseURL)
	postReq.Header.Set("User-Agent", sigeinUserAgent)

	postResp, err := p.client.Do(postReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "tigre: POST timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "tigre: POST failed"}
	}
	defer postResp.Body.Close()
	body, _ := io.ReadAll(postResp.Body)
	htmlBody := string(body)

	if sigeinNoFinesRx.MatchString(htmlBody) {
		return core.FineResult{
			Fines:      []core.Fine{},
			Total:      0,
			Source:     p.Name(),
			Confidence: "high",
			FetchedAt:  time.Now(),
		}, nil
	}

	fines := parseSIGEINActas(htmlBody, plate, "Tigre")
	for i := range fines {
		fines[i].Source = p.Name()
	}
	confidence := "low"
	if len(fines) > 0 {
		confidence = "medium"
	}
	return core.FineResult{
		Fines:      fines,
		Total:      len(fines),
		Source:     p.Name(),
		Confidence: confidence,
		FetchedAt:  time.Now(),
	}, nil
}
