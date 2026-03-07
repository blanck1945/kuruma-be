package core

import (
	"context"
	"strings"
	"time"
)

type VehicleProfileFetcher interface {
	FetchVehicleProfile(ctx context.Context, plate string) (VehicleProfile, error)
}

type VehicleProfileStore interface {
	Get(ctx context.Context, plate string) (VehicleProfile, bool, error)
	Save(ctx context.Context, profile VehicleProfile) error
}

type VehicleProfileService struct {
	fetcher VehicleProfileFetcher
	store   VehicleProfileStore
}

func NewVehicleProfileService(fetcher VehicleProfileFetcher) *VehicleProfileService {
	return &VehicleProfileService{fetcher: fetcher}
}

func (s *VehicleProfileService) SetStore(store VehicleProfileStore) {
	s.store = store
}

func (s *VehicleProfileService) SearchByPlate(ctx context.Context, plate string) (VehicleProfile, error) {
	if s == nil || s.fetcher == nil {
		return VehicleProfile{}, DomainError{Code: ErrInternal, Message: "vehicle profile service unavailable"}
	}

	normalizedPlate := normalizePlate(plate)
	if !isValidPlate(normalizedPlate) {
		return VehicleProfile{}, DomainError{Code: ErrInvalidPlate, Message: "invalid plate format"}
	}

	if s.store != nil {
		if cached, ok, err := s.store.Get(ctx, normalizedPlate); err == nil && ok {
			return cached, nil
		}
	}

	out, err := s.fetcher.FetchVehicleProfile(ctx, normalizedPlate)
	if err != nil {
		return VehicleProfile{}, err
	}

	if strings.TrimSpace(out.Plate) == "" {
		out.Plate = normalizedPlate
	} else {
		out.Plate = normalizePlate(out.Plate)
	}
	out.Make = strings.TrimSpace(out.Make)
	out.Model = strings.TrimSpace(out.Model)
	out.Type = strings.TrimSpace(out.Type)
	out.Fuel = strings.TrimSpace(out.Fuel)
	out.Source = strings.TrimSpace(out.Source)
	if out.Source == "" {
		out.Source = "unknown"
	}
	out.Confidence = strings.ToLower(strings.TrimSpace(out.Confidence))
	if out.Confidence == "" {
		out.Confidence = "medium"
	}
	if out.FetchedAt.IsZero() {
		out.FetchedAt = time.Now()
	}

	if out.Make == "" && out.Model == "" && out.Year == 0 && out.Type == "" {
		return VehicleProfile{}, DomainError{Code: ErrNotFound, Message: "vehicle profile not found for plate"}
	}

	if s.store != nil {
		_ = s.store.Save(ctx, out)
	}

	return out, nil
}
