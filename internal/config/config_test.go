package config

import (
	"strings"
	"testing"
	"time"
)

func lookup(values map[string]string) func(string) (string, bool) {
	return func(key string) (string, bool) { v, ok := values[key]; return v, ok }
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := LoadFrom(lookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != "127.0.0.1:8080" || cfg.MySQL.Database != "locallife" || cfg.Redis.DB != 0 {
		t.Fatalf("unexpected defaults: addr=%q database=%q redis_db=%d", cfg.HTTP.Addr, cfg.MySQL.Database, cfg.Redis.DB)
	}
	if cfg.HTTP.RequestTimeout != 3*time.Second || cfg.DependencyTimeout != 2*time.Second {
		t.Fatal("unexpected timeouts")
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := LoadFrom(lookup(map[string]string{
		"HTTP_ADDR": ":9090", "HTTP_REQUEST_TIMEOUT": "7s", "MYSQL_MAX_OPEN_CONNS": "12",
		"MYSQL_MAX_IDLE_CONNS": "0", "REDIS_DB": "2", "REDIS_PASSWORD": " space : @ password ",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.HTTP.Addr != ":9090" || cfg.HTTP.RequestTimeout != 7*time.Second || cfg.MySQL.MaxOpenConns != 12 || cfg.MySQL.MaxIdleConns != 0 || cfg.Redis.DB != 2 || cfg.Redis.Password != " space : @ password " {
		t.Fatal("environment overrides were not preserved")
	}
}

func TestLoadRejectsInvalidConfigurationWithoutLeakingValues(t *testing.T) {
	for key, value := range map[string]string{
		"HTTP_ADDR": "missing-port", "HTTP_REQUEST_TIMEOUT": "0s", "SHUTDOWN_TIMEOUT": "-1s",
		"STARTUP_TIMEOUT": "bad-secret-value", "DEPENDENCY_TIMEOUT": "0",
		"MYSQL_ADDR": "bad-address", "MYSQL_USER": "", "MYSQL_DATABASE": "",
		"MYSQL_MAX_OPEN_CONNS": "0", "MYSQL_MAX_IDLE_CONNS": "-1",
		"MYSQL_CONN_MAX_LIFETIME": "-1s", "REDIS_ADDR": "localhost:abc", "REDIS_DB": "-1", "REDIS_POOL_SIZE": "0",
	} {
		t.Run(key, func(t *testing.T) {
			_, err := LoadFrom(lookup(map[string]string{key: value}))
			if err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("expected %s validation error, got %v", key, err)
			}
			if strings.Contains(err.Error(), "bad-secret-value") {
				t.Fatal("configuration value leaked")
			}
		})
	}
	_, err := LoadFrom(lookup(map[string]string{"MYSQL_MAX_OPEN_CONNS": "2", "MYSQL_MAX_IDLE_CONNS": "3"}))
	if err == nil {
		t.Fatal("idle pool cannot exceed open pool")
	}
}

func TestAuthConfigurationDefaultsAndDevModeGuard(t *testing.T) {
	cfg, err := LoadFrom(lookup(nil))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Auth.DevCodes || cfg.Auth.CodeTTL != 5*time.Minute || cfg.Auth.CodeCooldown != time.Minute || cfg.Auth.SessionTTL != 30*time.Minute || cfg.Auth.MaxCodeAttempts != 5 {
		t.Fatal("unexpected auth defaults")
	}
	for _, host := range []string{"0.0.0.0:8080", ":8080", "192.168.1.2:8080"} {
		if _, err := LoadFrom(lookup(map[string]string{"AUTH_DEV_CODES": "true", "HTTP_ADDR": host})); err == nil {
			t.Fatalf("dev codes exposed on %s", host)
		}
	}
	for _, host := range []string{"127.0.0.1:8080", "[::1]:8080", "localhost:8080"} {
		if _, err := LoadFrom(lookup(map[string]string{"AUTH_DEV_CODES": "true", "HTTP_ADDR": host})); err != nil {
			t.Fatal(err)
		}
	}
	for key, value := range map[string]string{"AUTH_DEV_CODES": "bad", "AUTH_CODE_TTL": "0s", "AUTH_CODE_COOLDOWN": "-1s", "AUTH_SESSION_TTL": "0s", "AUTH_MAX_CODE_ATTEMPTS": "0"} {
		if _, err := LoadFrom(lookup(map[string]string{key: value})); err == nil {
			t.Fatalf("invalid %s accepted", key)
		}
	}
}

func TestAuthDurationsHaveWholeSecondMinimum(t *testing.T) {
	for _, key := range []string{"AUTH_CODE_TTL", "AUTH_CODE_COOLDOWN", "AUTH_SESSION_TTL"} {
		if _, err := LoadFrom(lookup(map[string]string{key: "500ms"})); err == nil {
			t.Fatalf("%s must reject a duration below one second", key)
		}
		if _, err := LoadFrom(lookup(map[string]string{key: "1s"})); err != nil {
			t.Fatal(err)
		}
	}
}
