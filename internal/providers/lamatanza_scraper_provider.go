package providers

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"regexp"
	"strings"
	"time"

	"flota/internal/core"
	"flota/internal/providers/captchasolver"
)

// ⚠️ Field names (txtPatente, txtDNI, txtCaptcha, btnConsultar) need DevTools confirmation.
const laMatanzaBaseURL = "https://infracciones.lamatanza.gov.ar/"

var laMatanzaCaptchaURLRx = regexp.MustCompile(`(?i)src=["']?(CaptchaImage\.axd[^"'\s>]*)`)

type LaMatanzaScraperProvider struct {
	client *http.Client
	solver captchasolver.Solver
}

func NewLaMatanzaScraperProvider(solver captchasolver.Solver) *LaMatanzaScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &LaMatanzaScraperProvider{
		client: &http.Client{Timeout: 30 * time.Second, Jar: jar},
		solver: solver,
	}
}

func (p *LaMatanzaScraperProvider) Name() string  { return "lamatanza_scraper" }
func (p *LaMatanzaScraperProvider) Priority() int { return 50 }
func (p *LaMatanzaScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "lamatanza") &&
		(strings.TrimSpace(q.Plate) != "" || strings.TrimSpace(q.Document) != "")
}

func (p *LaMatanzaScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))
	document := strings.TrimSpace(q.Document)
	identifier := plate
	if identifier == "" {
		identifier = document
	}

	// Step 1: GET the page to extract ViewState fields and CAPTCHA image URL.
	getReq, err := http.NewRequestWithContext(ctx, http.MethodGet, laMatanzaBaseURL, nil)
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "lamatanza: failed to build GET request"}
	}
	getReq.Header.Set("User-Agent", sigeinUserAgent)

	resp, err := p.client.Do(getReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "lamatanza: GET timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "lamatanza: GET failed"}
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

	// Step 2: Fetch and solve the CAPTCHA image.
	captchaText := ""
	if m := laMatanzaCaptchaURLRx.FindStringSubmatch(pageHTML); len(m) > 1 {
		captchaRelURL := m[1]
		var captchaAbsURL string
		switch {
		case strings.HasPrefix(captchaRelURL, "http"):
			captchaAbsURL = captchaRelURL
		case strings.HasPrefix(captchaRelURL, "/"):
			captchaAbsURL = "https://infracciones.lamatanza.gov.ar" + captchaRelURL
		default:
			captchaAbsURL = laMatanzaBaseURL + captchaRelURL
		}
		imgReq, imgErr := http.NewRequestWithContext(ctx, http.MethodGet, captchaAbsURL, nil)
		if imgErr == nil {
			imgReq.Header.Set("User-Agent", sigeinUserAgent)
			imgReq.Header.Set("Referer", laMatanzaBaseURL)
			imgResp, imgErr := p.client.Do(imgReq)
			if imgErr == nil {
				imgBytes, _ := io.ReadAll(imgResp.Body)
				imgResp.Body.Close()
				if len(imgBytes) > 0 {
					captchaText, _ = p.solver.SolveImage(ctx, imgBytes)
				}
			}
		}
	}

	// Without a solved CAPTCHA the POST will fail — return low confidence empty result.
	if captchaText == "" {
		return core.FineResult{
			Fines:      []core.Fine{},
			Total:      0,
			Source:     p.Name(),
			Confidence: "low",
			FetchedAt:  time.Now(),
		}, nil
	}

	// Step 3: POST with search fields.
	values := url.Values{}
	values.Set("__EVENTTARGET", "")
	values.Set("__EVENTARGUMENT", "")
	values.Set("__VIEWSTATE", viewState)
	values.Set("__VIEWSTATEGENERATOR", vsGenerator)
	values.Set("__EVENTVALIDATION", eventValid)
	values.Set("txtPatente", plate)
	values.Set("txtDNI", document)
	values.Set("txtCaptcha", captchaText)
	values.Set("btnConsultar", "Consultar")

	postReq, err := http.NewRequestWithContext(ctx, http.MethodPost, laMatanzaBaseURL, strings.NewReader(values.Encode()))
	if err != nil {
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "lamatanza: failed to build POST request"}
	}
	postReq.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	postReq.Header.Set("Referer", laMatanzaBaseURL)
	postReq.Header.Set("User-Agent", sigeinUserAgent)

	postResp, err := p.client.Do(postReq)
	if err != nil {
		if ctx.Err() != nil {
			return core.FineResult{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "lamatanza: POST timeout"}
		}
		return core.FineResult{}, core.DomainError{Code: core.ErrProviderFailed, Message: "lamatanza: POST failed"}
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

	fines := parseSIGEINActas(htmlBody, identifier, "La Matanza")
	for i := range fines {
		fines[i].Source = p.Name()
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
