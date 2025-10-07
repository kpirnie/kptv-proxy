package database

import (
	"database/sql"
	"embed"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed migrations/*.sql
var migrations embed.FS

// DB wraps the sql.DB with additional functionality
type DB struct {
	*sql.DB
	logger *log.Logger
}

// Open creates a new database connection with optimized settings for WAL mode
func Open(path string, logger *log.Logger) (*DB, error) {
	// Ensure directory exists
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Open with optimized pragmas
	dsn := fmt.Sprintf("%s?_journal_mode=WAL&_busy_timeout=5000&_synchronous=NORMAL&_cache_size=10000&_foreign_keys=ON", path)
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(5 * time.Minute)

	wrapper := &DB{
		DB:     db,
		logger: logger,
	}

	// Run migrations
	if err := wrapper.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration failed: %w", err)
	}

	if logger != nil {
		logger.Println("[DATABASE] SQLite database opened successfully with WAL mode")
	}

	return wrapper, nil
}

// migrate runs all migration files
func (db *DB) migrate() error {
	// Create migrations table if not exists
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS schema_migrations (
			version INTEGER PRIMARY KEY,
			applied_at TIMESTAMP DEFAULT CURRENT_TIMESTAMP
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create migrations table: %w", err)
	}

	// Read all migration files
	entries, err := migrations.ReadDir("migrations")
	if err != nil {
		return fmt.Errorf("failed to read migrations: %w", err)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}

		// Extract version from filename (e.g., "001_initial_schema.sql" -> 1)
		var version int
		fmt.Sscanf(entry.Name(), "%d_", &version)

		// Check if already applied
		var exists bool
		err := db.QueryRow("SELECT EXISTS(SELECT 1 FROM schema_migrations WHERE version = ?)", version).Scan(&exists)
		if err != nil {
			return fmt.Errorf("failed to check migration status: %w", err)
		}

		if exists {
			continue
		}

		// Read migration file
		content, err := migrations.ReadFile(filepath.Join("migrations", entry.Name()))
		if err != nil {
			return fmt.Errorf("failed to read migration %s: %w", entry.Name(), err)
		}

		// Execute migration in transaction
		tx, err := db.Begin()
		if err != nil {
			return fmt.Errorf("failed to begin transaction: %w", err)
		}

		if _, err := tx.Exec(string(content)); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to execute migration %s: %w", entry.Name(), err)
		}

		if _, err := tx.Exec("INSERT INTO schema_migrations (version) VALUES (?)", version); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to record migration %s: %w", entry.Name(), err)
		}

		if err := tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit migration %s: %w", entry.Name(), err)
		}

		if db.logger != nil {
			db.logger.Printf("[DATABASE] Applied migration: %s", entry.Name())
		}
	}

	return nil
}

// Close closes the database connection
func (db *DB) Close() error {
	if db.logger != nil {
		db.logger.Println("[DATABASE] Closing database connection")
	}
	return db.DB.Close()
}

// Vacuum optimizes the database file
func (db *DB) Vacuum() error {
	if db.logger != nil {
		db.logger.Println("[DATABASE] Running VACUUM to optimize database")
	}
	_, err := db.Exec("VACUUM")
	return err
}

// Backup creates a backup of the database
func (db *DB) Backup(backupPath string) error {
	if db.logger != nil {
		db.logger.Printf("[DATABASE] Creating backup to: %s", backupPath)
	}

	// Ensure backup directory exists
	dir := filepath.Dir(backupPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create backup directory: %w", err)
	}

	// Use SQLite backup API
	backupDB, err := sql.Open("sqlite3", backupPath)
	if err != nil {
		return fmt.Errorf("failed to open backup database: %w", err)
	}
	defer backupDB.Close()

	// Perform backup using VACUUM INTO (SQLite 3.27.0+)
	_, err = db.Exec(fmt.Sprintf("VACUUM INTO '%s'", backupPath))
	if err != nil {
		return fmt.Errorf("backup failed: %w", err)
	}

	if db.logger != nil {
		db.logger.Printf("[DATABASE] Backup completed successfully")
	}

	return nil
}

// GetStats returns database statistics
func (db *DB) GetStats() (map[string]interface{}, error) {
	stats := make(map[string]interface{})

	// Get table counts
	tables := []string{"config", "sources", "channels", "streams", "dead_streams", "stream_orders", "import_history"}
	for _, table := range tables {
		var count int
		err := db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM %s", table)).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("failed to count %s: %w", table, err)
		}
		stats[table+"_count"] = count
	}

	// Get database size
	var pageCount, pageSize int
	err := db.QueryRow("PRAGMA page_count").Scan(&pageCount)
	if err != nil {
		return nil, fmt.Errorf("failed to get page count: %w", err)
	}
	err = db.QueryRow("PRAGMA page_size").Scan(&pageSize)
	if err != nil {
		return nil, fmt.Errorf("failed to get page size: %w", err)
	}
	stats["database_size_bytes"] = pageCount * pageSize

	return stats, nil
}
