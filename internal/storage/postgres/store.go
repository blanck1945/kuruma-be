package postgres

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"flota/internal/core"

	_ "github.com/jackc/pgx/v5/stdlib"
	"golang.org/x/crypto/bcrypt"
)

// jsonArray is a sql.Scanner that reads a PostgreSQL TEXT[] via array_to_json()::text.
type jsonArray struct{ dest *[]string }

func (j jsonArray) Scan(src any) error {
	*j.dest = []string{}
	if src == nil {
		return nil
	}
	var data []byte
	switch v := src.(type) {
	case string:
		data = []byte(v)
	case []byte:
		data = v
	default:
		return fmt.Errorf("jsonArray: cannot scan %T", src)
	}
	return json.Unmarshal(data, j.dest)
}

// pqArray wraps a *[]string for scanning TEXT[] columns using array_to_json in SQL.
func pqArray(dest *[]string) jsonArray { return jsonArray{dest: dest} }

// formatPGArray formats a []string as a PostgreSQL array literal, e.g. {a,b,c}.
func formatPGArray(s []string) string {
	if len(s) == 0 {
		return "{}"
	}
	b := []byte{'{'}
	for i, v := range s {
		if i > 0 {
			b = append(b, ',')
		}
		b = append(b, '"')
		for _, c := range v {
			if c == '"' || c == '\\' {
				b = append(b, '\\')
			}
			b = append(b, byte(c))
		}
		b = append(b, '"')
	}
	b = append(b, '}')
	return string(b)
}

type Store struct {
	db *sql.DB
}

func New(dsn string) (*Store, error) {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: open: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return nil, fmt.Errorf("postgres: ping: %w", err)
	}

	s := &Store{db: db}
	if err := s.migrate(ctx); err != nil {
		return nil, fmt.Errorf("postgres: migrate: %w", err)
	}
	return s, nil
}

func (s *Store) migrate(ctx context.Context) error {
	// Create tables with full schema (for new deployments)
	_, err := s.db.ExecContext(ctx, `
		CREATE TABLE IF NOT EXISTS organizations (
			id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			name          TEXT NOT NULL,
			slug          TEXT NOT NULL UNIQUE,
			email         TEXT UNIQUE,
			password_hash TEXT NOT NULL DEFAULT '',
			created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS fleet_vehicles (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			plate      TEXT NOT NULL,
			make       TEXT NOT NULL DEFAULT '',
			model      TEXT NOT NULL DEFAULT '',
			year       INTEGER NOT NULL DEFAULT 0,
			type       TEXT NOT NULL DEFAULT '',
			notes      TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			UNIQUE(org_id, plate)
		);

		-- Add columns to existing tables if they don't exist yet
		ALTER TABLE organizations ADD COLUMN IF NOT EXISTS email TEXT UNIQUE;
		ALTER TABLE organizations ADD COLUMN IF NOT EXISTS password_hash TEXT NOT NULL DEFAULT '';
		ALTER TABLE organizations ADD COLUMN IF NOT EXISTS tier TEXT NOT NULL DEFAULT 'free';
		ALTER TABLE organizations ADD COLUMN IF NOT EXISTS tier_expires_at TIMESTAMPTZ NOT NULL DEFAULT (NOW() + INTERVAL '10 days');
		ALTER TABLE fleet_vehicles ADD COLUMN IF NOT EXISTS vtv_due_date DATE;
	`)
	if err != nil {
		return err
	}
	// Migrate vehicle_fines_cache: add source column + composite PK if schema is old
	_, err = s.db.ExecContext(ctx, `
		DO $$
		BEGIN
			IF EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = 'vehicle_fines_cache')
			   AND NOT EXISTS (SELECT 1 FROM information_schema.columns
			                   WHERE table_name = 'vehicle_fines_cache' AND column_name = 'source') THEN
				DROP TABLE vehicle_fines_cache;
			END IF;
		END $$;

		CREATE TABLE IF NOT EXISTS vehicle_fines_cache (
			plate      TEXT NOT NULL,
			source     TEXT NOT NULL DEFAULT 'all',
			fines_json JSONB NOT NULL DEFAULT '[]',
			total      INTEGER NOT NULL DEFAULT 0,
			fetched_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			PRIMARY KEY (plate, source)
		);
	`)
	if err != nil {
		return err
	}

	// Module tables + enabled_modules column
	_, err = s.db.ExecContext(ctx, `
		ALTER TABLE organizations ADD COLUMN IF NOT EXISTS
			enabled_modules TEXT[] NOT NULL DEFAULT '{}';

		CREATE TABLE IF NOT EXISTS drivers (
			id                UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			org_id            UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			name              TEXT NOT NULL,
			dni               TEXT NOT NULL DEFAULT '',
			license_number    TEXT NOT NULL DEFAULT '',
			license_expires_at DATE,
			phone             TEXT NOT NULL DEFAULT '',
			email             TEXT NOT NULL DEFAULT '',
			notes             TEXT NOT NULL DEFAULT '',
			created_at        TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			updated_at        TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS vehicle_driver_assignments (
			id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			plate         TEXT NOT NULL,
			driver_id     UUID NOT NULL REFERENCES drivers(id) ON DELETE CASCADE,
			assigned_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
			unassigned_at TIMESTAMPTZ
		);

		CREATE TABLE IF NOT EXISTS maintenance_records (
			id               UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			org_id           UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			plate            TEXT NOT NULL,
			type             TEXT NOT NULL DEFAULT 'preventive',
			description      TEXT NOT NULL,
			service_date     DATE NOT NULL,
			km_at_service    INTEGER NOT NULL DEFAULT 0,
			next_service_date DATE,
			next_service_km  INTEGER,
			cost_ars         NUMERIC(12,2) NOT NULL DEFAULT 0,
			notes            TEXT NOT NULL DEFAULT '',
			created_at       TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS vehicle_documents (
			id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			org_id     UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			plate      TEXT NOT NULL DEFAULT '',
			driver_id  UUID REFERENCES drivers(id) ON DELETE SET NULL,
			doc_type   TEXT NOT NULL,
			doc_number TEXT NOT NULL DEFAULT '',
			issued_at  DATE,
			expires_at DATE,
			notes      TEXT NOT NULL DEFAULT '',
			created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS fuel_logs (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			plate          TEXT NOT NULL,
			fill_date      DATE NOT NULL,
			liters         NUMERIC(8,2) NOT NULL DEFAULT 0,
			km_at_fill     INTEGER NOT NULL DEFAULT 0,
			cost_per_liter NUMERIC(8,2) NOT NULL DEFAULT 0,
			total_cost_ars NUMERIC(12,2) NOT NULL DEFAULT 0,
			fuel_type      TEXT NOT NULL DEFAULT 'nafta',
			notes          TEXT NOT NULL DEFAULT '',
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);

		CREATE TABLE IF NOT EXISTS vehicle_schedules (
			id             UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			org_id         UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
			plate          TEXT NOT NULL,
			driver_id      UUID NOT NULL REFERENCES drivers(id) ON DELETE CASCADE,
			scheduled_date DATE NOT NULL,
			start_time     TEXT NOT NULL DEFAULT '',
			end_time       TEXT NOT NULL DEFAULT '',
			notes          TEXT NOT NULL DEFAULT '',
			created_at     TIMESTAMPTZ NOT NULL DEFAULT NOW()
		);
	`)
	return err
}

// SeedTestOrg inserts a test organization if it doesn't exist.
func (s *Store) SeedTestOrg(ctx context.Context) error {
	hash, err := bcrypt.GenerateFromPassword([]byte("Test1234"), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO organizations (name, slug, email, password_hash)
		VALUES ('Test Organization', 'test', 'test@flota.com', $1)
		ON CONFLICT (email) DO UPDATE SET password_hash = EXCLUDED.password_hash
	`, string(hash))
	return err
}

func (s *Store) Close() error {
	return s.db.Close()
}

// --- Organizations ---

func (s *Store) CreateOrg(ctx context.Context, name, slug, email, passwordHash string) (core.Organization, error) {
	var o core.Organization
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO organizations (name, slug, email, password_hash)
		 VALUES ($1, $2, $3, $4)
		 RETURNING id, name, slug, COALESCE(email, ''), tier, tier_expires_at,
		           COALESCE(array_to_json(enabled_modules)::text, '[]'), created_at, updated_at`,
		name, slug, email, passwordHash,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Email, &o.Tier, &o.TierExpiresAt, pqArray(&o.EnabledModules), &o.CreatedAt, &o.UpdatedAt)
	if err != nil {
		return core.Organization{}, fmt.Errorf("create org: %w", err)
	}
	return o, nil
}

func (s *Store) ListOrgs(ctx context.Context) ([]core.Organization, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, slug, COALESCE(email, ''), tier, tier_expires_at,
		        COALESCE(array_to_json(enabled_modules)::text, '[]'), created_at, updated_at
		 FROM organizations ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var orgs []core.Organization
	for rows.Next() {
		var o core.Organization
		if err := rows.Scan(&o.ID, &o.Name, &o.Slug, &o.Email, &o.Tier, &o.TierExpiresAt, pqArray(&o.EnabledModules), &o.CreatedAt, &o.UpdatedAt); err != nil {
			return nil, err
		}
		orgs = append(orgs, o)
	}
	return orgs, rows.Err()
}

func (s *Store) GetOrg(ctx context.Context, id string) (core.Organization, error) {
	var o core.Organization
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, COALESCE(email, ''), tier, tier_expires_at,
		        COALESCE(array_to_json(enabled_modules)::text, '[]'), created_at, updated_at
		 FROM organizations WHERE id = $1`,
		id,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Email, &o.Tier, &o.TierExpiresAt, pqArray(&o.EnabledModules), &o.CreatedAt, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		return core.Organization{}, core.DomainError{Code: core.ErrNotFound, Message: "organization not found"}
	}
	if err != nil {
		return core.Organization{}, err
	}
	return o, nil
}

func (s *Store) GetOrgByEmail(ctx context.Context, email string) (core.Organization, error) {
	var o core.Organization
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, slug, COALESCE(email, ''), password_hash, tier, tier_expires_at,
		        COALESCE(array_to_json(enabled_modules)::text, '[]'), created_at, updated_at
		 FROM organizations WHERE email = $1`,
		email,
	).Scan(&o.ID, &o.Name, &o.Slug, &o.Email, &o.PasswordHash, &o.Tier, &o.TierExpiresAt, pqArray(&o.EnabledModules), &o.CreatedAt, &o.UpdatedAt)
	if err == sql.ErrNoRows {
		return core.Organization{}, core.DomainError{Code: core.ErrNotFound, Message: "organization not found"}
	}
	if err != nil {
		return core.Organization{}, err
	}
	return o, nil
}

func (s *Store) UpdateOrgTier(ctx context.Context, orgID, tier string, expiresAt time.Time) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE organizations SET tier = $2, tier_expires_at = $3, updated_at = NOW() WHERE id = $1`,
		orgID, tier, expiresAt,
	)
	return err
}

// --- Fleet vehicles ---

func (s *Store) AddVehicle(ctx context.Context, orgID string, v core.FleetVehicle) (core.FleetVehicle, error) {
	var out core.FleetVehicle
	var vtvNull sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO fleet_vehicles (org_id, plate, make, model, year, type, notes)
		 VALUES ($1, $2, $3, $4, $5, $6, $7)
		 ON CONFLICT (org_id, plate) DO UPDATE
		   SET make = EXCLUDED.make, model = EXCLUDED.model, year = EXCLUDED.year,
		       type = EXCLUDED.type, notes = EXCLUDED.notes, updated_at = NOW()
		 RETURNING id, org_id, plate, make, model, year, type, notes, vtv_due_date, created_at, updated_at`,
		orgID, v.Plate, v.Make, v.Model, v.Year, v.Type, v.Notes,
	).Scan(&out.ID, &out.OrgID, &out.Plate, &out.Make, &out.Model, &out.Year, &out.Type, &out.Notes, &vtvNull, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return core.FleetVehicle{}, fmt.Errorf("add vehicle: %w", err)
	}
	if vtvNull.Valid {
		out.VTVDueDate = &vtvNull.Time
	}
	return out, nil
}

func (s *Store) ListVehicles(ctx context.Context, orgID string) ([]core.FleetVehicle, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, plate, make, model, year, type, notes, vtv_due_date, created_at, updated_at
		 FROM fleet_vehicles WHERE org_id = $1 ORDER BY created_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vehicles []core.FleetVehicle
	for rows.Next() {
		var v core.FleetVehicle
		var vtvNull sql.NullTime
		if err := rows.Scan(&v.ID, &v.OrgID, &v.Plate, &v.Make, &v.Model, &v.Year, &v.Type, &v.Notes, &vtvNull, &v.CreatedAt, &v.UpdatedAt); err != nil {
			return nil, err
		}
		if vtvNull.Valid {
			v.VTVDueDate = &vtvNull.Time
		}
		vehicles = append(vehicles, v)
	}
	return vehicles, rows.Err()
}

func (s *Store) RemoveVehicle(ctx context.Context, orgID, plate string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM fleet_vehicles WHERE org_id = $1 AND plate = $2`,
		orgID, plate,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return core.DomainError{Code: core.ErrNotFound, Message: "vehicle not found in fleet"}
	}
	return nil
}

func (s *Store) ClearVehicles(ctx context.Context, orgID string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM fleet_vehicles WHERE org_id = $1`, orgID,
	)
	return err
}

// --- Fines cache ---

func (s *Store) GetFinesCache(ctx context.Context, plate, source string) (core.FinesCache, error) {
	var c core.FinesCache
	var finesJSON []byte
	c.Plate = plate
	c.Source = source
	err := s.db.QueryRowContext(ctx,
		`SELECT fines_json, total, fetched_at FROM vehicle_fines_cache WHERE plate = $1 AND source = $2`,
		plate, source,
	).Scan(&finesJSON, &c.Total, &c.FetchedAt)
	if err == sql.ErrNoRows {
		return core.FinesCache{}, core.DomainError{Code: core.ErrNotFound, Message: "no fines cache"}
	}
	if err != nil {
		return core.FinesCache{}, err
	}
	if err := json.Unmarshal(finesJSON, &c.Fines); err != nil {
		return core.FinesCache{}, err
	}
	return c, nil
}

func (s *Store) UpsertFinesCache(ctx context.Context, cache core.FinesCache) error {
	finesJSON, err := json.Marshal(cache.Fines)
	if err != nil {
		return err
	}
	_, err = s.db.ExecContext(ctx,
		`INSERT INTO vehicle_fines_cache (plate, source, fines_json, total, fetched_at)
		 VALUES ($1, $2, $3, $4, NOW())
		 ON CONFLICT (plate, source) DO UPDATE SET fines_json=$3, total=$4, fetched_at=NOW()`,
		cache.Plate, cache.Source, finesJSON, cache.Total,
	)
	return err
}

func (s *Store) ListStaleFineCaches(ctx context.Context, maxAge time.Duration) ([]core.FinesCache, error) {
	cutoff := time.Now().Add(-maxAge)
	rows, err := s.db.QueryContext(ctx,
		`SELECT plate, source, total, fetched_at FROM vehicle_fines_cache WHERE fetched_at < $1`,
		cutoff,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.FinesCache
	for rows.Next() {
		var c core.FinesCache
		if err := rows.Scan(&c.Plate, &c.Source, &c.Total, &c.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) ListFinesCacheByPlates(ctx context.Context, plates []string) ([]core.FinesCache, error) {
	if len(plates) == 0 {
		return nil, nil
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT plate, source, total, fetched_at
		 FROM vehicle_fines_cache
		 WHERE plate = ANY($1)
		 ORDER BY plate, source`,
		plates,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.FinesCache
	for rows.Next() {
		var c core.FinesCache
		if err := rows.Scan(&c.Plate, &c.Source, &c.Total, &c.FetchedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) UpdateVehicleVTV(ctx context.Context, orgID, plate string, vtvDate *time.Time) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE fleet_vehicles SET vtv_due_date=$1, updated_at=NOW() WHERE org_id=$2 AND plate=$3`,
		vtvDate, orgID, plate,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return core.DomainError{Code: core.ErrNotFound, Message: "vehicle not found in fleet"}
	}
	return nil
}

// --- Settings ---

func (s *Store) UpdateEnabledModules(ctx context.Context, orgID string, modules []string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE organizations SET enabled_modules = $2::text[], updated_at = NOW() WHERE id = $1`,
		orgID, formatPGArray(modules),
	)
	return err
}

// --- Drivers ---

func (s *Store) ListDrivers(ctx context.Context, orgID string) ([]core.Driver, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, org_id, name, dni, license_number, license_expires_at, phone, email, notes, created_at, updated_at
		 FROM drivers WHERE org_id = $1 ORDER BY name`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Driver
	for rows.Next() {
		var d core.Driver
		var lic sql.NullTime
		if err := rows.Scan(&d.ID, &d.OrgID, &d.Name, &d.DNI, &d.LicenseNumber, &lic, &d.Phone, &d.Email, &d.Notes, &d.CreatedAt, &d.UpdatedAt); err != nil {
			return nil, err
		}
		if lic.Valid {
			d.LicenseExpiresAt = &lic.Time
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) AddDriver(ctx context.Context, orgID string, d core.Driver) (core.Driver, error) {
	var out core.Driver
	var lic sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO drivers (org_id, name, dni, license_number, license_expires_at, phone, email, notes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING id, org_id, name, dni, license_number, license_expires_at, phone, email, notes, created_at, updated_at`,
		orgID, d.Name, d.DNI, d.LicenseNumber, d.LicenseExpiresAt, d.Phone, d.Email, d.Notes,
	).Scan(&out.ID, &out.OrgID, &out.Name, &out.DNI, &out.LicenseNumber, &lic, &out.Phone, &out.Email, &out.Notes, &out.CreatedAt, &out.UpdatedAt)
	if err != nil {
		return core.Driver{}, err
	}
	if lic.Valid {
		out.LicenseExpiresAt = &lic.Time
	}
	return out, nil
}

func (s *Store) UpdateDriver(ctx context.Context, orgID string, d core.Driver) (core.Driver, error) {
	var out core.Driver
	var lic sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`UPDATE drivers SET name=$3, dni=$4, license_number=$5, license_expires_at=$6,
		        phone=$7, email=$8, notes=$9, updated_at=NOW()
		 WHERE id=$1 AND org_id=$2
		 RETURNING id, org_id, name, dni, license_number, license_expires_at, phone, email, notes, created_at, updated_at`,
		d.ID, orgID, d.Name, d.DNI, d.LicenseNumber, d.LicenseExpiresAt, d.Phone, d.Email, d.Notes,
	).Scan(&out.ID, &out.OrgID, &out.Name, &out.DNI, &out.LicenseNumber, &lic, &out.Phone, &out.Email, &out.Notes, &out.CreatedAt, &out.UpdatedAt)
	if err == sql.ErrNoRows {
		return core.Driver{}, core.DomainError{Code: core.ErrNotFound, Message: "driver not found"}
	}
	if err != nil {
		return core.Driver{}, err
	}
	if lic.Valid {
		out.LicenseExpiresAt = &lic.Time
	}
	return out, nil
}

func (s *Store) ClearDrivers(ctx context.Context, orgID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM drivers WHERE org_id=$1`, orgID)
	return err
}

func (s *Store) RemoveDriver(ctx context.Context, orgID, driverID string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM drivers WHERE id=$1 AND org_id=$2`, driverID, orgID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return core.DomainError{Code: core.ErrNotFound, Message: "driver not found"}
	}
	return nil
}

func (s *Store) AssignDriver(ctx context.Context, orgID, plate, driverID string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO vehicle_driver_assignments (org_id, plate, driver_id) VALUES ($1,$2,$3)`,
		orgID, plate, driverID,
	)
	return err
}

func (s *Store) UnassignDriver(ctx context.Context, orgID, plate, driverID string) error {
	_, err := s.db.ExecContext(ctx,
		`UPDATE vehicle_driver_assignments SET unassigned_at=NOW()
		 WHERE org_id=$1 AND plate=$2 AND driver_id=$3 AND unassigned_at IS NULL`,
		orgID, plate, driverID,
	)
	return err
}

func (s *Store) GetCurrentDriver(ctx context.Context, orgID, plate string) (*core.Driver, error) {
	var d core.Driver
	var lic sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`SELECT dr.id, dr.org_id, dr.name, dr.dni, dr.license_number, dr.license_expires_at,
		        dr.phone, dr.email, dr.notes, dr.created_at, dr.updated_at
		 FROM drivers dr
		 JOIN vehicle_driver_assignments vda ON vda.driver_id = dr.id
		 WHERE vda.org_id=$1 AND vda.plate=$2 AND vda.unassigned_at IS NULL
		 ORDER BY vda.assigned_at DESC LIMIT 1`,
		orgID, plate,
	).Scan(&d.ID, &d.OrgID, &d.Name, &d.DNI, &d.LicenseNumber, &lic, &d.Phone, &d.Email, &d.Notes, &d.CreatedAt, &d.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if lic.Valid {
		d.LicenseExpiresAt = &lic.Time
	}
	return &d, nil
}

func (s *Store) ListCurrentAssignments(ctx context.Context, orgID string) ([]core.Assignment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT plate, driver_id::text
		 FROM vehicle_driver_assignments
		 WHERE org_id = $1 AND unassigned_at IS NULL
		 ORDER BY assigned_at DESC`,
		orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.Assignment
	for rows.Next() {
		var a core.Assignment
		if err := rows.Scan(&a.Plate, &a.DriverID); err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

func (s *Store) ListVehicleDriverHistory(ctx context.Context, orgID, plate string) ([]core.DriverAssignment, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT vda.driver_id::text, dr.name, vda.assigned_at, vda.unassigned_at
		 FROM vehicle_driver_assignments vda
		 JOIN drivers dr ON dr.id = vda.driver_id
		 WHERE vda.org_id=$1 AND vda.plate=$2
		 ORDER BY vda.assigned_at DESC`,
		orgID, plate,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.DriverAssignment
	for rows.Next() {
		var a core.DriverAssignment
		var ua sql.NullTime
		if err := rows.Scan(&a.DriverID, &a.DriverName, &a.AssignedAt, &ua); err != nil {
			return nil, err
		}
		if ua.Valid {
			a.UnassignedAt = &ua.Time
		} else {
			a.IsCurrent = true
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// --- Maintenance ---

func (s *Store) ListMaintenance(ctx context.Context, orgID string, plate ...string) ([]core.MaintenanceRecord, error) {
	var rows *sql.Rows
	var err error
	if len(plate) > 0 && plate[0] != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, plate, type, description, service_date, km_at_service,
			        next_service_date, next_service_km, cost_ars, notes, created_at
			 FROM maintenance_records WHERE org_id=$1 AND plate=$2 ORDER BY service_date DESC`,
			orgID, plate[0],
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, plate, type, description, service_date, km_at_service,
			        next_service_date, next_service_km, cost_ars, notes, created_at
			 FROM maintenance_records WHERE org_id=$1 ORDER BY service_date DESC`,
			orgID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.MaintenanceRecord
	for rows.Next() {
		var r core.MaintenanceRecord
		var nsd sql.NullTime
		var nsk sql.NullInt64
		if err := rows.Scan(&r.ID, &r.OrgID, &r.Plate, &r.Type, &r.Description, &r.ServiceDate,
			&r.KmAtService, &nsd, &nsk, &r.CostARS, &r.Notes, &r.CreatedAt); err != nil {
			return nil, err
		}
		if nsd.Valid {
			r.NextServiceDate = &nsd.Time
		}
		if nsk.Valid {
			v := int(nsk.Int64)
			r.NextServiceKm = &v
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) AddMaintenance(ctx context.Context, orgID string, r core.MaintenanceRecord) (core.MaintenanceRecord, error) {
	var out core.MaintenanceRecord
	var nsd sql.NullTime
	var nsk sql.NullInt64
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO maintenance_records (org_id, plate, type, description, service_date, km_at_service,
		        next_service_date, next_service_km, cost_ars, notes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10)
		 RETURNING id, org_id, plate, type, description, service_date, km_at_service,
		           next_service_date, next_service_km, cost_ars, notes, created_at`,
		orgID, r.Plate, r.Type, r.Description, r.ServiceDate, r.KmAtService,
		r.NextServiceDate, r.NextServiceKm, r.CostARS, r.Notes,
	).Scan(&out.ID, &out.OrgID, &out.Plate, &out.Type, &out.Description, &out.ServiceDate,
		&out.KmAtService, &nsd, &nsk, &out.CostARS, &out.Notes, &out.CreatedAt)
	if err != nil {
		return core.MaintenanceRecord{}, err
	}
	if nsd.Valid {
		out.NextServiceDate = &nsd.Time
	}
	if nsk.Valid {
		v := int(nsk.Int64)
		out.NextServiceKm = &v
	}
	return out, nil
}

func (s *Store) ClearMaintenance(ctx context.Context, orgID string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM maintenance_records WHERE org_id=$1`, orgID)
	return err
}

func (s *Store) RemoveMaintenance(ctx context.Context, orgID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM maintenance_records WHERE id=$1 AND org_id=$2`, id, orgID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return core.DomainError{Code: core.ErrNotFound, Message: "maintenance record not found"}
	}
	return nil
}

// --- Documents ---

func (s *Store) ListDocuments(ctx context.Context, orgID string, plate ...string) ([]core.VehicleDocument, error) {
	var rows *sql.Rows
	var err error
	if len(plate) > 0 && plate[0] != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, plate, driver_id, doc_type, doc_number, issued_at, expires_at, notes, created_at
			 FROM vehicle_documents WHERE org_id=$1 AND plate=$2 ORDER BY doc_type`,
			orgID, plate[0],
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, plate, driver_id, doc_type, doc_number, issued_at, expires_at, notes, created_at
			 FROM vehicle_documents WHERE org_id=$1 ORDER BY plate, doc_type`,
			orgID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.VehicleDocument
	for rows.Next() {
		var d core.VehicleDocument
		var did sql.NullString
		var iss, exp sql.NullTime
		if err := rows.Scan(&d.ID, &d.OrgID, &d.Plate, &did, &d.DocType, &d.DocNumber, &iss, &exp, &d.Notes, &d.CreatedAt); err != nil {
			return nil, err
		}
		if did.Valid {
			d.DriverID = &did.String
		}
		if iss.Valid {
			d.IssuedAt = &iss.Time
		}
		if exp.Valid {
			d.ExpiresAt = &exp.Time
		}
		out = append(out, d)
	}
	return out, rows.Err()
}

func (s *Store) AddDocument(ctx context.Context, orgID string, d core.VehicleDocument) (core.VehicleDocument, error) {
	var out core.VehicleDocument
	var did sql.NullString
	var iss, exp sql.NullTime
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO vehicle_documents (org_id, plate, driver_id, doc_type, doc_number, issued_at, expires_at, notes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8)
		 RETURNING id, org_id, plate, driver_id, doc_type, doc_number, issued_at, expires_at, notes, created_at`,
		orgID, d.Plate, d.DriverID, d.DocType, d.DocNumber, d.IssuedAt, d.ExpiresAt, d.Notes,
	).Scan(&out.ID, &out.OrgID, &out.Plate, &did, &out.DocType, &out.DocNumber, &iss, &exp, &out.Notes, &out.CreatedAt)
	if err != nil {
		return core.VehicleDocument{}, err
	}
	if did.Valid {
		out.DriverID = &did.String
	}
	if iss.Valid {
		out.IssuedAt = &iss.Time
	}
	if exp.Valid {
		out.ExpiresAt = &exp.Time
	}
	return out, nil
}

func (s *Store) RemoveDocument(ctx context.Context, orgID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM vehicle_documents WHERE id=$1 AND org_id=$2`, id, orgID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return core.DomainError{Code: core.ErrNotFound, Message: "document not found"}
	}
	return nil
}

// --- Fuel ---

func (s *Store) ListFuelLogs(ctx context.Context, orgID string, plate ...string) ([]core.FuelLog, error) {
	var rows *sql.Rows
	var err error
	if len(plate) > 0 && plate[0] != "" {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, plate, fill_date, liters, km_at_fill, cost_per_liter, total_cost_ars, fuel_type, notes, created_at
			 FROM fuel_logs WHERE org_id=$1 AND plate=$2 ORDER BY fill_date DESC`,
			orgID, plate[0],
		)
	} else {
		rows, err = s.db.QueryContext(ctx,
			`SELECT id, org_id, plate, fill_date, liters, km_at_fill, cost_per_liter, total_cost_ars, fuel_type, notes, created_at
			 FROM fuel_logs WHERE org_id=$1 ORDER BY fill_date DESC`,
			orgID,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.FuelLog
	for rows.Next() {
		var f core.FuelLog
		if err := rows.Scan(&f.ID, &f.OrgID, &f.Plate, &f.FillDate, &f.Liters, &f.KmAtFill,
			&f.CostPerLiter, &f.TotalCostARS, &f.FuelType, &f.Notes, &f.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, rows.Err()
}

func (s *Store) AddFuelLog(ctx context.Context, orgID string, f core.FuelLog) (core.FuelLog, error) {
	var out core.FuelLog
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO fuel_logs (org_id, plate, fill_date, liters, km_at_fill, cost_per_liter, total_cost_ars, fuel_type, notes)
		 VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		 RETURNING id, org_id, plate, fill_date, liters, km_at_fill, cost_per_liter, total_cost_ars, fuel_type, notes, created_at`,
		orgID, f.Plate, f.FillDate, f.Liters, f.KmAtFill, f.CostPerLiter, f.TotalCostARS, f.FuelType, f.Notes,
	).Scan(&out.ID, &out.OrgID, &out.Plate, &out.FillDate, &out.Liters, &out.KmAtFill,
		&out.CostPerLiter, &out.TotalCostARS, &out.FuelType, &out.Notes, &out.CreatedAt)
	if err != nil {
		return core.FuelLog{}, err
	}
	return out, nil
}

func (s *Store) RemoveFuelLog(ctx context.Context, orgID, id string) error {
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM fuel_logs WHERE id=$1 AND org_id=$2`, id, orgID,
	)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return core.DomainError{Code: core.ErrNotFound, Message: "fuel log not found"}
	}
	return nil
}

// --- Schedule ---

func (s *Store) ListSchedule(ctx context.Context, orgID string, from, to time.Time) ([]core.ScheduleEntry, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT vs.id::text, vs.org_id::text, vs.plate, vs.driver_id::text, dr.name,
		        vs.scheduled_date, vs.start_time, vs.end_time, vs.notes, vs.created_at
		 FROM vehicle_schedules vs
		 JOIN drivers dr ON dr.id = vs.driver_id
		 WHERE vs.org_id=$1 AND vs.scheduled_date BETWEEN $2 AND $3
		 ORDER BY vs.scheduled_date, vs.plate`,
		orgID, from, to,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.ScheduleEntry
	for rows.Next() {
		var e core.ScheduleEntry
		if err := rows.Scan(&e.ID, &e.OrgID, &e.Plate, &e.DriverID, &e.DriverName,
			&e.ScheduledDate, &e.StartTime, &e.EndTime, &e.Notes, &e.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, rows.Err()
}

func (s *Store) AddScheduleEntry(ctx context.Context, orgID string, e core.ScheduleEntry) (core.ScheduleEntry, error) {
	var out core.ScheduleEntry
	err := s.db.QueryRowContext(ctx,
		`INSERT INTO vehicle_schedules (org_id, plate, driver_id, scheduled_date, start_time, end_time, notes)
		 VALUES ($1, $2, $3::uuid, $4, $5, $6, $7)
		 RETURNING id::text, org_id::text, plate, driver_id::text, scheduled_date, start_time, end_time, notes, created_at`,
		orgID, e.Plate, e.DriverID, e.ScheduledDate, e.StartTime, e.EndTime, e.Notes,
	).Scan(&out.ID, &out.OrgID, &out.Plate, &out.DriverID,
		&out.ScheduledDate, &out.StartTime, &out.EndTime, &out.Notes, &out.CreatedAt)
	if err != nil {
		return core.ScheduleEntry{}, err
	}
	return out, nil
}

func (s *Store) RemoveScheduleEntry(ctx context.Context, orgID, id string) error {
	_, err := s.db.ExecContext(ctx,
		`DELETE FROM vehicle_schedules WHERE id=$1::uuid AND org_id=$2`,
		id, orgID,
	)
	return err
}
