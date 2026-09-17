// Package config loads typed settings from environment variables. It never logs values.
package config

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	HTTP              HTTP
	MySQL             MySQL
	Redis             Redis
	Auth              Auth
	StartupTimeout    time.Duration
	ShutdownTimeout   time.Duration
	DependencyTimeout time.Duration
}

type HTTP struct {
	Addr           string
	RequestTimeout time.Duration
}

type MySQL struct {
	Addr, User, Password, Database string
	MaxOpenConns, MaxIdleConns     int
	ConnMaxLifetime                time.Duration
}

type Redis struct {
	Addr, Password string
	DB, PoolSize   int
}

type Auth struct {
	DevCodes                          bool
	CodeTTL, CodeCooldown, SessionTTL time.Duration
	MaxCodeAttempts                   int
}

func Load() (Config, error) { return LoadFrom(os.LookupEnv) }

// LoadFrom accepts a lookup function so validation is independent of process state.
func LoadFrom(lookup func(string) (string, bool)) (Config, error) {
	var cfg Config
	var firstErr error
	invalid := func(key, reason string) {
		if firstErr == nil {
			firstErr = fmt.Errorf("%s: %s", key, reason)
		}
	}
	str := func(key, fallback string) string {
		if value, ok := lookup(key); ok {
			return value
		}
		return fallback
	}
	duration := func(key string, fallback time.Duration) time.Duration {
		value, err := time.ParseDuration(str(key, fallback.String()))
		if err != nil || value <= 0 {
			invalid(key, "must be a positive duration (for example 3s)")
		}
		return value
	}
	integer := func(key string, fallback, min int) int {
		value, err := strconv.Atoi(str(key, strconv.Itoa(fallback)))
		if err != nil || value < min {
			invalid(key, fmt.Sprintf("must be an integer >= %d", min))
		}
		return value
	}
	addr := func(key, fallback string) string {
		value := str(key, fallback)
		_, port, err := net.SplitHostPort(value)
		n, parseErr := strconv.Atoi(port)
		if err != nil || parseErr != nil || n < 1 || n > 65535 {
			invalid(key, "must be host:port with port between 1 and 65535")
		}
		return value
	}
	required := func(key, fallback string) string {
		value := str(key, fallback)
		if strings.TrimSpace(value) == "" {
			invalid(key, "must not be empty")
		}
		return value
	}
	cfg.HTTP = HTTP{Addr: addr("HTTP_ADDR", "127.0.0.1:8080"), RequestTimeout: duration("HTTP_REQUEST_TIMEOUT", 3*time.Second)}
	cfg.StartupTimeout = duration("STARTUP_TIMEOUT", 10*time.Second)
	cfg.ShutdownTimeout = duration("SHUTDOWN_TIMEOUT", 10*time.Second)
	cfg.DependencyTimeout = duration("DEPENDENCY_TIMEOUT", 2*time.Second)
	cfg.MySQL = MySQL{
		Addr: addr("MYSQL_ADDR", "127.0.0.1:3306"), User: required("MYSQL_USER", "locallife"),
		Password: str("MYSQL_PASSWORD", "locallife_dev"), Database: required("MYSQL_DATABASE", "locallife"),
		MaxOpenConns: integer("MYSQL_MAX_OPEN_CONNS", 20, 1), MaxIdleConns: integer("MYSQL_MAX_IDLE_CONNS", 10, 0),
		ConnMaxLifetime: duration("MYSQL_CONN_MAX_LIFETIME", 3*time.Minute),
	}
	if cfg.MySQL.MaxIdleConns > cfg.MySQL.MaxOpenConns {
		invalid("MYSQL_MAX_IDLE_CONNS", "must not exceed MYSQL_MAX_OPEN_CONNS")
	}
	cfg.Redis = Redis{Addr: addr("REDIS_ADDR", "127.0.0.1:6379"), Password: str("REDIS_PASSWORD", ""), DB: integer("REDIS_DB", 0, 0), PoolSize: integer("REDIS_POOL_SIZE", 10, 1)}
	devCodes, err := strconv.ParseBool(str("AUTH_DEV_CODES", "false"))
	if err != nil {
		invalid("AUTH_DEV_CODES", "must be true or false")
	}
	cfg.Auth = Auth{DevCodes: devCodes, CodeTTL: duration("AUTH_CODE_TTL", 5*time.Minute), CodeCooldown: duration("AUTH_CODE_COOLDOWN", time.Minute), SessionTTL: duration("AUTH_SESSION_TTL", 30*time.Minute), MaxCodeAttempts: integer("AUTH_MAX_CODE_ATTEMPTS", 5, 1)}
	for _, setting := range []struct {
		name  string
		value time.Duration
	}{
		{"AUTH_CODE_TTL", cfg.Auth.CodeTTL},
		{"AUTH_CODE_COOLDOWN", cfg.Auth.CodeCooldown},
		{"AUTH_SESSION_TTL", cfg.Auth.SessionTTL},
	} {
		if setting.value < time.Second {
			invalid(setting.name, "must be at least 1s")
		}
	}
	if devCodes {
		host, _, _ := net.SplitHostPort(cfg.HTTP.Addr)
		ip := net.ParseIP(host)
		if host != "localhost" && (ip == nil || !ip.IsLoopback()) {
			invalid("AUTH_DEV_CODES", "requires HTTP_ADDR bound to loopback")
		}
	}
	if firstErr != nil {
		return Config{}, firstErr
	}
	return cfg, nil
}
