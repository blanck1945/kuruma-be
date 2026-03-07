package providers

import (
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"flota/internal/core"
)

const carsXEBaseURL = "https://api.carsxe.com/v2/platedecoder"

type CarsXEProvider struct {
	apiKey string
	client *http.Client
}

func NewCarsXEProvider(apiKey string) *CarsXEProvider {
	return &CarsXEProvider{
		apiKey: strings.TrimSpace(apiKey),
		client: &http.Client{Timeout: 12 * time.Second},
	}
}

func (p *CarsXEProvider) Name() string  { return "carsxe" }
func (p *CarsXEProvider) Priority() int { return 10 }
func (p *CarsXEProvider) Supports(plate string) bool {
	return strings.TrimSpace(plate) != "" && p.apiKey != ""
}

func (p *CarsXEProvider) FetchVehicleProfile(ctx context.Context, plate string) (core.VehicleProfile, error) {
	u, _ := url.Parse(carsXEBaseURL)
	q := u.Query()
	q.Set("key", p.apiKey)
	q.Set("plate", strings.ToUpper(strings.TrimSpace(plate)))
	q.Set("country", "AR")
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderFailed, Message: "carsxe request build failed"}
	}
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "carsxe provider timeout"}
		}
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderFailed, Message: "carsxe provider request failed"}
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 500 {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderFailed, Message: "carsxe server error"}
	}

	var raw struct {
		Success          bool   `json:"success"`
		Make             string `json:"make"`
		Model            string `json:"model"`
		RegistrationYear string `json:"registration_year"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&raw); err != nil {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderFailed, Message: "carsxe invalid json"}
	}

	if !raw.Success || (raw.Make == "" && raw.Model == "") {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrNotFound, Message: "vehicle profile not found"}
	}

	year, _ := strconv.Atoi(strings.TrimSpace(raw.RegistrationYear))
	return core.VehicleProfile{
		Plate:      strings.ToUpper(strings.TrimSpace(plate)),
		Make:       strings.TrimSpace(raw.Make),
		Model:      strings.TrimSpace(raw.Model),
		Year:       year,
		Source:     p.Name(),
		Confidence: "high",
		FetchedAt:  time.Now(),
	}, nil
}
