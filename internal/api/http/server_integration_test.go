package httpapi

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"flota/internal/config"
	"flota/internal/core"
	"flota/internal/providers"
	"flota/internal/storage/cache"
	"flota/internal/storage/logging"
	"flota/internal/storage/metrics"
)

func TestExternalSearchEndpoint(t *testing.T) {
	cfg := config.Config{
		ExternalAPIKeys:     map[string]string{"test-client": "test-key"},
		InternalJWTSecret:   "secret",
		DefaultRequestLimit: 100,
		ProviderTimeout:     2 * time.Second,
	}

	router := providers.NewProviderRouter(cfg.ProviderTimeout, providers.NewMockAPIProvider())
	svc := core.NewService(router, cache.NewMemoryCache(), 10*time.Second, time.Minute)
	rec := metrics.NewRecorder()
	svc.SetCacheObserver(rec.ObserveCache)

	h := NewHandler(svc, logging.New())
	vehicleRouter := providers.NewVehicleProfileRouter(cfg.ProviderTimeout, providers.NewVehicleProfileMockProvider())
	h.SetVehicleProfileService(core.NewVehicleProfileService(vehicleRouter))
	srv := NewServer(cfg, h, rec, logging.New(), nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/external/fines?plate=AAA000", nil)
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestExternalVehicleProfileEndpoint(t *testing.T) {
	cfg := config.Config{
		ExternalAPIKeys:     map[string]string{"test-client": "test-key"},
		InternalJWTSecret:   "secret",
		DefaultRequestLimit: 100,
		ProviderTimeout:     2 * time.Second,
	}

	router := providers.NewProviderRouter(cfg.ProviderTimeout, providers.NewMockAPIProvider())
	svc := core.NewService(router, cache.NewMemoryCache(), 10*time.Second, time.Minute)
	rec := metrics.NewRecorder()
	svc.SetCacheObserver(rec.ObserveCache)

	h := NewHandler(svc, logging.New())
	vehicleRouter := providers.NewVehicleProfileRouter(cfg.ProviderTimeout, providers.NewVehicleProfileMockProvider())
	h.SetVehicleProfileService(core.NewVehicleProfileService(vehicleRouter))
	srv := NewServer(cfg, h, rec, logging.New(), nil)

	req := httptest.NewRequest(http.MethodGet, "/v1/external/vehicle-profile?plate=AAA000", nil)
	req.Header.Set("X-API-Key", "test-key")
	rr := httptest.NewRecorder()
	srv.Router().ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}
