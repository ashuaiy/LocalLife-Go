// Package migration applies reviewed, embedded SQL. The HTTP server never changes schema.
package migration

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/migrations"
	_ "github.com/go-sql-driver/mysql"
	"github.com/golang-migrate/migrate/v4"
	migratemysql "github.com/golang-migrate/migrate/v4/database/mysql"
	"github.com/golang-migrate/migrate/v4/source/iofs"
)

func Up(ctx context.Context, cfg config.Config) (result error) {
	if err := ctx.Err(); err != nil {
		return err
	}
	db, err := sql.Open("mysql", platform.MySQLDSN(cfg.MySQL, cfg.DependencyTimeout, true))
	if err != nil {
		return errors.New("invalid migration connection settings")
	}
	defer db.Close()
	conn, err := db.Conn(ctx)
	if err != nil {
		return errors.New("migration MySQL unavailable: check MYSQL_* settings and service status")
	}
	// The migrate driver owns this connection only after successful construction.
	driver, err := migratemysql.WithConnection(ctx, conn, &migratemysql.Config{DatabaseName: cfg.MySQL.Database, StatementTimeout: cfg.StartupTimeout})
	if err != nil {
		_ = conn.Close()
		return errors.New("migration driver initialization failed: check database availability and privileges")
	}
	source, err := iofs.New(migrations.Files, ".")
	if err != nil {
		_ = driver.Close()
		return fmt.Errorf("migration source: %w", err)
	}
	m, err := migrate.NewWithInstance("iofs", source, cfg.MySQL.Database, driver)
	if err != nil {
		_ = source.Close()
		_ = driver.Close()
		return fmt.Errorf("migration initialization: %w", err)
	}
	defer func() { sourceErr, dbErr := m.Close(); result = errors.Join(result, sourceErr, dbErr) }()
	m.LockTimeout = cfg.StartupTimeout
	done := make(chan struct{})
	defer close(done)
	go func() {
		select {
		case <-ctx.Done():
			m.GracefulStop <- true
		case <-done:
		}
	}()
	err = m.Up()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if errors.Is(err, migrate.ErrNoChange) {
		return nil
	}
	return err
}
