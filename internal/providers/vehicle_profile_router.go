package providers

import (
	"context"
	"sort"
	"strings"
	"time"

	"flota/internal/core"
)

type VehicleProfileRouter struct {
	providers []VehicleProfileProvider
	timeout   time.Duration
}

func NewVehicleProfileRouter(timeout time.Duration, providers ...VehicleProfileProvider) *VehicleProfileRouter {
	pp := make([]VehicleProfileProvider, 0, len(providers))
	for _, provider := range providers {
		pp = append(pp, provider)
	}
	sort.Slice(pp, func(i, j int) bool {
		return pp[i].Priority() < pp[j].Priority()
	})
	return &VehicleProfileRouter{
		providers: pp,
		timeout:   timeout,
	}
}

func (r *VehicleProfileRouter) FetchVehicleProfile(ctx context.Context, plate string) (core.VehicleProfile, error) {
	var firstErr error
	for _, provider := range r.providers {
		if !provider.Supports(plate) {
			continue
		}

		callCtx := ctx
		cancel := func() {}
		if r.timeout > 0 {
			callCtx, cancel = context.WithTimeout(ctx, r.timeout)
		}

		profile, err := provider.FetchVehicleProfile(callCtx, plate)
		cancel()
		if err == nil {
			if strings.TrimSpace(profile.Source) == "" {
				profile.Source = provider.Name()
			}
			return profile, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	if firstErr != nil {
		return core.VehicleProfile{}, firstErr
	}
	return core.VehicleProfile{}, core.DomainError{Code: core.ErrNotFound, Message: "no vehicle profile provider returned data"}
}
