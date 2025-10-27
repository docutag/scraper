package db

import (
	"database/sql"
	"fmt"
	"log"
	"sort"
)

// PostgreSQL-specific migrations for Scraper

var postgresMigrations = []Migration{
	{
		Version: 1,
		Name:    "create_scraped_data_table",
		Up: `
			CREATE TABLE IF NOT EXISTS scraped_data (
				id TEXT PRIMARY KEY,
				url TEXT NOT NULL UNIQUE,
				data TEXT NOT NULL,
				created_at TIMESTAMPTZ DEFAULT NOW(),
				updated_at TIMESTAMPTZ DEFAULT NOW()
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
				applied_at TIMESTAMPTZ DEFAULT NOW()
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
				created_at TIMESTAMPTZ DEFAULT NOW(),
				updated_at TIMESTAMPTZ DEFAULT NOW(),
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
			ALTER TABLE images ADD COLUMN IF NOT EXISTS tombstone_datetime TIMESTAMPTZ;
		`,
		Down: `
			ALTER TABLE images DROP COLUMN IF EXISTS tombstone_datetime;
		`,
	},
	{
		Version: 6,
		Name:    "add_image_metadata_columns",
		Up: `
			ALTER TABLE images ADD COLUMN IF NOT EXISTS width INTEGER;
			ALTER TABLE images ADD COLUMN IF NOT EXISTS height INTEGER;
			ALTER TABLE images ADD COLUMN IF NOT EXISTS file_size_bytes INTEGER;
			ALTER TABLE images ADD COLUMN IF NOT EXISTS content_type TEXT;
			ALTER TABLE images ADD COLUMN IF NOT EXISTS exif_data TEXT;
		`,
		Down: `
			ALTER TABLE images DROP COLUMN IF EXISTS exif_data;
			ALTER TABLE images DROP COLUMN IF EXISTS content_type;
			ALTER TABLE images DROP COLUMN IF EXISTS file_size_bytes;
			ALTER TABLE images DROP COLUMN IF EXISTS height;
			ALTER TABLE images DROP COLUMN IF EXISTS width;
		`,
	},
	{
		Version: 7,
		Name:    "add_filesystem_and_slug_to_images",
		Up: `
			ALTER TABLE images ADD COLUMN IF NOT EXISTS file_path TEXT;
			ALTER TABLE images ADD COLUMN IF NOT EXISTS slug TEXT;
			CREATE INDEX IF NOT EXISTS idx_images_slug ON images(slug);
		`,
		Down: `
			DROP INDEX IF EXISTS idx_images_slug;
			ALTER TABLE images DROP COLUMN IF EXISTS slug;
			ALTER TABLE images DROP COLUMN IF EXISTS file_path;
		`,
	},
	{
		Version: 8,
		Name:    "add_slug_to_scraped_data",
		Up: `
			ALTER TABLE scraped_data ADD COLUMN IF NOT EXISTS slug TEXT;
			CREATE UNIQUE INDEX IF NOT EXISTS idx_scraped_data_slug ON scraped_data(slug);
		`,
		Down: `
			DROP INDEX IF EXISTS idx_scraped_data_slug;
			ALTER TABLE scraped_data DROP COLUMN IF EXISTS slug;
		`,
	},
	{
		Version: 9,
		Name:    "add_relevance_score_to_images",
		Up: `
			ALTER TABLE images ADD COLUMN IF NOT EXISTS relevance_score REAL DEFAULT 0.5;
		`,
		Down: `
			ALTER TABLE images DROP COLUMN IF EXISTS relevance_score;
		`,
	},
	{
		Version: 10,
		Name:    "add_extracted_text_to_images",
		Up: `
			ALTER TABLE images ADD COLUMN IF NOT EXISTS extracted_text TEXT;
		`,
		Down: `
			ALTER TABLE images DROP COLUMN IF EXISTS extracted_text;
		`,
	},
}

// MigratePostgres runs all pending PostgreSQL migrations
func MigratePostgres(db *sql.DB) error {
	log.Println("Creating schema_migrations table...")
	// Ensure migrations table exists
	if err := ensureMigrationsTablePostgres(db); err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	log.Println("Checking current schema version...")
	// Get current version
	currentVersion, err := getCurrentVersionPostgres(db)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}
	log.Printf("Current schema version: %d", currentVersion)

	// Sort migrations by version
	sortedMigrations := make([]Migration, len(postgresMigrations))
	copy(sortedMigrations, postgresMigrations)
	sort.Slice(sortedMigrations, func(i, j int) bool {
		return sortedMigrations[i].Version < sortedMigrations[j].Version
	})

	// Run pending migrations
	for _, m := range sortedMigrations {
		if m.Version <= currentVersion {
			log.Printf("Skipping migration %d (already applied)", m.Version)
			continue
		}

		if err := runMigrationPostgres(db, m); err != nil {
			return fmt.Errorf("failed to run migration %d (%s): %w", m.Version, m.Name, err)
		}
	}

	log.Println("All migrations complete")
	return nil
}

// ensureMigrationsTablePostgres creates the schema_migrations table if it doesn't exist
func ensureMigrationsTablePostgres(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			applied_at TIMESTAMPTZ DEFAULT NOW()
		);
	`)
	return err
}

// getCurrentVersionPostgres returns the current migration version
func getCurrentVersionPostgres(db *sql.DB) (int, error) {
	var version int
	err := db.QueryRow("SELECT COALESCE(MAX(version), 0) FROM schema_migrations").Scan(&version)
	if err != nil {
		return 0, err
	}
	return version, nil
}

// runMigrationPostgres executes a single migration with PostgreSQL placeholders
func runMigrationPostgres(db *sql.DB, m Migration) error {
	log.Printf("Applying migration %d: %s", m.Version, m.Name)

	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Execute migration
	if _, err := tx.Exec(m.Up); err != nil {
		return fmt.Errorf("failed to execute migration SQL: %w", err)
	}

	// Record migration (use PostgreSQL $1, $2 placeholders instead of ?)
	if _, err := tx.Exec(
		"INSERT INTO schema_migrations (version, name) VALUES ($1, $2)",
		m.Version, m.Name,
	); err != nil {
		return fmt.Errorf("failed to record migration: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit migration: %w", err)
	}

	log.Printf("✓ Applied migration %d: %s", m.Version, m.Name)
	return nil
}

// RollbackPostgres rolls back the last migration
func RollbackPostgres(db *sql.DB) error {
	currentVersion, err := getCurrentVersionPostgres(db)
	if err != nil {
		return fmt.Errorf("failed to get current version: %w", err)
	}

	if currentVersion == 0 {
		return fmt.Errorf("no migrations to rollback")
	}

	// Find the migration to rollback
	var targetMigration *Migration
	for _, m := range postgresMigrations {
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

	// Remove migration record (use PostgreSQL $1 placeholder)
	if _, err := tx.Exec("DELETE FROM schema_migrations WHERE version = $1", currentVersion); err != nil {
		return fmt.Errorf("failed to remove migration record: %w", err)
	}

	return tx.Commit()
}

// GetMigrationStatusPostgres returns the current migration status
func GetMigrationStatusPostgres(db *sql.DB) ([]MigrationStatus, error) {
	currentVersion, err := getCurrentVersionPostgres(db)
	if err != nil {
		return nil, err
	}

	var status []MigrationStatus
	for _, m := range postgresMigrations {
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
