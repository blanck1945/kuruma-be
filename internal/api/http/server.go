package httpapi

import (
	"context"
	_ "embed"
	"log/slog"
	"net/http"
	"os"
	"time"

	"flota/internal/api/middleware"
	"flota/internal/config"
	"flota/internal/storage/metrics"
)

type Pinger interface {
	Ping(ctx context.Context) error
}

//go:embed swagger_ui.html
var swaggerUIHTML []byte

type Server struct {
	cfg         config.Config
	handler     *Handler
	metrics     *metrics.Recorder
	rateLimiter *middleware.RateLimiter
	logger      *slog.Logger
	pinger      Pinger
}

func NewServer(cfg config.Config, h *Handler, m *metrics.Recorder, logger *slog.Logger, pinger Pinger) *Server {
	return &Server{
		cfg:        cfg,
		handler:    h,
		metrics:    m,
		rateLimiter: middleware.NewRateLimiter(cfg.DefaultRequestLimit, time.Minute),
		logger:     logger,
		pinger:     pinger,
	}
}

func (s *Server) Router() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.health)
	mux.HandleFunc("/readyz", s.ready)
	mux.HandleFunc("/metrics", s.metricsHandler)
	mux.HandleFunc("/openapi.yaml", s.openapiHandler)
	mux.HandleFunc("/swagger", s.swaggerHandler)
	mux.HandleFunc("/swagger/", s.swaggerHandler)
	mux.HandleFunc("/v1/external/fines", s.handler.SearchFines)
	mux.HandleFunc("/v1/internal/fines", s.handler.SearchFines)
	mux.HandleFunc("/v1/external/vehicle-profile", s.handler.SearchVehicleProfile)
	mux.HandleFunc("/v1/internal/vehicle-profile", s.handler.SearchVehicleProfile)
	mux.HandleFunc("/v1/external/parse-csv", s.handler.ParseCSV)
	mux.HandleFunc("/v1/external/parse-drivers-csv", s.handler.ParseDriversCSV)
	mux.HandleFunc("/v1/external/parse-maintenance-csv", s.handler.ParseMaintenanceCSV)
	mux.HandleFunc("/v1/external/parse-documents-csv", s.handler.ParseDocumentsCSV)
	mux.HandleFunc("/v1/external/parse-fuel-csv", s.handler.ParseFuelCSV)
	mux.HandleFunc("/v1/external/parse-schedule-csv", s.handler.ParseScheduleCSV)
	mux.HandleFunc("POST /v1/external/organizations", s.handler.CreateOrg)
	mux.HandleFunc("GET /v1/external/organizations", s.handler.ListOrgs)
	mux.HandleFunc("GET /v1/external/organizations/{id}", s.handler.GetOrg)
	// Fleet endpoints — autenticados con JWT de org
	mux.HandleFunc("POST /v1/public/login", s.handler.Login)
	mux.HandleFunc("GET /v1/fleet/vehicles", s.handler.FleetListVehicles)
	mux.HandleFunc("POST /v1/fleet/vehicles", s.handler.FleetAddVehicle)
	mux.HandleFunc("DELETE /v1/fleet/vehicles/{plate}", s.handler.FleetRemoveVehicle)
	mux.HandleFunc("DELETE /v1/fleet/vehicles", s.handler.FleetClearVehicles)
	mux.HandleFunc("GET /v1/fleet/vehicles/{plate}/fines", s.handler.FleetGetVehicleFines)
	mux.HandleFunc("PATCH /v1/fleet/vehicles/{plate}/vtv", s.handler.FleetUpdateVehicleVTV)
	mux.HandleFunc("POST /v1/fleet/chat", s.handler.FleetChat)
	mux.HandleFunc("POST /v1/fleet/subscription/checkout", s.handler.FleetCreateCheckout)
	mux.HandleFunc("POST /v1/public/mp-webhook", s.handler.MPWebhook)
	// Settings
	mux.HandleFunc("PATCH /v1/fleet/settings", s.handler.FleetUpdateSettings)
	// Drivers
	mux.HandleFunc("GET /v1/fleet/drivers", s.handler.FleetListDrivers)
	mux.HandleFunc("POST /v1/fleet/drivers", s.handler.FleetAddDriver)
	mux.HandleFunc("PUT /v1/fleet/drivers/{id}", s.handler.FleetUpdateDriver)
	mux.HandleFunc("DELETE /v1/fleet/drivers", s.handler.FleetClearDrivers)
	mux.HandleFunc("DELETE /v1/fleet/drivers/{id}", s.handler.FleetRemoveDriver)
	mux.HandleFunc("POST /v1/fleet/drivers/{id}/assign/{plate}", s.handler.FleetAssignDriver)
	mux.HandleFunc("POST /v1/fleet/drivers/{id}/unassign/{plate}", s.handler.FleetUnassignDriver)
	// Maintenance
	mux.HandleFunc("GET /v1/fleet/maintenance", s.handler.FleetListMaintenance)
	mux.HandleFunc("POST /v1/fleet/maintenance", s.handler.FleetAddMaintenance)
	mux.HandleFunc("DELETE /v1/fleet/maintenance", s.handler.FleetClearMaintenance)
	mux.HandleFunc("DELETE /v1/fleet/maintenance/{id}", s.handler.FleetRemoveMaintenance)
	// Documents
	mux.HandleFunc("GET /v1/fleet/documents", s.handler.FleetListDocuments)
	mux.HandleFunc("POST /v1/fleet/documents", s.handler.FleetAddDocument)
	mux.HandleFunc("DELETE /v1/fleet/documents/{id}", s.handler.FleetRemoveDocument)
	// Fuel
	mux.HandleFunc("GET /v1/fleet/fuel", s.handler.FleetListFuel)
	mux.HandleFunc("POST /v1/fleet/fuel", s.handler.FleetAddFuel)
	mux.HandleFunc("DELETE /v1/fleet/fuel/{id}", s.handler.FleetRemoveFuel)
	// Assignments
	mux.HandleFunc("GET /v1/fleet/assignments", s.handler.FleetListAssignments)
	mux.HandleFunc("GET /v1/fleet/vehicles/{plate}/driver", s.handler.FleetGetVehicleDriver)
	mux.HandleFunc("GET /v1/fleet/vehicles/{plate}/driver-history", s.handler.FleetGetVehicleDriverHistory)
	// Schedule
	mux.HandleFunc("GET /v1/fleet/schedule", s.handler.FleetListSchedule)
	mux.HandleFunc("POST /v1/fleet/schedule", s.handler.FleetAddScheduleEntry)
	mux.HandleFunc("DELETE /v1/fleet/schedule/{id}", s.handler.FleetRemoveScheduleEntry)

	handler := middleware.Trace(mux)
	handler = middleware.Auth(s.cfg)(handler)
	handler = middleware.Metrics(s.metrics)(handler)
	handler = s.rateLimiter.Middleware(handler)
	handler = middleware.CORS(handler)
	return handler
}

func (s *Server) health(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ok"}`))
}

func (s *Server) ready(w http.ResponseWriter, _ *http.Request) {
	if s.pinger != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := s.pinger.Ping(ctx); err != nil {
			s.logger.Error("readiness failed", "error", err.Error())
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte(`{"status":"degraded","redis":"down"}`))
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(`{"status":"ready"}`))
}

func (s *Server) metricsHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(s.metrics.SnapshotText()))
}

func (s *Server) openapiHandler(w http.ResponseWriter, _ *http.Request) {
	content, err := os.ReadFile("docs/openapi.yaml")
	if err != nil {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("openapi spec not found"))
		return
	}
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(content)
}

func (s *Server) swaggerHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/swagger" && r.URL.Path != "/swagger/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(swaggerUIHTML)
}

