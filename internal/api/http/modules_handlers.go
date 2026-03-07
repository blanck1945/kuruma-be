package httpapi

import (
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"flota/internal/core"
)

// PATCH /v1/fleet/settings
func (h *Handler) FleetUpdateSettings(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	var body struct {
		EnabledModules []string `json:"enabled_modules"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid request body")
		return
	}
	if body.EnabledModules == nil {
		body.EnabledModules = []string{}
	}
	if err := h.fleetStore.UpdateEnabledModules(r.Context(), orgID, body.EnabledModules); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]any{"enabled_modules": body.EnabledModules})
}

// GET /v1/fleet/drivers
func (h *Handler) FleetListDrivers(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	drivers, err := h.fleetStore.ListDrivers(r.Context(), orgID)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if drivers == nil {
		drivers = []core.Driver{}
	}
	okResponse(w, r, drivers)
}

// POST /v1/fleet/drivers
func (h *Handler) FleetAddDriver(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	var body struct {
		Name             string  `json:"name"`
		DNI              string  `json:"dni"`
		LicenseNumber    string  `json:"license_number"`
		LicenseExpiresAt *string `json:"license_expires_at"`
		Phone            string  `json:"phone"`
		Email            string  `json:"email"`
		Notes            string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "name is required")
		return
	}
	d := core.Driver{
		Name:          body.Name,
		DNI:           body.DNI,
		LicenseNumber: body.LicenseNumber,
		Phone:         body.Phone,
		Email:         body.Email,
		Notes:         body.Notes,
	}
	if body.LicenseExpiresAt != nil && *body.LicenseExpiresAt != "" {
		t, err := time.Parse("2006-01-02", *body.LicenseExpiresAt)
		if err != nil {
			errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid license_expires_at date")
			return
		}
		d.LicenseExpiresAt = &t
	}
	saved, err := h.fleetStore.AddDriver(r.Context(), orgID, d)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, envelope{Success: true, Data: saved})
}

// PUT /v1/fleet/drivers/{id}
func (h *Handler) FleetUpdateDriver(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	id := r.PathValue("id")
	var body struct {
		Name             string  `json:"name"`
		DNI              string  `json:"dni"`
		LicenseNumber    string  `json:"license_number"`
		LicenseExpiresAt *string `json:"license_expires_at"`
		Phone            string  `json:"phone"`
		Email            string  `json:"email"`
		Notes            string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Name) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "name is required")
		return
	}
	d := core.Driver{
		ID:            id,
		Name:          body.Name,
		DNI:           body.DNI,
		LicenseNumber: body.LicenseNumber,
		Phone:         body.Phone,
		Email:         body.Email,
		Notes:         body.Notes,
	}
	if body.LicenseExpiresAt != nil && *body.LicenseExpiresAt != "" {
		t, err := time.Parse("2006-01-02", *body.LicenseExpiresAt)
		if err != nil {
			errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid license_expires_at date")
			return
		}
		d.LicenseExpiresAt = &t
	}
	saved, err := h.fleetStore.UpdateDriver(r.Context(), orgID, d)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, saved)
}

// DELETE /v1/fleet/drivers/{id}
func (h *Handler) FleetRemoveDriver(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	id := r.PathValue("id")
	if err := h.fleetStore.RemoveDriver(r.Context(), orgID, id); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"id": id, "status": "removed"})
}

// DELETE /v1/fleet/drivers
func (h *Handler) FleetClearDrivers(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if err := h.fleetStore.ClearDrivers(r.Context(), orgID); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"status": "cleared"})
}

// DELETE /v1/fleet/maintenance
func (h *Handler) FleetClearMaintenance(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if err := h.fleetStore.ClearMaintenance(r.Context(), orgID); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"status": "cleared"})
}

// POST /v1/fleet/drivers/{id}/assign/{plate}
func (h *Handler) FleetAssignDriver(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	driverID := r.PathValue("id")
	plate := strings.ToUpper(r.PathValue("plate"))
	if err := h.fleetStore.AssignDriver(r.Context(), orgID, plate, driverID); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"driver_id": driverID, "plate": plate, "status": "assigned"})
}

// POST /v1/fleet/drivers/{id}/unassign/{plate}
func (h *Handler) FleetUnassignDriver(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	driverID := r.PathValue("id")
	plate := strings.ToUpper(r.PathValue("plate"))
	if err := h.fleetStore.UnassignDriver(r.Context(), orgID, plate, driverID); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"driver_id": driverID, "plate": plate, "status": "unassigned"})
}

// GET /v1/fleet/maintenance
func (h *Handler) FleetListMaintenance(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	plate := r.URL.Query().Get("plate")
	records, err := h.fleetStore.ListMaintenance(r.Context(), orgID, plate)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if records == nil {
		records = []core.MaintenanceRecord{}
	}
	okResponse(w, r, records)
}

// POST /v1/fleet/maintenance
func (h *Handler) FleetAddMaintenance(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	var body struct {
		Plate           string   `json:"plate"`
		Type            string   `json:"type"`
		Description     string   `json:"description"`
		ServiceDate     string   `json:"service_date"`
		KmAtService     int      `json:"km_at_service"`
		NextServiceDate *string  `json:"next_service_date"`
		NextServiceKm   *int     `json:"next_service_km"`
		CostARS         float64  `json:"cost_ars"`
		Notes           string   `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.Plate) == "" || strings.TrimSpace(body.ServiceDate) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "plate and service_date are required")
		return
	}
	sd, err := time.Parse("2006-01-02", body.ServiceDate)
	if err != nil {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid service_date")
		return
	}
	rec := core.MaintenanceRecord{
		Plate:         strings.ToUpper(body.Plate),
		Type:          body.Type,
		Description:   body.Description,
		ServiceDate:   sd,
		KmAtService:   body.KmAtService,
		NextServiceKm: body.NextServiceKm,
		CostARS:       body.CostARS,
		Notes:         body.Notes,
	}
	if rec.Type == "" {
		rec.Type = "preventive"
	}
	if body.NextServiceDate != nil && *body.NextServiceDate != "" {
		t, err := time.Parse("2006-01-02", *body.NextServiceDate)
		if err != nil {
			errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid next_service_date")
			return
		}
		rec.NextServiceDate = &t
	}
	saved, err := h.fleetStore.AddMaintenance(r.Context(), orgID, rec)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, envelope{Success: true, Data: saved})
}

// DELETE /v1/fleet/maintenance/{id}
func (h *Handler) FleetRemoveMaintenance(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	id := r.PathValue("id")
	if err := h.fleetStore.RemoveMaintenance(r.Context(), orgID, id); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"id": id, "status": "removed"})
}

// GET /v1/fleet/documents
func (h *Handler) FleetListDocuments(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	plate := r.URL.Query().Get("plate")
	docs, err := h.fleetStore.ListDocuments(r.Context(), orgID, plate)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if docs == nil {
		docs = []core.VehicleDocument{}
	}
	okResponse(w, r, docs)
}

// POST /v1/fleet/documents
func (h *Handler) FleetAddDocument(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	var body struct {
		Plate     string  `json:"plate"`
		DriverID  *string `json:"driver_id"`
		DocType   string  `json:"doc_type"`
		DocNumber string  `json:"doc_number"`
		IssuedAt  *string `json:"issued_at"`
		ExpiresAt *string `json:"expires_at"`
		Notes     string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.DocType) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "doc_type is required")
		return
	}
	doc := core.VehicleDocument{
		Plate:     strings.ToUpper(body.Plate),
		DriverID:  body.DriverID,
		DocType:   body.DocType,
		DocNumber: body.DocNumber,
		Notes:     body.Notes,
	}
	if body.IssuedAt != nil && *body.IssuedAt != "" {
		t, err := time.Parse("2006-01-02", *body.IssuedAt)
		if err != nil {
			errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid issued_at")
			return
		}
		doc.IssuedAt = &t
	}
	if body.ExpiresAt != nil && *body.ExpiresAt != "" {
		t, err := time.Parse("2006-01-02", *body.ExpiresAt)
		if err != nil {
			errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid expires_at")
			return
		}
		doc.ExpiresAt = &t
	}
	saved, err := h.fleetStore.AddDocument(r.Context(), orgID, doc)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, envelope{Success: true, Data: saved})
}

// DELETE /v1/fleet/documents/{id}
func (h *Handler) FleetRemoveDocument(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	id := r.PathValue("id")
	if err := h.fleetStore.RemoveDocument(r.Context(), orgID, id); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"id": id, "status": "removed"})
}

// GET /v1/fleet/fuel
func (h *Handler) FleetListFuel(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	plate := r.URL.Query().Get("plate")
	logs, err := h.fleetStore.ListFuelLogs(r.Context(), orgID, plate)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if logs == nil {
		logs = []core.FuelLog{}
	}
	okResponse(w, r, logs)
}

// POST /v1/fleet/fuel
func (h *Handler) FleetAddFuel(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	var body struct {
		Plate        string  `json:"plate"`
		FillDate     string  `json:"fill_date"`
		Liters       float64 `json:"liters"`
		KmAtFill     int     `json:"km_at_fill"`
		CostPerLiter float64 `json:"cost_per_liter"`
		TotalCostARS float64 `json:"total_cost_ars"`
		FuelType     string  `json:"fuel_type"`
		Notes        string  `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.Plate) == "" || strings.TrimSpace(body.FillDate) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "plate and fill_date are required")
		return
	}
	fd, err := time.Parse("2006-01-02", body.FillDate)
	if err != nil {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid fill_date")
		return
	}
	ft := body.FuelType
	if ft == "" {
		ft = "nafta"
	}
	f := core.FuelLog{
		Plate:        strings.ToUpper(body.Plate),
		FillDate:     fd,
		Liters:       body.Liters,
		KmAtFill:     body.KmAtFill,
		CostPerLiter: body.CostPerLiter,
		TotalCostARS: body.TotalCostARS,
		FuelType:     ft,
		Notes:        body.Notes,
	}
	saved, err := h.fleetStore.AddFuelLog(r.Context(), orgID, f)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, envelope{Success: true, Data: saved})
}

// DELETE /v1/fleet/fuel/{id}
func (h *Handler) FleetRemoveFuel(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	id := r.PathValue("id")
	if err := h.fleetStore.RemoveFuelLog(r.Context(), orgID, id); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"id": id, "status": "removed"})
}

// GET /v1/fleet/assignments
func (h *Handler) FleetListAssignments(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	assignments, err := h.fleetStore.ListCurrentAssignments(r.Context(), orgID)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if assignments == nil {
		assignments = []core.Assignment{}
	}
	okResponse(w, r, assignments)
}

// GET /v1/fleet/vehicles/{plate}/driver
func (h *Handler) FleetGetVehicleDriver(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	plate := strings.ToUpper(r.PathValue("plate"))
	driver, err := h.fleetStore.GetCurrentDriver(r.Context(), orgID, plate)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, driver) // *Driver — JSON null if unassigned
}

// GET /v1/fleet/vehicles/{plate}/driver-history
func (h *Handler) FleetGetVehicleDriverHistory(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	plate := strings.ToUpper(r.PathValue("plate"))
	history, err := h.fleetStore.ListVehicleDriverHistory(r.Context(), orgID, plate)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if history == nil {
		history = []core.DriverAssignment{}
	}
	okResponse(w, r, history)
}

// GET /v1/fleet/schedule?from=YYYY-MM-DD&to=YYYY-MM-DD
func (h *Handler) FleetListSchedule(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	from, _ := time.Parse("2006-01-02", r.URL.Query().Get("from"))
	to, _ := time.Parse("2006-01-02", r.URL.Query().Get("to"))
	if from.IsZero() {
		from = time.Now().Add(-7 * 24 * time.Hour)
	}
	if to.IsZero() {
		to = time.Now().Add(7 * 24 * time.Hour)
	}
	entries, err := h.fleetStore.ListSchedule(r.Context(), orgID, from, to)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if entries == nil {
		entries = []core.ScheduleEntry{}
	}
	okResponse(w, r, entries)
}

// POST /v1/fleet/schedule
func (h *Handler) FleetAddScheduleEntry(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	var body struct {
		Plate         string `json:"plate"`
		DriverID      string `json:"driver_id"`
		ScheduledDate string `json:"scheduled_date"`
		StartTime     string `json:"start_time"`
		EndTime       string `json:"end_time"`
		Notes         string `json:"notes"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Plate) == "" || strings.TrimSpace(body.DriverID) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "plate and driver_id are required")
		return
	}
	date, err := time.Parse("2006-01-02", body.ScheduledDate)
	if err != nil {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid scheduled_date, use YYYY-MM-DD")
		return
	}
	entry := core.ScheduleEntry{
		Plate:         strings.ToUpper(body.Plate),
		DriverID:      body.DriverID,
		ScheduledDate: date,
		StartTime:     body.StartTime,
		EndTime:       body.EndTime,
		Notes:         body.Notes,
	}
	saved, err := h.fleetStore.AddScheduleEntry(r.Context(), orgID, entry)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, envelope{Success: true, Data: saved})
}

// DELETE /v1/fleet/schedule/{id}
func (h *Handler) FleetRemoveScheduleEntry(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	id := r.PathValue("id")
	if err := h.fleetStore.RemoveScheduleEntry(r.Context(), orgID, id); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"id": id, "status": "removed"})
}
