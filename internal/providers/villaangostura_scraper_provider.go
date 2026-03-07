package providers

import (
	"context"
	"net/http"
	"net/http/cookiejar"
	"strings"
	"time"

	"flota/internal/core"
)

const villaAngosturaSIGEINURL = "https://vla.sigein.net/home.aspx"

type VillaAngosturaScraperProvider struct {
	client *http.Client
}

func NewVillaAngosturaScraperProvider() *VillaAngosturaScraperProvider {
	jar, _ := cookiejar.New(nil)
	return &VillaAngosturaScraperProvider{
		client: &http.Client{Timeout: 15 * time.Second, Jar: jar},
	}
}

func (p *VillaAngosturaScraperProvider) Name() string     { return "villaangostura_scraper" }
func (p *VillaAngosturaScraperProvider) Priority() int    { return 40 }
func (p *VillaAngosturaScraperProvider) Supports(q core.Query) bool {
	src := strings.ToLower(strings.TrimSpace(q.Source))
	return (src == "all" || src == "villaangostura") && strings.TrimSpace(q.Plate) != ""
}

func (p *VillaAngosturaScraperProvider) Fetch(ctx context.Context, q core.Query) (core.FineResult, error) {
	plate := strings.ToUpper(strings.TrimSpace(q.Plate))
	fines, err := fetchSIGEIN(ctx, p.client, villaAngosturaSIGEINURL, plate, "Villa La Angostura")
	if err != nil {
		return core.FineResult{}, err
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
