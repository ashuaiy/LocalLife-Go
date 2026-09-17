package platform

import (
	"context"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/go-sql-driver/mysql"
)

func TestMySQLDSNRoundTripAndConnectionPolicy(t *testing.T) {
	cfg := config.MySQL{Addr: "127.0.0.1:3306", User: "local", Password: "p@ss:word/?#", Database: "locallife"}
	parsed, err := mysql.ParseDSN(MySQLDSN(cfg, time.Second, false))
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Passwd != cfg.Password || !parsed.ParseTime || parsed.MultiStatements || parsed.Timeout != time.Second || parsed.ReadTimeout != time.Second {
		t.Fatal("unsafe or incorrect DSN")
	}
	migration, err := mysql.ParseDSN(MySQLDSN(cfg, time.Second, true))
	if err != nil || !migration.MultiStatements {
		t.Fatal("migration connection must support multiple statements")
	}
}

func TestCanceledStartupDoesNotConnect(t *testing.T) {
	cfg, err := config.LoadFrom(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	deps, err := Open(ctx, cfg)
	if err == nil || deps != nil {
		t.Fatal("canceled startup must fail")
	}
}
