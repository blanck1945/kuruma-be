package vehicledb

import (
	"context"
	"database/sql"
	"time"

	"flota/internal/core"
	_ "modernc.org/sqlite"
)

type SQLiteStore struct {
	db *sql.DB
}

func NewSQLiteStore(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	if err := db.Ping(); err != nil {
		return nil, err
	}
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS vehicle_profiles (
		plate      TEXT PRIMARY KEY,
		make       TEXT,
		model      TEXT,
		year       INTEGER,
		type       TEXT,
		fuel       TEXT,
		source     TEXT,
		confidence TEXT,
		fetched_at TEXT
	)`); err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db}, nil
}

func (s *SQLiteStore) Get(ctx context.Context, plate string) (core.VehicleProfile, bool, error) {
	row := s.db.QueryRowContext(ctx,
		`SELECT plate, make, model, year, type, fuel, source, confidence, fetched_at FROM vehicle_profiles WHERE plate = ?`,
		plate,
	)
	var p core.VehicleProfile
	var fetchedAt string
	err := row.Scan(&p.Plate, &p.Make, &p.Model, &p.Year, &p.Type, &p.Fuel, &p.Source, &p.Confidence, &fetchedAt)
	if err == sql.ErrNoRows {
		return core.VehicleProfile{}, false, nil
	}
	if err != nil {
		return core.VehicleProfile{}, false, err
	}
	p.FetchedAt, _ = time.Parse(time.RFC3339, fetchedAt)
	return p, true, nil
}

func (s *SQLiteStore) Save(ctx context.Context, p core.VehicleProfile) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO vehicle_profiles (plate, make, model, year, type, fuel, source, confidence, fetched_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		p.Plate, p.Make, p.Model, p.Year, p.Type, p.Fuel, p.Source, p.Confidence, p.FetchedAt.UTC().Format(time.RFC3339),
	)
	return err
}

func (s *SQLiteStore) Close() error {
	return s.db.Close()
}
