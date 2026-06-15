// Package db opens the application's sqlite database via gorm and runs
// embedded goose migrations at boot. Goose owns the schema; gorm is used
// only for queries (no AutoMigrate).
package db

import (
	"database/sql"
	"embed"
	"fmt"

	"github.com/glebarez/sqlite"
	"github.com/pressly/goose/v3"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"
)

//go:embed migrations/*.sql
var migrationsFS embed.FS

// Open opens (or creates) the sqlite database at path and optionally
// applies any pending goose migrations.
func Open(path string, migrateOnBoot bool) (*gorm.DB, error) {
	gdb, err := gorm.Open(sqlite.Open(path+"?_pragma=journal_mode(WAL)&_pragma=foreign_keys(ON)"), &gorm.Config{
		Logger: gormlogger.Default.LogMode(gormlogger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("db open: %w", err)
	}
	if migrateOnBoot {
		sqlDB, err := gdb.DB()
		if err != nil {
			return nil, fmt.Errorf("db handle: %w", err)
		}
		if err := migrate(sqlDB); err != nil {
			return nil, err
		}
	}
	return gdb, nil
}

func migrate(sqlDB *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("db migrate dialect: %w", err)
	}
	if err := goose.Up(sqlDB, "migrations"); err != nil {
		return fmt.Errorf("db migrate up: %w", err)
	}
	return nil
}
