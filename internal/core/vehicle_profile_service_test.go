package core

import (
	"context"
	"testing"
	"time"
)

type fakeVehicleProfileFetcher struct {
	profile VehicleProfile
	err     error
}

func (f *fakeVehicleProfileFetcher) FetchVehicleProfile(_ context.Context, _ string) (VehicleProfile, error) {
	if f.err != nil {
		return VehicleProfile{}, f.err
	}
	return f.profile, nil
}

func TestVehicleProfileService_InvalidPlate(t *testing.T) {
	svc := NewVehicleProfileService(&fakeVehicleProfileFetcher{})
	_, err := svc.SearchByPlate(context.Background(), "BAD")
	if err == nil {
		t.Fatalf("expected error, got nil")
	}
}

func TestVehicleProfileService_NormalizesOutput(t *testing.T) {
	svc := NewVehicleProfileService(&fakeVehicleProfileFetcher{
		profile: VehicleProfile{
			Make:      "Ford",
			Model:     "Focus",
			Year:      2018,
			Source:    "provider-x",
			FetchedAt: time.Now(),
		},
	})

	out, err := svc.SearchByPlate(context.Background(), "aa-123-bb")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if out.Plate != "AA123BB" {
		t.Fatalf("expected normalized plate AA123BB, got %s", out.Plate)
	}
	if out.Confidence == "" {
		t.Fatalf("expected default confidence")
	}
}
