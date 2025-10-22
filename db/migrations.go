package db

import (
	"database/sql"
	"fmt"
	"sort"
)

// Migration represents a database migration
type Migration struct {
	Version int
	Name    string
	Up      string
	Down    string
}

// migrations holds all database migrations in order
var migrations = []Migration{
	{
		Version: 1,
		Name:    "create_scraped_data_table",
		Up: `
			CREATE TABLE IF NOT EXISTS scraped_data (
				id TEXT PRIMARY KEY,
				url TEXT NOT NULL UNIQUE,
				data TEXT NOT NULL,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);
			CREATE INDEX IF NOT EXISTS idx_scraped_data_url ON scraped_data(url);
			CREATE INDEX IF NOT EXISTS idx_scraped_data_created_at ON scraped_data(created_at);
		`,
		Down: `
			DROP INDEX IF EXISTS idx_scraped_data_created_at;
			DROP INDEX IF EXISTS idx_scraped_data_url;
			DROP TABLE IF EXISTS scraped_data;
		`,
	},
	{
		Version: 2,
		Name:    "create_schema_migrations_table",
		Up: `
			CREATE TABLE IF NOT EXISTS schema_migrations (
				version INTEGER PRIMARY KEY,
				name TEXT NOT NULL,
				applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
			);
		`,
		Down: `
			DROP TABLE IF EXISTS schema_migrations;
		`,
	},
	{
		Version: 3,
		Name:    "create_images_table",
		Up: `
			CREATE TABLE IF NOT EXISTS images (
				id TEXT PRIMARY KEY,
				scrape_id TEXT NOT NULL,
				url TEXT NOT NULL,
				alt_text TEXT,
				summary TEXT,
				tags TEXT,
				base64_data TEXT,
				created_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				updated_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP,
				FOREIGN KEY (scrape_id) REFERENCES scraped_data(id) ON DELETE CASCADE
			);
			CREATE INDEX IF NOT EXISTS idx_images_scrape_id ON images(scrape_id);
			CREATE INDEX IF NOT EXISTS idx_images_created_at ON images(created_at);
		`,
		Down: `
			DROP INDEX IF EXISTS idx_images_created_at;
			DROP INDEX IF EXISTS idx_images_scrape_id;
			DROP TABLE IF EXISTS images;
		`,
	},
	{
		Version: 4,
		Name:    "add_images_url_index",
		Up: `
			CREATE INDEX IF NOT EXISTS idx_images_url ON images(url);
		`,
		Down: `
			DROP INDEX IF EXISTS idx_images_url;
		`,
	},
	{
		Version: 5,
		Name:    "add_tombstone_datetime_to_images",
		Up: `
			ALTER TABLE images ADD COLUMN tombstone_datetime TIMESTAMP;
		`,
		Down: `
			ALTER TABLE images DROP COLUMN tombstone_datetime;
		`,
	},
	{
		Version: 6,
		Name:    "add_image_metadata_columns",
		Up: `
			ALTER TABLE images ADD COLUMN width INTEGER;
			ALTER TABLE images ADD COLUMN height INTEGER;
			ALTER TABLE images ADD COLUMN file_size_bytes INTEGER;
			ALTER TABLE images ADD COLUMN content_type TEXT;
			ALTER TABLE images ADD COLUMN exif_data TEXT;
		`,
		Down: `
			ALTER TABLE images DROP COLUMN exif_data;
			ALTER TABLE images DROP COLUMN content_type;
			ALTER TABLE images DROP COLUMN file_size_bytes;
			ALTER TABLE images DROP COLUMN height;
			ALTER TABLE images DROP COLUMN width;
		`,
	},
	{
		Version: 7,
		Name:    "add_filesystem_and_slug_to_images",
		Up: `
			ALTER TABLE images ADD COLUMN file_path TEXT;
			ALTER TABLE images ADD COLUMN slug TEXT;
			CREATE INDEX IF NOT EXISTS idx_images_slug ON images(slug);
		`,
		Down: `
			DROP INDEX IF EXISTS idx_images_slug;
			ALTER TABLE images DROP COLUMN slug;
			ALTER TABLE images DROP COLUMN file_path;
		`,
	},
	{
		Version: 8,
		Name:    "add_slug_to_scraped_data",
		Up: `
			ALTER TABLE scraped_data ADD COLUMN slug TEXT;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_scraped_data_slug ON scraped_data(slug);
		`,
		Down: `
			DROP INDEX IF EXISTS idx_scraped_data_slug;
			ALTER TABLE scraped_data DROP COLUMN slug;
		`,
	},
}

// Migrate runs all pending migrations
func Migrate(db *sql.DB) error {
	// Ensure migrations table exists (run v2 first if needed)
	if err := ensureMigrationsTable(db); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Get current version
	currentVersion, err := getCurrentVersion(db)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	// Sort migrations by version
	sortedMigrations := make([]Migration, len(migrations))
	copy(sortedMigrations, migrations)
	sort.Slice(sortedMigrations, func(i, j int) bool {
		return sortedMigrations[i].Version < sortedMigrations[j].Version
	})

	// Run pending migrations
	for _, m := range sortedMigrations {
		if m.Version <= currentVersion {
			continue
		}

		if err := runMigration(db, m); err != nil {
			return fmt.Errorf("failed to run migration %d (%s): %w", m.Version, m.Name, err)
		}
	}

	return nil
}

// ensureMigrationsTable creates the schema_migrations table if it doesn't exist
func ensureMigrationsTable(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		);
	`)
	return err
}

// getCurrentVersion returns the current migration version
func getCurrentVersion(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// runMigration executes a single migration
func runMigration(db *sql.DB, m Migration) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Execute migration
	if _, err := tx.Exec(m.Up); err != nil {
		return fmt.Errorf("failed to execute migration SQL: %w", err)
	}

	// Record migration
	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version, name) VALUES (?, ?)",
		m.Version, m.Name,
	); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	return tx.Commit()
}

// Rollback rolls back the last migration
func Rollback(db *sql.DB) error {
	currentVersion, err := getCurrentVersion(db)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	if currentVersion == 0 {
		return fmt.Errorf("no migrations to rollback")
	}

	// Find the migration to rollback
	var targetMigration *Migration
	for _, m := range migrations {
		if m.Version == currentVersion {
			targetMigration = &m
			break
		}
	}

	if targetMigration == nil {
		return fmt.Errorf("migration %d not found", currentVersion)
	}

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Execute rollback
	if _, err := tx.Exec(targetMigration.Down); err != nil {
		return fmt.Errorf("failed to rollback migration: %w", err)
	}

	// Remove migration record
	if _, err := tx.Exec("DELETE FROM schema_migrations WHERE version = ?", currentVersion); err != nil {
		return fmt.Errorf("failed to remove migration record: %w", err)
	}

	return tx.Commit()
}

// GetMigrationStatus returns the current migration status
func GetMigrationStatus(db *sql.DB) ([]MigrationStatus, error) {
	currentVersion, err := getCurrentVersion(db)
	if err != nil {
		return nil, err
	}

	var status []MigrationStatus
	for _, m := range migrations {
		s := MigrationStatus{
			Version: m.Version,
			Name:    m.Name,
			Applied: m.Version <= currentVersion,
		}
		status = append(status, s)
	}

	sort.Slice(status, func(i, j int) bool {
		return status[i].Version < status[j].Version
	})

	return status, nil
}

// MigrationStatus represents the status of a migration
type MigrationStatus struct {
	Version int
	Name    string
	Applied bool
}
