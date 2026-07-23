package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/pressly/goose/v3"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type OpenOptions struct {
	MaxOpenConnections    int
	MaxIdleConnections    int
	ConnectionMaxLifetime time.Duration
	ConnectionMaxIdleTime time.Duration
	LogLevel              logger.LogLevel
}

func DefaultOpenOptions() OpenOptions {
	return OpenOptions{
		MaxOpenConnections:    30,
		MaxIdleConnections:    10,
		ConnectionMaxLifetime: 30 * time.Minute,
		ConnectionMaxIdleTime: 5 * time.Minute,
		LogLevel:              logger.Warn,
	}
}

func Open(ctx context.Context, databaseURL string) (*gorm.DB, error) {
	return OpenWithOptions(ctx, databaseURL, DefaultOpenOptions())
}

func OpenWithOptions(ctx context.Context, databaseURL string, options OpenOptions) (*gorm.DB, error) {
	if databaseURL == "" {
		return nil, errors.New("database URL is required")
	}

	db, err := gorm.Open(postgres.Open(databaseURL), &gorm.Config{
		TranslateError: true,
		Logger:         logger.Default.LogMode(options.LogLevel),
	})
	if err != nil {
		return nil, fmt.Errorf("open PostgreSQL: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get PostgreSQL pool: %w", err)
	}
	if options.MaxOpenConnections > 0 {
		sqlDB.SetMaxOpenConns(options.MaxOpenConnections)
	}
	if options.MaxIdleConnections >= 0 {
		sqlDB.SetMaxIdleConns(options.MaxIdleConnections)
	}
	if options.ConnectionMaxLifetime > 0 {
		sqlDB.SetConnMaxLifetime(options.ConnectionMaxLifetime)
	}
	if options.ConnectionMaxIdleTime > 0 {
		sqlDB.SetConnMaxIdleTime(options.ConnectionMaxIdleTime)
	}
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	return db, nil
}

func Close(db *gorm.DB) error {
	if db == nil {
		return nil
	}
	sqlDB, err := db.DB()
	if err != nil {
		return fmt.Errorf("get PostgreSQL pool: %w", err)
	}
	return sqlDB.Close()
}

func SQLDB(db *gorm.DB) (*sql.DB, error) {
	if db == nil {
		return nil, errors.New("database is nil")
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("get PostgreSQL pool: %w", err)
	}
	return sqlDB, nil
}

func RunMigrations(ctx context.Context, db *gorm.DB, migrationsDir string) error {
	if migrationsDir == "" {
		return errors.New("migrations directory is required")
	}
	sqlDB, err := SQLDB(db)
	if err != nil {
		return err
	}
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.UpContext(ctx, sqlDB, migrationsDir); err != nil {
		return fmt.Errorf("run migrations: %w", err)
	}
	return nil
}
