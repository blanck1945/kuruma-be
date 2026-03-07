package core

import (
	"context"
	"time"
)

const (
	TierFree     = "free"
	TierPro      = "pro"
	TierBusiness = "business"
)

type Organization struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Slug           string    `json:"slug"`
	Email          string    `json:"email,omitempty"`
	PasswordHash   string    `json:"-"`
	Tier           string    `json:"tier"`
	TierExpiresAt  time.Time `json:"tier_expires_at"`
	EnabledModules []string  `json:"enabled_modules"`
	CreatedAt      time.Time `json:"created_at"`
	UpdatedAt      time.Time `json:"updated_at"`
}

type FleetVehicle struct {
	ID         string     `json:"id"`
	OrgID      string     `json:"org_id"`
	Plate      string     `json:"plate"`
	Make       string     `json:"make,omitempty"`
	Model      string     `json:"model,omitempty"`
	Year       int        `json:"year,omitempty"`
	Type       string     `json:"type,omitempty"`
	Notes      string     `json:"notes,omitempty"`
	VTVDueDate *time.Time `json:"vtv_due_date,omitempty"`
	CreatedAt  time.Time  `json:"created_at"`
	UpdatedAt  time.Time  `json:"updated_at"`
}

type FinesCache struct {
	Plate     string    `json:"plate"`
	Source    string    `json:"source"`
	Fines     []Fine    `json:"fines"`
	Total     int       `json:"total"`
	FetchedAt time.Time `json:"fetched_at"`
}

type Assignment struct {
	Plate    string `json:"plate"`
	DriverID string `json:"driver_id"`
}

type DriverAssignment struct {
	DriverID     string     `json:"driver_id"`
	DriverName   string     `json:"driver_name"`
	AssignedAt   time.Time  `json:"assigned_at"`
	UnassignedAt *time.Time `json:"unassigned_at,omitempty"`
	IsCurrent    bool       `json:"is_current"`
}

// --- Module types ---

type Driver struct {
	ID               string     `json:"id"`
	OrgID            string     `json:"org_id"`
	Name             string     `json:"name"`
	DNI              string     `json:"dni"`
	LicenseNumber    string     `json:"license_number"`
	LicenseExpiresAt *time.Time `json:"license_expires_at,omitempty"`
	Phone            string     `json:"phone"`
	Email            string     `json:"email"`
	Notes            string     `json:"notes"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
}

type MaintenanceRecord struct {
	ID              string     `json:"id"`
	OrgID           string     `json:"org_id"`
	Plate           string     `json:"plate"`
	Type            string     `json:"type"`
	Description     string     `json:"description"`
	ServiceDate     time.Time  `json:"service_date"`
	KmAtService     int        `json:"km_at_service"`
	NextServiceDate *time.Time `json:"next_service_date,omitempty"`
	NextServiceKm   *int       `json:"next_service_km,omitempty"`
	CostARS         float64    `json:"cost_ars"`
	Notes           string     `json:"notes"`
	CreatedAt       time.Time  `json:"created_at"`
}

type VehicleDocument struct {
	ID        string     `json:"id"`
	OrgID     string     `json:"org_id"`
	Plate     string     `json:"plate"`
	DriverID  *string    `json:"driver_id,omitempty"`
	DocType   string     `json:"doc_type"`
	DocNumber string     `json:"doc_number"`
	IssuedAt  *time.Time `json:"issued_at,omitempty"`
	ExpiresAt *time.Time `json:"expires_at,omitempty"`
	Notes     string     `json:"notes"`
	CreatedAt time.Time  `json:"created_at"`
}

type FuelLog struct {
	ID           string    `json:"id"`
	OrgID        string    `json:"org_id"`
	Plate        string    `json:"plate"`
	FillDate     time.Time `json:"fill_date"`
	Liters       float64   `json:"liters"`
	KmAtFill     int       `json:"km_at_fill"`
	CostPerLiter float64   `json:"cost_per_liter"`
	TotalCostARS float64   `json:"total_cost_ars"`
	FuelType     string    `json:"fuel_type"`
	Notes        string    `json:"notes"`
	CreatedAt    time.Time `json:"created_at"`
}

type ScheduleEntry struct {
	ID            string    `json:"id"`
	OrgID         string    `json:"org_id"`
	Plate         string    `json:"plate"`
	DriverID      string    `json:"driver_id"`
	DriverName    string    `json:"driver_name,omitempty"`
	ScheduledDate time.Time `json:"scheduled_date"`
	StartTime     string    `json:"start_time"`
	EndTime       string    `json:"end_time"`
	Notes         string    `json:"notes"`
	CreatedAt     time.Time `json:"created_at"`
}

type FleetStore interface {
	CreateOrg(ctx context.Context, name, slug, email, passwordHash string) (Organization, error)
	ListOrgs(ctx context.Context) ([]Organization, error)
	GetOrg(ctx context.Context, id string) (Organization, error)
	GetOrgByEmail(ctx context.Context, email string) (Organization, error)
	AddVehicle(ctx context.Context, orgID string, v FleetVehicle) (FleetVehicle, error)
	ListVehicles(ctx context.Context, orgID string) ([]FleetVehicle, error)
	RemoveVehicle(ctx context.Context, orgID, plate string) error
	ClearVehicles(ctx context.Context, orgID string) error
	GetFinesCache(ctx context.Context, plate, source string) (FinesCache, error)
	UpsertFinesCache(ctx context.Context, cache FinesCache) error
	ListStaleFineCaches(ctx context.Context, maxAge time.Duration) ([]FinesCache, error)
	ListFinesCacheByPlates(ctx context.Context, plates []string) ([]FinesCache, error)
	UpdateVehicleVTV(ctx context.Context, orgID, plate string, vtvDate *time.Time) error
	UpdateOrgTier(ctx context.Context, orgID, tier string, expiresAt time.Time) error

	// Settings
	UpdateEnabledModules(ctx context.Context, orgID string, modules []string) error

	// Drivers
	ListDrivers(ctx context.Context, orgID string) ([]Driver, error)
	AddDriver(ctx context.Context, orgID string, d Driver) (Driver, error)
	UpdateDriver(ctx context.Context, orgID string, d Driver) (Driver, error)
	RemoveDriver(ctx context.Context, orgID, driverID string) error
	ClearDrivers(ctx context.Context, orgID string) error
	AssignDriver(ctx context.Context, orgID, plate, driverID string) error
	UnassignDriver(ctx context.Context, orgID, plate, driverID string) error
	GetCurrentDriver(ctx context.Context, orgID, plate string) (*Driver, error)
	ListCurrentAssignments(ctx context.Context, orgID string) ([]Assignment, error)
	ListVehicleDriverHistory(ctx context.Context, orgID, plate string) ([]DriverAssignment, error)

	// Maintenance
	ListMaintenance(ctx context.Context, orgID string, plate ...string) ([]MaintenanceRecord, error)
	AddMaintenance(ctx context.Context, orgID string, r MaintenanceRecord) (MaintenanceRecord, error)
	RemoveMaintenance(ctx context.Context, orgID, id string) error
	ClearMaintenance(ctx context.Context, orgID string) error

	// Documents
	ListDocuments(ctx context.Context, orgID string, plate ...string) ([]VehicleDocument, error)
	AddDocument(ctx context.Context, orgID string, d VehicleDocument) (VehicleDocument, error)
	RemoveDocument(ctx context.Context, orgID, id string) error

	// Fuel
	ListFuelLogs(ctx context.Context, orgID string, plate ...string) ([]FuelLog, error)
	AddFuelLog(ctx context.Context, orgID string, f FuelLog) (FuelLog, error)
	RemoveFuelLog(ctx context.Context, orgID, id string) error

	// Schedule
	ListSchedule(ctx context.Context, orgID string, from, to time.Time) ([]ScheduleEntry, error)
	AddScheduleEntry(ctx context.Context, orgID string, e ScheduleEntry) (ScheduleEntry, error)
	RemoveScheduleEntry(ctx context.Context, orgID, id string) error
}
