package providers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"flota/internal/core"
)

type VehicleProfileHTTPProvider struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewVehicleProfileHTTPProvider(baseURL, apiKey string) *VehicleProfileHTTPProvider {
	return &VehicleProfileHTTPProvider{
		baseURL: strings.TrimSpace(baseURL),
		apiKey:  strings.TrimSpace(apiKey),
		client: &http.Client{
			Timeout: 12 * time.Second,
		},
	}
}

func (p *VehicleProfileHTTPProvider) Name() string  { return "vehicle_profile_http" }
func (p *VehicleProfileHTTPProvider) Priority() int { return 10 }
func (p *VehicleProfileHTTPProvider) Supports(plate string) bool {
	return strings.TrimSpace(plate) != "" && p.baseURL != ""
}

func (p *VehicleProfileHTTPProvider) FetchVehicleProfile(ctx context.Context, plate string) (core.VehicleProfile, error) {
	base, err := url.Parse(p.baseURL)
	if err != nil {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderFailed, Message: "vehicle profile base url is invalid"}
	}
	params := base.Query()
	params.Set("plate", plate)
	base.RawQuery = params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base.String(), nil)
	if err != nil {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderFailed, Message: "vehicle profile request build failed"}
	}
	req.Header.Set("Accept", "application/json")
	if p.apiKey != "" {
		req.Header.Set("X-API-Key", p.apiKey)
		req.Header.Set("Authorization", "Bearer "+p.apiKey)
	}

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderTimeout, Message: "vehicle profile provider timeout"}
		}
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderFailed, Message: "vehicle profile provider request failed"}
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrNotFound, Message: "vehicle profile not found"}
	}
	if resp.StatusCode >= 400 {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderFailed, Message: fmt.Sprintf("vehicle profile provider status %d", resp.StatusCode)}
	}

	var raw any
	if decodeErr := json.NewDecoder(resp.Body).Decode(&raw); decodeErr != nil {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrProviderFailed, Message: "vehicle profile provider invalid json"}
	}

	payload := asObject(raw)
	if data, ok := payload["data"]; ok {
		if nested := asObject(data); len(nested) > 0 {
			payload = nested
		}
	}

	profile := core.VehicleProfile{
		Plate:      firstNonEmptyString(payload, "plate", "patente", "dominio"),
		Make:       firstNonEmptyString(payload, "make", "marca", "brand"),
		Model:      firstNonEmptyString(payload, "model", "modelo"),
		Type:       firstNonEmptyString(payload, "type", "vehicle_type", "tipo", "class"),
		Fuel:       firstNonEmptyString(payload, "fuel", "combustible"),
		Source:     p.Name(),
		Confidence: firstNonEmptyString(payload, "confidence"),
		FetchedAt:  time.Now(),
	}
	profile.Year = firstInt(payload, "year", "anio", "año")
	if profile.Plate == "" {
		profile.Plate = plate
	}
	if profile.Make == "" && profile.Model == "" && profile.Year == 0 && profile.Type == "" {
		return core.VehicleProfile{}, core.DomainError{Code: core.ErrNotFound, Message: "vehicle profile not found"}
	}
	return profile, nil
}

func asObject(value any) map[string]any {
	if out, ok := value.(map[string]any); ok {
		return out
	}
	return map[string]any{}
}

func firstNonEmptyString(payload map[string]any, keys ...string) string {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case string:
			if v := strings.TrimSpace(typed); v != "" {
				return v
			}
		case float64:
			return strconv.Itoa(int(typed))
		case int:
			return strconv.Itoa(typed)
		}
	}
	return ""
}

func firstInt(payload map[string]any, keys ...string) int {
	for _, key := range keys {
		value, ok := payload[key]
		if !ok {
			continue
		}
		switch typed := value.(type) {
		case float64:
			return int(typed)
		case int:
			return typed
		case string:
			n, err := strconv.Atoi(strings.TrimSpace(typed))
			if err == nil {
				return n
			}
		}
	}
	return 0
}
