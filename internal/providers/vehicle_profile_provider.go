package providers

import (
	"context"

	"flota/internal/core"
)

type VehicleProfileProvider interface {
	Name() string
	Priority() int
	Supports(plate string) bool
	FetchVehicleProfile(ctx context.Context, plate string) (core.VehicleProfile, error)
}
