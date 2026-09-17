// Package platform owns external connection pools and their lifetimes.
package platform

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/ashuaiy/local-life-go/internal/config"
	mysqldriver "github.com/go-sql-driver/mysql"
	"github.com/redis/go-redis/v9"
	gormmysql "gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type Connections struct {
	DB    *gorm.DB
	SQL   *sql.DB
	Redis *redis.Client
}

func MySQLDSN(cfg config.MySQL, timeout time.Duration, multiStatements bool) string {
	dsn := mysqldriver.NewConfig()
	dsn.Net = "tcp"
	dsn.Addr = cfg.Addr
	dsn.User = cfg.User
	dsn.Passwd = cfg.Password
	dsn.DBName = cfg.Database
	dsn.ParseTime = true
	dsn.Loc = time.UTC
	dsn.MultiStatements = multiStatements
	dsn.Timeout = timeout
	dsn.ReadTimeout = timeout
	dsn.WriteTimeout = timeout
	dsn.Params = map[string]string{"charset": "utf8mb4", "time_zone": "'+00:00'"}
	return dsn.FormatDSN()
}

func Open(ctx context.Context, cfg config.Config) (*Connections, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	dsn, err := mysqldriver.ParseDSN(MySQLDSN(cfg.MySQL, cfg.DependencyTimeout, false))
	if err != nil {
		return nil, errors.New("invalid MySQL connection settings")
	}
	connector, err := mysqldriver.NewConnector(dsn)
	if err != nil {
		return nil, errors.New("invalid MySQL connector")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(cfg.MySQL.MaxOpenConns)
	db.SetMaxIdleConns(cfg.MySQL.MaxIdleConns)
	db.SetConnMaxLifetime(cfg.MySQL.ConnMaxLifetime)
	if err = db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, errors.New("MySQL unavailable: check MYSQL_* settings and service status")
	}
	orm, err := gorm.Open(gormmysql.New(gormmysql.Config{Conn: db, SkipInitializeWithVersion: true}), &gorm.Config{DisableAutomaticPing: true, TranslateError: true, Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		_ = db.Close()
		return nil, errors.New("MySQL ORM initialization failed")
	}
	rdb := redis.NewClient(&redis.Options{
		Addr: cfg.Redis.Addr, Password: cfg.Redis.Password, DB: cfg.Redis.DB, PoolSize: cfg.Redis.PoolSize,
		DialTimeout: cfg.DependencyTimeout, ReadTimeout: cfg.DependencyTimeout, WriteTimeout: cfg.DependencyTimeout,
		ContextTimeoutEnabled: true,
	})
	if err = rdb.Ping(ctx).Err(); err != nil {
		_ = rdb.Close()
		_ = db.Close()
		return nil, errors.New("Redis unavailable: check REDIS_* settings and service status")
	}
	return &Connections{DB: orm, SQL: db, Redis: rdb}, nil
}

func (c *Connections) Close() error { return errors.Join(c.Redis.Close(), c.SQL.Close()) }
