package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	httpapi "flota/internal/api/http"
	"flota/internal/config"
	"flota/internal/core"
	"flota/internal/providers"
	"flota/internal/providers/captchasolver"
	"flota/internal/storage/cache"
	"flota/internal/storage/logging"
	"flota/internal/storage/metrics"
	"flota/internal/storage/postgres"
	"flota/internal/storage/vehicledb"
)

func main() {
	cfg := config.Load()
	logger := logging.New()
	recorder := metrics.NewRecorder()

	l1 := cache.NewMemoryCache()
	l2 := cache.NewRedisCache(cfg.RedisAddr, cfg.RedisPassword, cfg.RedisDB)
	multiCache := cache.MultiLevel{L1: l1, L2: l2}

	solver := captchasolver.New(cfg.CapSolverAPIKey)
	router := providers.NewProviderRouter(cfg.ProviderTimeout,
		providers.NewCABAScraperProvider(),
		providers.NewCordobaScraperProvider(),
		providers.NewPBAScraperProvider(cfg.PBARecaptchaToken, cfg.PBASiteKey, solver),
		providers.NewEntreRiosScraperProvider(),
		providers.NewSantaFeScraperProvider(),
		providers.NewMisionesScraperProvider(solver),
		providers.NewCorrientesScraperProvider(),
		providers.NewChacoScraperProvider(),
		providers.NewSaltaScraperProvider(solver),
		providers.NewJujuyScraperProvider(solver),
		providers.NewRioTerceroScraperProvider(),
		providers.NewRoqueSaenzPenaScraperProvider(),
		providers.NewVillaAngosturaScraperProvider(),
		providers.NewSantaRosaScraperProvider(),
		providers.NewLomasDeZamoraScraperProvider(),
		providers.NewAvellanedaScraperProvider(),
		providers.NewAlmiranteBrownScraperProvider(),
		providers.NewEscobarScraperProvider(),
		providers.NewPosadasScraperProvider(),
		providers.NewVenadoTuertoScraperProvider(),
		providers.NewMendozaScraperProvider(solver),
		providers.NewTresDeFebreroScraperProvider(),
		providers.NewLaMatanzaScraperProvider(solver),
		providers.NewTigreScraperProvider(),
		providers.NewSanMartinScraperProvider(),
		providers.NewMockScraperProvider(cfg.EnableScraping),
	)
	service := core.NewService(router, multiCache, 15*time.Second, 2*time.Minute)
	service.SetCacheObserver(recorder.ObserveCache)
	vehicleProfileRouter := providers.NewVehicleProfileRouter(cfg.ProviderTimeout,
		providers.NewVehicleProfileMockProvider(),
	)
	vehicleProfileService := core.NewVehicleProfileService(vehicleProfileRouter)
	if vdb, err := vehicledb.NewSQLiteStore(cfg.VehicleDBPath); err != nil {
		logger.Warn("vehicle db unavailable", "error", err.Error())
	} else {
		vehicleProfileService.SetStore(vdb)
	}
	handler := httpapi.NewHandler(service, logger)
	handler.SetVehicleProfileService(vehicleProfileService)
	handler.SetGeminiAPIKey(cfg.GeminiAPIKey)
	handler.SetJWTSecret(cfg.InternalJWTSecret)
	handler.SetMPAccessToken(cfg.MPAccessToken)
	handler.SetBackendURL(cfg.BackendURL)
	handler.SetFrontendURL(cfg.FrontendURL)
	var pgStore *postgres.Store
	if cfg.DatabaseURL != "" {
		if store, err := postgres.New(cfg.DatabaseURL); err != nil {
			logger.Warn("postgres unavailable", "error", err.Error())
		} else {
			pgStore = store
			handler.SetFleetStore(pgStore)
			logger.Info("postgres connected")
			if err := pgStore.SeedTestOrg(context.Background()); err != nil {
				logger.Warn("seed test org failed", "error", err.Error())
			} else {
				logger.Info("test org ready", "email", "test@flota.com", "password", "Test1234")
			}
			go runFinesCacheRefresher(context.Background(), pgStore, service, logger)
		}
	}

	server := httpapi.NewServer(cfg, handler, recorder, logger, l2)

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           server.Router(),
		ReadHeaderTimeout: 3 * time.Second,
		ReadTimeout:       5 * time.Second,
		WriteTimeout:      35 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Info("starting http server", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server crashed", "error", err.Error())
			os.Exit(1)
		}
	}()

	waitForShutdown(logger, srv)
}

func runFinesCacheRefresher(ctx context.Context, store core.FleetStore, svc *core.Service, logger *slog.Logger) {
	const maxAge = 24 * time.Hour
	const interval = time.Hour

	refresh := func() {
		stale, err := store.ListStaleFineCaches(ctx, maxAge)
		if err != nil {
			logger.Warn("fines cache refresh: list failed", "error", err)
			return
		}
		if len(stale) == 0 {
			return
		}
		logger.Info("fines cache refresh: refreshing stale entries", "count", len(stale))
		for _, entry := range stale {
			result, err := svc.SearchFines(ctx, core.Query{Plate: entry.Plate, Source: entry.Source})
			if err != nil {
				logger.Warn("fines cache refresh: scrape failed", "plate", entry.Plate, "source", entry.Source, "error", err)
				continue
			}
			if err := store.UpsertFinesCache(ctx, core.FinesCache{
				Plate:  entry.Plate,
				Source: entry.Source,
				Fines:  result.Fines,
				Total:  result.Total,
			}); err != nil {
				logger.Warn("fines cache refresh: upsert failed", "plate", entry.Plate, "source", entry.Source, "error", err)
			}
		}
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			refresh()
		case <-ctx.Done():
			return
		}
	}
}

func waitForShutdown(logger *slog.Logger, srv *http.Server) {
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)
	<-stop

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logger.Info("shutting down server")
	if err := srv.Shutdown(ctx); err != nil {
		logger.Error("shutdown failed", "error", err.Error())
	}
}
