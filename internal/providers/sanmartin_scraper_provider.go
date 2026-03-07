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

// ⚠️ Endpoint and field names need DevTools confirmation.
// Base URL im.sanmartin.gov.ar may require a specific path (e.g. /infracciones/).
const sanMartinBaseURL = "https://im.sanmartin.gov.ar/"

type SanMartinScraperProvider struct {
	client *http.Client
}

func NewSanMartinScraperProvider() *SanMartinScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &SanMartinScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second, Jar: jar},
	}
}

func (p *SanMartinScraperProvider) Name() string  { return "sanmartin_scraper" }
func (p *SanMartinScraperProvider) Priority() int { return 52 }
func (p *SanMartinScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "sanmartin") &&
		(strings.TrimSpace(q.Plate) != "" || strings.TrimSpace(q.Document) != "")
}

func (p *SanMartinScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))
	document := strings.TrimSpace(q.Document)
	identifier := plate
	if identifier == "" {
		identifier = document
	}

	// Step 1: GET to obtain ASP.NET hidden fields.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, sanMartinBaseURL, nil)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "sanmartin: failed to build GET request"}
	}
	getReq.Header.Set("User-Agent", sigeinUserAgent)

	resp, err := p.client.Do(getReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "sanmartin: GET timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "sanmartin: GET failed"}
	}
	pageBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	pageHTML := string(pageBytes)

	viewState := sigeinExtractField(sigeinViewStateRx, pageHTML)
	eventValid := sigeinExtractField(sigeinEvValidRx, pageHTML)
	vsGenerator := sigeinExtractField(sigeinVSGenRx, pageHTML)

	// Step 2: POST with search fields.
	// ⚠️ Field names (txtPatente, txtDNI, btnConsultar) need DevTools confirmation.
	values := url.Values{}
	if viewState != "" {
		values.Set("__EVENTTARGET", "")
		values.Set("__EVENTARGUMENT", "")
		values.Set("__VIEWSTATE", viewState)
		if vsGenerator != "" {
			values.Set("__VIEWSTATEGENERATOR", vsGenerator)
		}
		values.Set("__EVENTVALIDATION", eventValid)
	}
	values.Set("txtPatente", plate)
	values.Set("txtDNI", document)
	values.Set("btnConsultar", "Consultar")

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, sanMartinBaseURL, strings.NewReader(values.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "sanmartin: failed to build POST request"}
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Referer", sanMartinBaseURL)
	postReq.Header.Set("User-Agent", sigeinUserAgent)

	postResp, err := p.client.Do(postReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "sanmartin: POST timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "sanmartin: POST failed"}
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

	fines := parseSIGEINActas(htmlBody, identifier, "San Martín")
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
