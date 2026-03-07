package providers

import (
	"context"
	"strings"
	"time"

	"flota/internal/core"
)

type VehicleProfileMockProvider struct{}

func NewVehicleProfileMockProvider() *VehicleProfileMockProvider {
	return &VehicleProfileMockProvider{}
}

func (p *VehicleProfileMockProvider) Name() string  { return "vehicle_profile_mock" }
func (p *VehicleProfileMockProvider) Priority() int { return 200 }
func (p *VehicleProfileMockProvider) Supports(plate string) bool {
	return strings.TrimSpace(plate) != ""
}

func (p *VehicleProfileMockProvider) FetchVehicleProfile(_ context.Context, plate string) (core.VehicleProfile, error) {
	normalized := strings.ToUpper(strings.TrimSpace(plate))
	switch normalized {
	case "OVR038":
		return core.VehicleProfile{
			Plate:      normalized,
			Make:       "CHEVROLET",
			Model:      "AGILE 1.4 8V",
			Source:     p.Name(),
			Confidence: "high",
			FetchedAt:  time.Now(),
		}, nil
	case "AAA000":
		return core.VehicleProfile{
			Plate:      normalized,
			Make:       "Volkswagen",
			Model:      "Gol Trend",
			Year:       2017,
			Type:       "Auto",
			Fuel:       "Nafta",
			Source:     p.Name(),
			Confidence: "low",
			FetchedAt:  time.Now(),
		}, nil
	case "AB123CD":
		return core.VehicleProfile{
			Plate:      normalized,
			Make:       "Toyota",
			Model:      "Hilux",
			Year:       2020,
			Type:       "Pickup",
			Fuel:       "Diesel",
			Source:     p.Name(),
			Confidence: "low",
			FetchedAt:  time.Now(),
		}, nil
	}
	return core.VehicleProfile{}, core.DomainError{Code: core.ErrNotFound, Message: "vehicle profile not found for plate"}
}
