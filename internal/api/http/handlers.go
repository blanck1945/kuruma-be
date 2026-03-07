package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"flota/internal/api/middleware"
	"flota/internal/core"
)

type Handler struct {
	service               *core.Service
	vehicleProfileService *core.VehicleProfileService
	fleetStore            core.FleetStore
	geminiAPIKey          string
	jwtSecret             string
	mpAccessToken         string
	backendURL            string
	frontendURL           string
	logger                *slog.Logger
	chatSessions          *chatSessionStore
}

func NewHandler(service *core.Service, logger *slog.Logger) *Handler {
	return &Handler{service: service, logger: logger, chatSessions: newChatSessionStore()}
}

func (h *Handler) SetVehicleProfileService(vehicleProfileService *core.VehicleProfileService) {
	h.vehicleProfileService = vehicleProfileService
}

func (h *Handler) SetGeminiAPIKey(key string) {
	h.geminiAPIKey = key
}

func (h *Handler) SetJWTSecret(secret string) {
	h.jwtSecret = secret
}

func (h *Handler) SetMPAccessToken(token string) {
	h.mpAccessToken = token
}

func (h *Handler) SetBackendURL(url string) {
	h.backendURL = url
}

func (h *Handler) SetFrontendURL(url string) {
	h.frontendURL = url
}

func (h *Handler) SearchFines(w http.ResponseWriter, r *http.Request) {
	plate := strings.TrimSpace(r.URL.Query().Get("plate"))
	document := strings.TrimSpace(r.URL.Query().Get("document"))
	source := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("source")))
	pbaCaptchaToken := strings.TrimSpace(r.URL.Query().Get("pba_captcha_token"))
	misionesCaptchaToken := strings.TrimSpace(r.URL.Query().Get("misiones_captcha_token"))
	saltaCaptchaToken := strings.TrimSpace(r.URL.Query().Get("salta_captcha_token"))
	jujuyCaptchaToken := strings.TrimSpace(r.URL.Query().Get("jujuy_captcha_token"))
	mendozaCaptchaToken := strings.TrimSpace(r.URL.Query().Get("mendoza_captcha_token"))
	if plate == "" && document == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "query param plate or document is required")
		return
	}
	if source != "" && source != "all" &&
		source != "caba" && source != "pba" && source != "cordoba" && source != "entrerios" &&
		source != "santafe" && source != "misiones" && source != "corrientes" && source != "chaco" &&
		source != "salta" && source != "jujuy" &&
		source != "riotercero" && source != "roquesaenzpena" && source != "villaangostura" &&
		source != "santarosa" && source != "lomasdezamora" && source != "avellaneda" &&
		source != "almirante_brown" && source != "escobar" && source != "posadas" && source != "venadotuerto" &&
		source != "mendoza" && source != "tresdefebrero" &&
		source != "lamatanza" && source != "tigre" && source != "sanmartin" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate,
			"query param source must be one of: all, caba, pba, cordoba, entrerios, santafe, misiones, corrientes, chaco, salta, jujuy, riotercero, roquesaenzpena, villaangostura, santarosa, lomasdezamora, avellaneda, almirante_brown, escobar, posadas, venadotuerto, mendoza, tresdefebrero, lamatanza, tigre, sanmartin")
		return
	}

	result, err := h.service.SearchFines(r.Context(), core.Query{
		Plate:                plate,
		Document:             document,
		Source:               source,
		PBACaptchaToken:      pbaCaptchaToken,
		MisionesCaptchaToken: misionesCaptchaToken,
		SaltaCaptchaToken:    saltaCaptchaToken,
		JujuyCaptchaToken:    jujuyCaptchaToken,
		MendozaCaptchaToken:  mendozaCaptchaToken,
	})
	if err != nil {
		status, code := mapError(err)
		h.logger.Warn("search fines failed",
			"plate", plate,
			"error", err.Error(),
			"trace_id", r.Header.Get("X-Trace-Id"),
			"status", status,
			"code", code,
		)
		errorResponse(w, r, status, code, err.Error())
		return
	}

	okResponse(w, r, projectResultByRole(result, middleware.Role(r.Context())))
}

func (h *Handler) SearchVehicleProfile(w http.ResponseWriter, r *http.Request) {
	plate := strings.TrimSpace(r.URL.Query().Get("plate"))
	if plate == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "query param plate is required")
		return
	}
	if h.vehicleProfileService == nil {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "vehicle profile service unavailable")
		return
	}

	profile, err := h.vehicleProfileService.SearchByPlate(r.Context(), plate)
	if err != nil {
		status, code := mapError(err)
		h.logger.Warn("search vehicle profile failed",
			"plate", plate,
			"error", err.Error(),
			"trace_id", r.Header.Get("X-Trace-Id"),
			"status", status,
			"code", code,
		)
		errorResponse(w, r, status, code, err.Error())
		return
	}

	okResponse(w, r, profile)
}

func mapError(err error) (int, core.ErrorCode) {
	if de, ok := err.(core.DomainError); ok {
		switch de.Code {
		case core.ErrInvalidPlate:
			return http.StatusBadRequest, de.Code
		case core.ErrNotFound:
			return http.StatusNotFound, de.Code
		case core.ErrProviderTimeout:
			return http.StatusGatewayTimeout, de.Code
		case core.ErrProviderFailed:
			return http.StatusBadGateway, de.Code
		case core.ErrUnauthorized:
			return http.StatusUnauthorized, de.Code
		case core.ErrForbidden:
			return http.StatusForbidden, de.Code
		case core.ErrRateLimited:
			return http.StatusTooManyRequests, de.Code
		case core.ErrSubscriptionRequired:
			return http.StatusPaymentRequired, de.Code
		default:
			return http.StatusInternalServerError, core.ErrInternal
		}
	}
	return http.StatusInternalServerError, core.ErrInternal
}

func projectResultByRole(in core.FineResult, role string) core.FineResult {
	_ = role
	return in
}

// ParseCSV recibe texto CSV/Excel pre-convertido, lo manda a Gemini Flash
// y devuelve un array de vehículos extraídos.

type parseCSVRequest struct {
	Content string `json:"content"`
}

// flexInt unmarshals JSON numbers or quoted strings into an int.
type flexInt int

func (f *flexInt) UnmarshalJSON(b []byte) error {
	var n int
	if err := json.Unmarshal(b, &n); err == nil {
		*f = flexInt(n)
		return nil
	}
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	if s == "" {
		*f = 0
		return nil
	}
	n64, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		*f = 0
		return nil
	}
	*f = flexInt(n64)
	return nil
}

type vehicleRow struct {
	Plate string  `json:"plate"`
	Make  string  `json:"make"`
	Year  flexInt `json:"year"`
	Type  string  `json:"type"`
}

func (h *Handler) ParseCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}

	var req parseCSVRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "body must be JSON with non-empty content field")
		return
	}

	if h.geminiAPIKey == "" {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "gemini api key not configured")
		return
	}

	rows, err := h.callGemini(r.Context(), req.Content)
	if err != nil {
		h.logger.Warn("gemini call failed", "error", err.Error())
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "AI analysis failed: "+err.Error())
		return
	}

	okResponse(w, r, rows)
}

// driverRow is the shape Gemini returns for driver imports.
type driverRow struct {
	Name             string `json:"name"`
	DNI              string `json:"dni"`
	LicenseNumber    string `json:"license_number"`
	LicenseExpiresAt string `json:"license_expires_at"`
	Phone            string `json:"phone"`
	Email            string `json:"email"`
	Notes            string `json:"notes"`
}

func (h *Handler) ParseDriversCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req parseCSVRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "body must be JSON with non-empty content field")
		return
	}
	if h.geminiAPIKey == "" {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "gemini api key not configured")
		return
	}
	rows, err := h.callGeminiDrivers(r.Context(), req.Content)
	if err != nil {
		h.logger.Warn("gemini call failed", "error", err.Error())
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "AI analysis failed: "+err.Error())
		return
	}
	okResponse(w, r, rows)
}

func (h *Handler) callGeminiDrivers(ctx context.Context, csvContent string) ([]driverRow, error) {
	prompt := "Extraé datos de conductores de esta tabla. Devolvé SOLO un JSON array sin texto adicional.\n" +
		"Cada objeto debe tener: name (nombre completo, string), dni (número de documento, string sin puntos ni espacios), " +
		"license_number (número de licencia de conducir, string), " +
		"license_expires_at (fecha de vencimiento de la licencia en formato YYYY-MM-DD, string vacío si no existe), " +
		"phone (teléfono, string), email (email, string), notes (observaciones, string).\n" +
		"Solo incluí filas que tengan al menos un nombre. Si un campo no existe ponelo string vacío.\n\n" +
		"Datos:\n" + csvContent

	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	apiURL := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=" + h.geminiAPIKey
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
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
		return nil, fmt.Errorf("gemini API error %d", resp.StatusCode)
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, err
	}
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	text := geminiResp.Candidates[0].Content.Parts[0].Text
	var rows []driverRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return nil, fmt.Errorf("could not parse Gemini response as JSON: %w", err)
	}
	return rows, nil
}

func (h *Handler) callGemini(ctx context.Context, csvContent string) ([]vehicleRow, error) {
	prompt := "Extraé datos vehiculares de esta tabla. Devolvé SOLO un JSON array sin texto adicional.\n" +
		"Cada objeto debe tener: plate (patente argentina sin guiones/espacios, mayúsculas), " +
		"make (marca, string), year (año como entero JSON sin comillas, ej: 2019), type (tipo de vehículo, string).\n" +
		"IMPORTANTE: year debe ser un número JSON (integer), nunca un string.\n" +
		"Solo incluí filas que tengan al menos una patente válida. Si un campo no existe ponelo vacío string o 0 integer.\n\n" +
		"Datos:\n" + csvContent

	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	apiURL := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=" + h.geminiAPIKey
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
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
		return nil, fmt.Errorf("gemini API error %d", resp.StatusCode)
	}

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return nil, err
	}
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return nil, fmt.Errorf("empty response from Gemini")
	}

	text := geminiResp.Candidates[0].Content.Parts[0].Text
	var rows []vehicleRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		return nil, fmt.Errorf("could not parse Gemini response as JSON: %w", err)
	}
	return rows, nil
}

// geminiText sends a prompt to Gemini and returns the raw JSON text response.
func (h *Handler) geminiText(ctx context.Context, prompt string) (string, error) {
	body := map[string]any{
		"contents": []map[string]any{
			{"parts": []map[string]any{{"text": prompt}}},
		},
		"generationConfig": map[string]any{
			"responseMimeType": "application/json",
		},
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return "", err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	apiURL := "https://generativelanguage.googleapis.com/v1beta/models/gemini-2.0-flash:generateContent?key=" + h.geminiAPIKey
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gemini API error %d", resp.StatusCode)
	}
	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct{ Text string `json:"text"` } `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return "", err
	}
	if len(geminiResp.Candidates) == 0 || len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return "", fmt.Errorf("empty response from Gemini")
	}
	return geminiResp.Candidates[0].Content.Parts[0].Text, nil
}

// ── Maintenance CSV ──────────────────────────────────────────────────────────

type maintenanceRow struct {
	Plate           string  `json:"plate"`
	Type            string  `json:"type"`
	Description     string  `json:"description"`
	ServiceDate     string  `json:"service_date"`
	KmAtService     int     `json:"km_at_service"`
	NextServiceDate string  `json:"next_service_date"`
	NextServiceKm   int     `json:"next_service_km"`
	CostARS         float64 `json:"cost_ars"`
	Notes           string  `json:"notes"`
}

func (h *Handler) ParseMaintenanceCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req parseCSVRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "body must be JSON with non-empty content field")
		return
	}
	if h.geminiAPIKey == "" {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "gemini api key not configured")
		return
	}
	prompt := "Extraé registros de mantenimiento de esta tabla. Devolvé SOLO un JSON array sin texto adicional.\n" +
		"Cada objeto debe tener: plate (patente argentina sin guiones/espacios, mayúsculas), " +
		"type (tipo: preventive/corrective/revision, usar preventive por defecto), " +
		"description (descripción del servicio, string), " +
		"service_date (fecha del servicio en formato YYYY-MM-DD), " +
		"km_at_service (km al momento del servicio, integer, 0 si no existe), " +
		"next_service_date (fecha próximo servicio YYYY-MM-DD, string vacío si no existe), " +
		"next_service_km (km próximo servicio, integer, 0 si no existe), " +
		"cost_ars (costo en pesos, número, 0 si no existe), " +
		"notes (observaciones, string).\n" +
		"Solo incluí filas que tengan patente y fecha. Datos:\n" + req.Content

	text, err := h.geminiText(r.Context(), prompt)
	if err != nil {
		h.logger.Warn("gemini call failed", "error", err.Error())
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "AI analysis failed: "+err.Error())
		return
	}
	var rows []maintenanceRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "could not parse AI response")
		return
	}
	okResponse(w, r, rows)
}

// ── Documents CSV ────────────────────────────────────────────────────────────

type documentRow struct {
	Plate     string `json:"plate"`
	DocType   string `json:"doc_type"`
	DocNumber string `json:"doc_number"`
	IssuedAt  string `json:"issued_at"`
	ExpiresAt string `json:"expires_at"`
	Notes     string `json:"notes"`
}

func (h *Handler) ParseDocumentsCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req parseCSVRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "body must be JSON with non-empty content field")
		return
	}
	if h.geminiAPIKey == "" {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "gemini api key not configured")
		return
	}
	prompt := "Extraé documentos vehiculares de esta tabla. Devolvé SOLO un JSON array sin texto adicional.\n" +
		"Cada objeto debe tener: plate (patente argentina sin guiones/espacios, mayúsculas), " +
		"doc_type (tipo de documento: seguro, vtv, habilitacion, cedula, otro), " +
		"doc_number (número de documento/póliza, string), " +
		"issued_at (fecha de emisión en formato YYYY-MM-DD, string vacío si no existe), " +
		"expires_at (fecha de vencimiento en formato YYYY-MM-DD, string vacío si no existe), " +
		"notes (observaciones, string).\n" +
		"Solo incluí filas que tengan patente y tipo de documento. Datos:\n" + req.Content

	text, err := h.geminiText(r.Context(), prompt)
	if err != nil {
		h.logger.Warn("gemini call failed", "error", err.Error())
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "AI analysis failed: "+err.Error())
		return
	}
	var rows []documentRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "could not parse AI response")
		return
	}
	okResponse(w, r, rows)
}

// ── Fuel CSV ─────────────────────────────────────────────────────────────────

type fuelRow struct {
	Plate        string  `json:"plate"`
	FillDate     string  `json:"fill_date"`
	Liters       float64 `json:"liters"`
	KmAtFill     int     `json:"km_at_fill"`
	CostPerLiter float64 `json:"cost_per_liter"`
	TotalCostARS float64 `json:"total_cost_ars"`
	FuelType     string  `json:"fuel_type"`
	Notes        string  `json:"notes"`
}

func (h *Handler) ParseFuelCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req parseCSVRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "body must be JSON with non-empty content field")
		return
	}
	if h.geminiAPIKey == "" {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "gemini api key not configured")
		return
	}
	prompt := "Extraé registros de carga de combustible de esta tabla. Devolvé SOLO un JSON array sin texto adicional.\n" +
		"Cada objeto debe tener: plate (patente argentina sin guiones/espacios, mayúsculas), " +
		"fill_date (fecha de carga en formato YYYY-MM-DD), " +
		"liters (litros cargados, número decimal, 0 si no existe), " +
		"km_at_fill (km al momento de la carga, integer, 0 si no existe), " +
		"cost_per_liter (precio por litro en pesos, número decimal, 0 si no existe), " +
		"total_cost_ars (costo total en pesos, número decimal; si no está calculado multiplicá liters * cost_per_liter), " +
		"fuel_type (tipo: nafta/gasoil/gnc/electrico, usar nafta por defecto), " +
		"notes (observaciones, string).\n" +
		"Solo incluí filas que tengan patente y fecha. Datos:\n" + req.Content

	text, err := h.geminiText(r.Context(), prompt)
	if err != nil {
		h.logger.Warn("gemini call failed", "error", err.Error())
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "AI analysis failed: "+err.Error())
		return
	}
	var rows []fuelRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "could not parse AI response")
		return
	}
	okResponse(w, r, rows)
}

// ── Schedule CSV ──────────────────────────────────────────────────────────────

type scheduleRow struct {
	Plate         string `json:"plate"`
	DriverName    string `json:"driver_name"`
	ScheduledDate string `json:"scheduled_date"`
	StartTime     string `json:"start_time"`
	EndTime       string `json:"end_time"`
	Notes         string `json:"notes"`
}

func (h *Handler) ParseScheduleCSV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.NotFound(w, r)
		return
	}
	var req parseCSVRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || strings.TrimSpace(req.Content) == "" {
		errorResponse(w, r, http.StatusBadRequest, core.ErrInvalidPlate, "body must be JSON with non-empty content field")
		return
	}
	if h.geminiAPIKey == "" {
		errorResponse(w, r, http.StatusInternalServerError, core.ErrInternal, "gemini api key not configured")
		return
	}
	prompt := "Extraé registros de horario/turno de conductores de esta tabla. Devolvé SOLO un JSON array sin texto adicional.\n" +
		"Cada objeto debe tener: plate (patente argentina sin guiones/espacios, mayúsculas), " +
		"driver_name (nombre completo del conductor, string), " +
		"scheduled_date (fecha del turno en formato YYYY-MM-DD, obligatorio), " +
		"start_time (hora de inicio en formato HH:MM, string vacío si no existe), " +
		"end_time (hora de fin en formato HH:MM, string vacío si no existe), " +
		"notes (observaciones, string vacío si no existe).\n" +
		"Solo incluí filas que tengan patente, conductor y fecha. Las fechas pueden ser pasadas, presentes o futuras. Datos:\n" + req.Content

	text, err := h.geminiText(r.Context(), prompt)
	if err != nil {
		h.logger.Warn("gemini call failed", "error", err.Error())
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "AI analysis failed: "+err.Error())
		return
	}
	var rows []scheduleRow
	if err := json.Unmarshal([]byte(text), &rows); err != nil {
		errorResponse(w, r, http.StatusBadGateway, core.ErrProviderFailed, "could not parse AI response")
		return
	}
	okResponse(w, r, rows)
}
