package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"flota/internal/api/middleware"
	"flota/internal/core"

	"golang.org/x/crypto/bcrypt"
)

func (h *Handler) SetFleetStore(store core.FleetStore) {
	h.fleetStore = store
}

// orgIDFromToken extracts and validates the org JWT, returning the org ID.
func (h *Handler) orgIDFromToken(r *http.Request) (string, error) {
	parts := strings.SplitN(r.Header.Get("Authorization"), " ", 2)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", core.DomainError{Code: core.ErrUnauthorized, Message: "missing bearer token"}
	}
	claims, err := middleware.ParseHS256JWT(parts[1], h.jwtSecret)
	if err != nil {
		return "", err
	}
	orgID, _ := claims["sub"].(string)
	if orgID == "" {
		return "", core.DomainError{Code: core.ErrUnauthorized, Message: "invalid token claims"}
	}
	return orgID, nil
}

// POST /v1/public/login
func (h *Handler) Login(w http.ResponseWriter, r *http.Request) {
	if h.fleetStore == nil {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "fleet store unavailable")
		return
	}
	var body struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid request body")
		return
	}

	org, err := h.fleetStore.GetOrgByEmail(r.Context(), strings.ToLower(strings.TrimSpace(body.Email)))
	if err != nil {
		// Don't reveal whether the email exists
		errorResponse(w, r, http.StatusUnauthorized, core.ErrUnauthorized, "invalid credentials")
		return
	}
	if err := bcrypt.CompareHashAndPassword([]byte(org.PasswordHash), []byte(body.Password)); err != nil {
		errorResponse(w, r, http.StatusUnauthorized, core.ErrUnauthorized, "invalid credentials")
		return
	}

	token, err := middleware.GenerateHS256JWT(map[string]any{
		"sub":   org.ID,
		"scope": "fleet:manage",
	}, h.jwtSecret)
	if err != nil {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "could not generate token")
		return
	}

	enabledModules := org.EnabledModules
	if enabledModules == nil {
		enabledModules = []string{}
	}
	okResponse(w, r, map[string]any{
		"token": token,
		"org": map[string]any{
			"id":              org.ID,
			"name":            org.Name,
			"slug":            org.Slug,
			"enabled_modules": enabledModules,
		},
	})
}

// --- Fleet vehicle endpoints (JWT auth, scoped to the authenticated org) ---

// GET /v1/fleet/vehicles
func (h *Handler) FleetListVehicles(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	vehicles, err := h.fleetStore.ListVehicles(r.Context(), orgID)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if vehicles == nil {
		vehicles = []core.FleetVehicle{}
	}
	okResponse(w, r, vehicles)
}

// POST /v1/fleet/vehicles
func (h *Handler) FleetAddVehicle(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	var v core.FleetVehicle
	if err := json.NewDecoder(r.Body).Decode(&v); err != nil || strings.TrimSpace(v.Plate) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "plate is required")
		return
	}
	v.Plate = strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(v.Plate, "-", ""), " ", ""))
	saved, err := h.fleetStore.AddVehicle(r.Context(), orgID, v)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, envelope{Success: true, Data: saved})
}

// DELETE /v1/fleet/vehicles/{plate}
func (h *Handler) FleetRemoveVehicle(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	plate := strings.ToUpper(r.PathValue("plate"))
	if err := h.fleetStore.RemoveVehicle(r.Context(), orgID, plate); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"plate": plate, "status": "removed"})
}

// DELETE /v1/fleet/vehicles  (clear all)
func (h *Handler) FleetClearVehicles(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if err := h.fleetStore.ClearVehicles(r.Context(), orgID); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]string{"status": "cleared"})
}

// GET /v1/fleet/vehicles/{plate}/fines?source=caba
func (h *Handler) FleetGetVehicleFines(w http.ResponseWriter, r *http.Request) {
	_, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	plate := strings.ToUpper(r.PathValue("plate"))
	source := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))
	if source == "" {
		source = "all"
	}
	force := r.URL.Query().Get("force") == "true"

	if !force {
		cached, err := h.fleetStore.GetFinesCache(r.Context(), plate, source)
		if err == nil {
			// Cache entry exists (fresh or stale) — always serve from DB.
			// Stale entries are refreshed by the background cron, not by the frontend request.
			fines := cached.Fines
			if fines == nil {
				fines = []core.Fine{}
			}
			okResponse(w, r, map[string]any{
				"fines":      fines,
				"total":      cached.Total,
				"cached":     true,
				"fetched_at": cached.FetchedAt,
			})
			return
		}
		// err != nil means no cache entry exists yet (first time) — fall through to live fetch.
	}

	result, err := h.service.SearchFines(r.Context(), core.Query{Plate: plate, Source: source})
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}

	_ = h.fleetStore.UpsertFinesCache(r.Context(), core.FinesCache{
		Plate:  plate,
		Source: source,
		Fines:  result.Fines,
		Total:  result.Total,
	})

	fines := result.Fines
	if fines == nil {
		fines = []core.Fine{}
	}
	okResponse(w, r, map[string]any{
		"fines":  fines,
		"total":  result.Total,
		"cached": false,
	})
}

// PATCH /v1/fleet/vehicles/{plate}/vtv
func (h *Handler) FleetUpdateVehicleVTV(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	plate := strings.ToUpper(r.PathValue("plate"))

	var body struct {
		VTVDueDate *string `json:"vtv_due_date"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid request body")
		return
	}

	var vtvDate *time.Time
	if body.VTVDueDate != nil && *body.VTVDueDate != "" {
		t, err := time.Parse("2006-01-02", *body.VTVDueDate)
		if err != nil {
			errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "invalid date format, expected YYYY-MM-DD")
			return
		}
		vtvDate = &t
	}

	if err := h.fleetStore.UpdateVehicleVTV(r.Context(), orgID, plate, vtvDate); err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, map[string]any{"plate": plate, "vtv_due_date": body.VTVDueDate})
}

// --- Org admin endpoints (X-API-Key auth via middleware) ---

// POST /v1/external/organizations
func (h *Handler) CreateOrg(w http.ResponseWriter, r *http.Request) {
	if h.fleetStore == nil {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "fleet store unavailable")
		return
	}
	var body struct {
		Name     string `json:"name"`
		Slug     string `json:"slug"`
		Email    string `json:"email"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil ||
		strings.TrimSpace(body.Name) == "" || strings.TrimSpace(body.Slug) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "name and slug are required")
		return
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(body.Password), bcrypt.DefaultCost)
	if err != nil {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "could not hash password")
		return
	}
	org, err := h.fleetStore.CreateOrg(r.Context(),
		strings.TrimSpace(body.Name), strings.TrimSpace(body.Slug),
		strings.ToLower(strings.TrimSpace(body.Email)), string(hash),
	)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, envelope{Success: true, Data: org})
}

// GET /v1/external/organizations
func (h *Handler) ListOrgs(w http.ResponseWriter, r *http.Request) {
	if h.fleetStore == nil {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "fleet store unavailable")
		return
	}
	orgs, err := h.fleetStore.ListOrgs(r.Context())
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if orgs == nil {
		orgs = []core.Organization{}
	}
	okResponse(w, r, orgs)
}

// GET /v1/external/organizations/{id}
func (h *Handler) GetOrg(w http.ResponseWriter, r *http.Request) {
	if h.fleetStore == nil {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "fleet store unavailable")
		return
	}
	org, err := h.fleetStore.GetOrg(r.Context(), r.PathValue("id"))
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	okResponse(w, r, org)
}

// POST /v1/fleet/chat
func (h *Handler) FleetChat(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if h.geminiAPIKey == "" {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "gemini api key not configured")
		return
	}

	var body struct {
		Message   string `json:"message"`
		SessionID string `json:"session_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || strings.TrimSpace(body.Message) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "message is required")
		return
	}

	ctx := r.Context()

	org, err := h.fleetStore.GetOrg(ctx, orgID)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}

	// Tier access check
	if org.Tier != core.TierBusiness && time.Now().After(org.TierExpiresAt) {
		errorResponse(w, r, http.StatusPaymentRequired, core.ErrSubscriptionRequired,
			"Tu período de prueba ha vencido. Actualizá tu plan para seguir usando el asistente.")
		return
	}

	vehicles, err := h.fleetStore.ListVehicles(ctx, orgID)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}

	plates := make([]string, 0, len(vehicles))
	for _, v := range vehicles {
		plates = append(plates, v.Plate)
	}

	finesCache, _ := h.fleetStore.ListFinesCacheByPlates(ctx, plates)

	// Load module data based on enabled modules
	hasModule := func(m string) bool {
		for _, mod := range org.EnabledModules {
			if mod == m {
				return true
			}
		}
		return false
	}
	var drivers []core.Driver
	var assignments []core.Assignment
	if hasModule("conductores") {
		drivers, _ = h.fleetStore.ListDrivers(ctx, orgID)
		assignments, _ = h.fleetStore.ListCurrentAssignments(ctx, orgID)
	}
	var maintenance []core.MaintenanceRecord
	if hasModule("mantenimiento") {
		maintenance, _ = h.fleetStore.ListMaintenance(ctx, orgID)
	}
	var documents []core.VehicleDocument
	if hasModule("documentos") {
		documents, _ = h.fleetStore.ListDocuments(ctx, orgID)
	}
	var fuelLogs []core.FuelLog
	if hasModule("combustible") {
		fuelLogs, _ = h.fleetStore.ListFuelLogs(ctx, orgID)
	}

	// Build fine totals map: plate -> []"source=N multas"
	finesByPlate := make(map[string][]string)
	for _, fc := range finesCache {
		unit := "multa"
		if fc.Total != 1 {
			unit = "multas"
		}
		var totalAmount float64
		yearCount := make(map[int]int)
		yearAmount := make(map[int]float64)
		for _, f := range fc.Fines {
			totalAmount += f.Amount
			if !f.IssuedAt.IsZero() {
				y := f.IssuedAt.Year()
				yearCount[y]++
				yearAmount[y] += f.Amount
			}
		}
		// Build per-year breakdown sorted ascending
		type yearEntry struct{ y, n int; a float64 }
		var years []yearEntry
		for y, n := range yearCount {
			years = append(years, yearEntry{y, n, yearAmount[y]})
		}
		// simple insertion sort (small slice)
		for i := 1; i < len(years); i++ {
			for j := i; j > 0 && years[j].y < years[j-1].y; j-- {
				years[j], years[j-1] = years[j-1], years[j]
			}
		}
		var breakdown strings.Builder
		for _, e := range years {
			fmt.Fprintf(&breakdown, " %d:%d", e.y, e.n)
		}
		finesByPlate[fc.Plate] = append(finesByPlate[fc.Plate],
			fmt.Sprintf("%s=%d %s%s", fc.Source, fc.Total, unit, breakdown.String()))
	}

	// Build system prompt
	today := time.Now().Format("2006-01-02")
	var sb strings.Builder
	fmt.Fprintf(&sb, "Sos el asistente de FlotaFR para la organización %s.\n", org.Name)
	sb.WriteString("FlotaFR es una plataforma de gestión de flotas de vehículos argentinos.\n")
	fmt.Fprintf(&sb, "Fecha de hoy: %s\n\n", today)

	fmt.Fprintf(&sb, "FLOTA (%d vehículo(s)):\n", len(vehicles))
	for _, v := range vehicles {
		desc := v.Plate + ":"
		if v.Make != "" {
			desc += " " + strings.ToUpper(v.Make)
		}
		if v.Model != "" {
			desc += " " + strings.ToUpper(v.Model)
		}
		if v.Year > 0 {
			desc += fmt.Sprintf(" %d", v.Year)
		}
		if v.VTVDueDate != nil {
			days := int(time.Until(*v.VTVDueDate).Hours() / 24)
			desc += fmt.Sprintf(" | VTV: vence %s (%d días)", v.VTVDueDate.Format("2006-01-02"), days)
		} else {
			desc += " | VTV: N/A"
		}
		fmt.Fprintf(&sb, "- %s\n", desc)
	}

	sb.WriteString("\nMULTAS CACHEADAS:\n")
	if len(finesCache) == 0 {
		sb.WriteString("- Sin multas cacheadas\n")
	} else {
		for _, v := range vehicles {
			parts, ok := finesByPlate[v.Plate]
			if ok {
				fmt.Fprintf(&sb, "- %s: %s\n", v.Plate, strings.Join(parts, " | "))
			} else {
				fmt.Fprintf(&sb, "- %s: sin multas cacheadas\n", v.Plate)
			}
		}
	}

	// --- Module data sections ---
	if hasModule("conductores") && len(drivers) > 0 {
		plateByDriver := make(map[string]string)
		for _, a := range assignments {
			plateByDriver[a.DriverID] = a.Plate
		}
		fmt.Fprintf(&sb, "\nCONDUCTORES (%d):\n", len(drivers))
		for _, d := range drivers {
			line := "- " + d.Name
			if d.DNI != "" {
				line += fmt.Sprintf(" (DNI %s)", d.DNI)
			}
			if d.LicenseExpiresAt != nil {
				days := int(time.Until(*d.LicenseExpiresAt).Hours() / 24)
				switch {
				case days < 0:
					line += fmt.Sprintf(" | LICENCIA VENCIDA hace %d días", -days)
				case days < 30:
					line += fmt.Sprintf(" | licencia vence en %d días (%s)", days, d.LicenseExpiresAt.Format("2006-01-02"))
				default:
					line += fmt.Sprintf(" | licencia vigente hasta %s", d.LicenseExpiresAt.Format("2006-01-02"))
				}
			}
			if d.Phone != "" {
				line += " | tel: " + d.Phone
			}
			if plate, ok := plateByDriver[d.ID]; ok {
				line += " | asignado a: " + plate
			} else {
				line += " | sin vehículo asignado"
			}
			sb.WriteString(line + "\n")
		}
	}

	if hasModule("mantenimiento") && len(maintenance) > 0 {
		sb.WriteString("\nMANTENIMIENTO (último service por vehículo):\n")
		lastByPlate := make(map[string]core.MaintenanceRecord)
		for _, r := range maintenance {
			if _, ok := lastByPlate[r.Plate]; !ok {
				lastByPlate[r.Plate] = r
			}
		}
		for _, r := range lastByPlate {
			line := fmt.Sprintf("- %s: service %s", r.Plate, r.ServiceDate.Format("2006-01-02"))
			if r.Description != "" {
				line += " · " + r.Description
			}
			if r.CostARS > 0 {
				line += fmt.Sprintf(" · $%.0f ARS", r.CostARS)
			}
			if r.NextServiceDate != nil {
				days := int(time.Until(*r.NextServiceDate).Hours() / 24)
				if days < 0 {
					line += fmt.Sprintf(" | PRÓXIMO SERVICE VENCIDO hace %d días", -days)
				} else {
					line += fmt.Sprintf(" | próximo service en %d días (%s)", days, r.NextServiceDate.Format("2006-01-02"))
				}
			}
			if r.NextServiceKm != nil {
				line += fmt.Sprintf(" o a %d km", *r.NextServiceKm)
			}
			sb.WriteString(line + "\n")
		}
	}

	if hasModule("documentos") && len(documents) > 0 {
		sb.WriteString("\nDOCUMENTOS:\n")
		for _, d := range documents {
			target := d.Plate
			if target == "" {
				target = "org"
			}
			line := fmt.Sprintf("- %s: %s", target, strings.ToUpper(d.DocType))
			if d.DocNumber != "" {
				line += " " + d.DocNumber
			}
			if d.ExpiresAt != nil {
				days := int(time.Until(*d.ExpiresAt).Hours() / 24)
				switch {
				case days < 0:
					line += fmt.Sprintf(" | VENCIDO hace %d días (%s)", -days, d.ExpiresAt.Format("2006-01-02"))
				case days < 30:
					line += fmt.Sprintf(" | vence en %d días (%s)", days, d.ExpiresAt.Format("2006-01-02"))
				default:
					line += fmt.Sprintf(" | vigente hasta %s", d.ExpiresAt.Format("2006-01-02"))
				}
			}
			sb.WriteString(line + "\n")
		}
	}

	if hasModule("combustible") && len(fuelLogs) > 0 {
		sb.WriteString("\nCOMBUSTIBLE:\n")
		logsByPlate := make(map[string][]core.FuelLog)
		for _, l := range fuelLogs {
			logsByPlate[l.Plate] = append(logsByPlate[l.Plate], l)
		}
		for _, plate := range plates {
			logs := logsByPlate[plate]
			if len(logs) == 0 {
				continue
			}
			sort.Slice(logs, func(i, j int) bool {
				return logs[i].FillDate.Before(logs[j].FillDate)
			})
			last := logs[len(logs)-1]
			line := fmt.Sprintf("- %s: última carga %s (%.1fL · $%.0f ARS)",
				plate, last.FillDate.Format("2006-01-02"), last.Liters, last.TotalCostARS)
			if len(logs) >= 2 {
				var totalKm int
				var totalL float64
				for i := 1; i < len(logs); i++ {
					km := logs[i].KmAtFill - logs[i-1].KmAtFill
					if km > 0 {
						totalKm += km
						totalL += logs[i].Liters
					}
				}
				if totalL > 0 && totalKm > 0 {
					line += fmt.Sprintf(" | promedio %.1f km/L", float64(totalKm)/totalL)
				}
			}
			sb.WriteString(line + "\n")
		}
	}

	sb.WriteString("\nLANGUAGE: Respond in the same language the user writes in. English if they write English, Spanish if Spanish. Be concise.\n")
	sb.WriteString("FORMAT: In prose/text responses, format currency amounts as Argentine pesos with dot-separated thousands and two decimals. Example: $6.716.429,30 ARS. Never write raw numbers like 6716429.3 in text.\n")
	sb.WriteString("In ACCION_TABLA rows, amount columns MUST be raw numbers (no $ sign, no ARS, no dots/commas formatting). Example row: [\"AAA000\",61,6716429.30]. The frontend formats them.\n")

	sb.WriteString("\nTOOL USAGE — OBLIGATORIO:\n")
	sb.WriteString("Llamá a consultar_multas SIEMPRE que el usuario pregunte sobre:\n")
	sb.WriteString("- montos, totales en ARS/pesos\n")
	sb.WriteString("- multas filtradas por año o período\n")
	sb.WriteString("- datos detallados de multas de una patente específica\n")
	sb.WriteString("NO respondas montos desde este prompt — el tool tiene los datos reales.\n")
	sb.WriteString("Para preguntas sobre la flota en general (cantidad de vehículos, VTV) respondé sin tool.\n")

	sb.WriteString("\nACTION RULES:\n")
	sb.WriteString("Emit at most ONE action per reply. It must be the LAST line. Nothing may follow it.\n")
	sb.WriteString("Use the per-year breakdown in MULTAS CACHEADAS to answer date-filtered questions (e.g. 'multas de 2022').\n\n")

	sb.WriteString("ACCION_DESCARGA — ONLY when the user explicitly wants to download/export a file.\n")
	sb.WriteString("Keywords: descargar, bajar, exportar, dame el excel, generar excel, quiero el archivo, download.\n")
	sb.WriteString("Format: ACCION_DESCARGA:{\"plates\":[\"AAA000\"],\"filename\":\"name.xlsx\"}\n\n")

	sb.WriteString("ACCION_GRAFICO — when the user wants a chart/graph/visual.\n")
	sb.WriteString("Keywords: gráfico, grafico, chart, barras, líneas, visualizar, evolución.\n")
	sb.WriteString("Format: ACCION_GRAFICO:{\"plate\":\"OVR038\",\"type\":\"bar\",\"metric\":\"count\",\"groupBy\":\"year\",\"sources\":[\"caba\",\"pba\"]}\n")
	sb.WriteString("type: bar|line|area · metric: count|amount · groupBy: year|month · sources: caba,pba,cordoba,santafe,mendoza,entrerios\n")
	sb.WriteString("Only emit if the plate exists. If none specified, ask the user.\n\n")

	sb.WriteString("ACCION_TABLA — when the user wants to SEE or LIST data (mostrame, cuáles, qué autos, dame una lista, show me, which, list, ranking, tabla).\n")
	sb.WriteString("Format: ACCION_TABLA:{\"columns\":[\"Patente\",\"CABA\",\"PBA\",\"Total\"],\"rows\":[[\"OVR038\",3,0,3]]}\n")
	sb.WriteString("Rules: last column MUST be 'Total' (sum of numeric cols). Use 0 for missing data. Only include rows matching the filter. Max 50 rows.\n\n")

	sb.WriteString("NO ACTION — for simple factual questions ('cuántas', 'cuál es el total', 'tiene VTV'), just answer in plain text. No action needed.\n\n")

	sb.WriteString("FEW-SHOT EXAMPLES (follow this behavior exactly):\n")
	sb.WriteString("Q: Mostrame los autos del 2020 que tengan multas\n")
	sb.WriteString("A: Los vehículos del año 2020 con multas son: [lista]. <newline> ACCION_TABLA:{\"columns\":[\"Patente\",\"Modelo\",\"CABA\",\"PBA\",\"Total\"],\"rows\":[...]}\n\n")
	sb.WriteString("Q: Graficame las multas de OVR038 en CABA por año\n")
	sb.WriteString("A: Acá está el gráfico de multas de OVR038 en CABA por año. <newline> ACCION_GRAFICO:{\"plate\":\"OVR038\",\"type\":\"bar\",\"metric\":\"count\",\"groupBy\":\"year\",\"sources\":[\"caba\"]}\n\n")
	sb.WriteString("Q: Descargame los autos con multas en PBA\n")
	sb.WriteString("A: Hay X vehículos con multas en PBA: [lista]. <newline> ACCION_DESCARGA:{\"plates\":[...],\"filename\":\"multas_pba.xlsx\"}\n\n")
	sb.WriteString("Q: Cuál es el total en pesos de las multas de OVR038 en PBA?\n")
	sb.WriteString("A: El total de multas de OVR038 en PBA es $287.500 ARS (14 multas). [plain text, no action]\n\n")
	sb.WriteString("Q: Qué autos tienen VTV vencida?\n")
	sb.WriteString("A: Los vehículos con VTV vencida son: [lista]. <newline> ACCION_TABLA:{...}\n\n")

	systemPrompt := sb.String()

	// ── Load or create session ────────────────────────────────────────────
	sessionKey := orgID + ":" + body.SessionID
	sess := h.chatSessions.getOrCreate(sessionKey)
	sess.mu.Lock()
	defer sess.mu.Unlock()
	sess.lastUsed = time.Now()

	// Snapshot existing history + append current user message.
	contents := make([]gContent, len(sess.contents), len(sess.contents)+1)
	copy(contents, sess.contents)
	contents = append(contents, gContent{Role: "user", Parts: []gPart{{Text: body.Message}}})

	// ── Tool: consultar_multas ────────────────────────────────────────────
	toolDecls := []map[string]any{
		{
			"functionDeclarations": []map[string]any{
				{
					"name":        "consultar_multas",
					"description": "Obtiene las multas detalladas de una patente desde la/s fuente/s indicadas. Devuelve cantidad, monto total ARS y desglose por año. Usá esta función siempre que necesites datos exactos de multas (montos, fechas, cantidades por período).",
					"parameters": map[string]any{
						"type": "OBJECT",
						"properties": map[string]any{
							"plate": map[string]any{
								"type":        "STRING",
								"description": "Patente del vehículo, sin guiones (ej: OVR038)",
							},
							"sources": map[string]any{
								"type":        "ARRAY",
								"description": "Fuentes a consultar. Valores válidos: caba, pba, cordoba, santafe, mendoza, entrerios",
								"items":       map[string]any{"type": "STRING"},
							},
						},
						"required": []string{"plate", "sources"},
					},
				},
			},
		},
	}

	// ── Function executor ─────────────────────────────────────────────────
	execFn := func(name string, args map[string]any) any {
		if name != "consultar_multas" {
			return map[string]any{"error": "función desconocida"}
		}
		plate, _ := args["plate"].(string)
		plate = strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(plate, "-", ""), " ", ""))
		sourcesRaw, _ := args["sources"].([]any)
		var sources []string
		for _, s := range sourcesRaw {
			if str, ok := s.(string); ok {
				sources = append(sources, str)
			}
		}

		type yearStat struct {
			Count  int     `json:"count"`
			Amount float64 `json:"amount_ars"`
		}
		type srcStat struct {
			Count      int                 `json:"count"`
			AmountARS  float64             `json:"amount_ars"`
			ByYear     map[string]yearStat `json:"by_year"`
			NoCacheData bool               `json:"no_cache_data,omitempty"`
		}

		out := map[string]any{
			"plate":             plate,
			"total_count":       0,
			"total_amount_ars":  0.0,
			"by_source":         map[string]srcStat{},
		}
		totalCount := 0
		var totalAmount float64

		for _, src := range sources {
			cache, err := h.fleetStore.GetFinesCache(ctx, plate, src)
			if err != nil {
				out["by_source"].(map[string]srcStat)[src] = srcStat{NoCacheData: true}
				continue
			}
			var srcAmt float64
			byYear := map[string]yearStat{}
			for _, f := range cache.Fines {
				srcAmt += f.Amount
				if !f.IssuedAt.IsZero() {
					y := fmt.Sprintf("%d", f.IssuedAt.Year())
					ys := byYear[y]
					ys.Count++
					ys.Amount += f.Amount
					byYear[y] = ys
				}
			}
			totalCount += cache.Total
			totalAmount += srcAmt
			out["by_source"].(map[string]srcStat)[src] = srcStat{
				Count: cache.Total, AmountARS: srcAmt, ByYear: byYear,
			}
		}
		out["total_count"] = totalCount
		out["total_amount_ars"] = totalAmount
		return out
	}

	// ── Helper: one Gemini HTTP call ──────────────────────────────────────
	type gRespPart struct {
		Text         string         `json:"text"`
		FunctionCall map[string]any `json:"functionCall"`
	}
	type gRespCandidate struct {
		Content struct {
			Parts []gRespPart `json:"parts"`
			Role  string      `json:"role"`
		} `json:"content"`
		FinishReason string `json:"finishReason"`
	}
	type gRespBody struct {
		Candidates []gRespCandidate `json:"candidates"`
	}

	apiURL := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=" + h.geminiAPIKey

	doRequest := func(conts []gContent) (*gRespBody, error) {
		reqMap := map[string]any{
			"system_instruction": map[string]any{"parts": []gPart{{Text: systemPrompt}}},
			"contents":           conts,
			"tools":              toolDecls,
			"tool_config": map[string]any{
				"function_calling_config": map[string]any{
					"mode": "AUTO",
				},
			},
			"generationConfig": map[string]any{
				"temperature":     0.6,
				"maxOutputTokens": 4096,
			},
		}
		b, err := json.Marshal(reqMap)
		if err != nil {
			return nil, err
		}
		callCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		req, err := http.NewRequestWithContext(callCtx, http.MethodPost, apiURL, bytes.NewReader(b))
		if err != nil {
			return nil, err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			var errBody map[string]any
			_ = json.NewDecoder(resp.Body).Decode(&errBody)
			msg, _ := errBody["error"].(map[string]any)["message"].(string)
			if msg == "" {
				msg = fmt.Sprintf("status %d", resp.StatusCode)
			}
			return nil, fmt.Errorf("gemini %d: %s", resp.StatusCode, msg)
		}
		var gr gRespBody
		if err := json.NewDecoder(resp.Body).Decode(&gr); err != nil {
			return nil, err
		}
		if len(gr.Candidates) == 0 {
			return nil, fmt.Errorf("empty response from gemini")
		}
		return &gr, nil
	}

	// ── Agentic loop (max 4 iterations to allow multi-step tool calls) ────
	for i := 0; i < 4; i++ {
		gr, err := doRequest(contents)
		if err != nil {
			errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, err.Error())
			return
		}

		parts := gr.Candidates[0].Content.Parts
		var modelParts []gPart
		var fnRespParts []gPart
		calledFn := false

		for _, p := range parts {
			if len(p.FunctionCall) > 0 {
				calledFn = true
				name, _ := p.FunctionCall["name"].(string)
				args, _ := p.FunctionCall["args"].(map[string]any)
				result := execFn(name, args)
				modelParts = append(modelParts, gPart{FunctionCall: p.FunctionCall})
				fnRespParts = append(fnRespParts, gPart{
					FunctionResponse: map[string]any{
						"name":     name,
						"response": map[string]any{"result": result},
					},
				})
			} else if p.Text != "" {
				modelParts = append(modelParts, gPart{Text: p.Text})
			}
		}

		if calledFn {
			contents = append(contents,
				gContent{Role: "model", Parts: modelParts},
				gContent{Role: "user", Parts: fnRespParts},
			)
			continue
		}

		// Collect text from all parts
		var reply strings.Builder
		for _, p := range parts {
			reply.WriteString(p.Text)
		}
		if reply.Len() == 0 {
			errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "empty response from gemini")
			return
		}
		// Persist the final conversation state (includes all tool call turns).
		contents = append(contents, gContent{Role: "model", Parts: []gPart{{Text: reply.String()}}})
		sess.contents = contents
		okResponse(w, r, map[string]any{"reply": reply.String(), "session_id": body.SessionID})
		return
	}

	errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "max iterations reached without text response")
}

// POST /v1/fleet/subscription/checkout
func (h *Handler) FleetCreateCheckout(w http.ResponseWriter, r *http.Request) {
	orgID, err := h.orgIDFromToken(r)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}
	if h.mpAccessToken == "" {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "payment provider not configured")
		return
	}

	ctx := r.Context()
	org, err := h.fleetStore.GetOrg(ctx, orgID)
	if err != nil {
		status, code := mapError(err)
		errorResponse(w, r, status, code, err.Error())
		return
	}

	pref := map[string]any{
		"items": []map[string]any{{
			"title":       "FlotaFR PRO — 1 mes",
			"quantity":    1,
			"unit_price":  4999.0,
			"currency_id": "ARS",
		}},
		"payer":              map[string]any{"email": org.Email},
		"external_reference": orgID,
		"notification_url":   h.backendURL + "/v1/public/mp-webhook",
		"back_urls": map[string]any{
			"success": h.frontendURL + "/subscription/success",
			"failure": h.frontendURL + "/subscription/failure",
			"pending": h.frontendURL + "/subscription/pending",
		},
		"auto_return": "approved",
	}

	b, err := json.Marshal(pref)
	if err != nil {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "failed to build checkout")
		return
	}

	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(callCtx, http.MethodPost,
		"https://api.mercadopago.com/checkout/preferences", bytes.NewReader(b))
	if err != nil {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "failed to create request")
		return
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+h.mpAccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "payment provider unavailable")
		return
	}
	defer resp.Body.Close()

	var mpResp struct {
		ID               string `json:"id"`
		InitPoint        string `json:"init_point"`
		SandboxInitPoint string `json:"sandbox_init_point"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&mpResp); err != nil || mpResp.InitPoint == "" {
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "invalid response from payment provider")
		return
	}

	okResponse(w, r, map[string]any{
		"checkout_url": mpResp.InitPoint,
		"sandbox_url":  mpResp.SandboxInitPoint,
	})
}

// POST /v1/public/mp-webhook  (no auth — called by Mercado Pago)
func (h *Handler) MPWebhook(w http.ResponseWriter, r *http.Request) {
	if h.fleetStore == nil || h.mpAccessToken == "" {
		w.WriteHeader(http.StatusOK)
		return
	}

	var notif struct {
		Type string `json:"type"`
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	_ = json.NewDecoder(r.Body).Decode(&notif)

	// MP sends topic+id as query params (old format) or as JSON body (new format).
	topic := notif.Type
	paymentID := notif.Data.ID
	if topic == "" {
		topic = r.URL.Query().Get("topic")
	}
	if paymentID == "" {
		paymentID = r.URL.Query().Get("id")
	}

	// Always acknowledge immediately — MP retries on non-200.
	w.WriteHeader(http.StatusOK)

	if topic != "payment" || paymentID == "" {
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://api.mercadopago.com/v1/payments/"+paymentID, nil)
	if err != nil {
		return
	}
	req.Header.Set("Authorization", "Bearer "+h.mpAccessToken)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()

	var payment struct {
		Status            string `json:"status"`
		ExternalReference string `json:"external_reference"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payment); err != nil {
		return
	}
	if payment.Status != "approved" || payment.ExternalReference == "" {
		return
	}

	expiresAt := time.Now().Add(30 * 24 * time.Hour)
	_ = h.fleetStore.UpdateOrgTier(ctx, payment.ExternalReference, core.TierPro, expiresAt)
}
