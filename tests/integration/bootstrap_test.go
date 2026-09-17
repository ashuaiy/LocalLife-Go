package integration

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/migration"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/cloudwego/hertz/pkg/common/ut"
	mysqldriver "github.com/go-sql-driver/mysql"
)

// These tests use real MySQL and Redis. Opt in against a dedicated *_test database.
func TestBootstrap(t *testing.T) {
	if os.Getenv("RUN_INTEGRATION") != "1" {
		t.Skip("set RUN_INTEGRATION=1; see docs/testing.md")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.MySQL.Database, "_test") {
		t.Fatal("integration tests require MYSQL_DATABASE ending in _test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := migration.Up(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err := migration.Up(ctx, cfg); err != nil {
		t.Fatalf("repeated migration must be a no-op: %v", err)
	}
	deps, err := platform.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer deps.Close()

	t.Run("schema and order constraints", func(t *testing.T) {
		var version uint
		var dirty bool
		if err := deps.SQL.QueryRowContext(ctx, "SELECT version,dirty FROM schema_migrations").Scan(&version, &dirty); err != nil || version != 1 || dirty {
			t.Fatalf("schema version=%d dirty=%t err=%v", version, dirty, err)
		}
		for _, table := range []string{"users", "shop_type", "shop", "blog", "follow", "voucher", "seckill_voucher", "voucher_order"} {
			var count int
			if err := deps.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.tables WHERE table_schema=? AND table_name=?", cfg.MySQL.Database, table).Scan(&count); err != nil || count != 1 {
				t.Fatalf("missing %s: %v", table, err)
			}
		}
		tx, err := deps.SQL.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		insert := func(query string, args ...any) int64 {
			t.Helper()
			res, err := tx.ExecContext(ctx, query, args...)
			if err != nil {
				t.Fatal(err)
			}
			id, err := res.LastInsertId()
			if err != nil {
				t.Fatal(err)
			}
			return id
		}
		userID := insert("INSERT INTO users(phone) VALUES (?)", rand.Text())
		typeID := insert("INSERT INTO shop_type(name) VALUES (?)", rand.Text())
		shopID := insert("INSERT INTO shop(type_id,name,longitude,latitude) VALUES (?, 'test shop', 116.4,39.9)", typeID)
		voucherID := insert("INSERT INTO voucher(shop_id,title,pay_value,actual_value) VALUES (?, 'test voucher',100,200)", shopID)
		insert("INSERT INTO seckill_voucher(voucher_id,stock,begin_time,end_time) VALUES (?,1,UTC_TIMESTAMP(),DATE_ADD(UTC_TIMESTAMP(),INTERVAL 1 HOUR))", voucherID)
		insert("INSERT INTO voucher_order(user_id,voucher_id) VALUES (?,?)", userID, voucherID)
		_, err = tx.ExecContext(ctx, "INSERT INTO voucher_order(user_id,voucher_id) VALUES (?,?)", userID, voucherID)
		var duplicate *mysqldriver.MySQLError
		if !errors.As(err, &duplicate) || duplicate.Number != 1062 {
			t.Fatalf("unique order constraint not enforced: %v", err)
		}
		for i, want := range []int64{1, 0} {
			res, err := tx.ExecContext(ctx, "UPDATE seckill_voucher SET stock=stock-1 WHERE voucher_id=? AND stock>0", voucherID)
			if err != nil {
				t.Fatal(err)
			}
			rows, _ := res.RowsAffected()
			if rows != want {
				t.Fatalf("deduction %d affected %d rows", i, rows)
			}
		}
		if _, err := tx.ExecContext(ctx, "UPDATE seckill_voucher SET stock=-1 WHERE voucher_id=?", voucherID); err == nil {
			t.Fatal("negative stock must fail")
		}
	})
	t.Run("GORM context and pool", func(t *testing.T) {
		canceled, cancel := context.WithCancel(ctx)
		cancel()
		var value int
		if err := deps.DB.WithContext(canceled).Raw("SELECT 1").Scan(&value).Error; !errors.Is(err, context.Canceled) {
			t.Fatalf("context not propagated: %v", err)
		}
		if deps.SQL.Stats().MaxOpenConnections != cfg.MySQL.MaxOpenConns {
			t.Fatal("pool settings not applied")
		}
	})
	t.Run("Redis round trip", func(t *testing.T) {
		key := "locallife:test:" + rand.Text()
		defer deps.Redis.Del(context.Background(), key)
		if err := deps.Redis.Set(ctx, key, "ok", time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
		value, err := deps.Redis.Get(ctx, key).Result()
		if err != nil || value != "ok" {
			t.Fatalf("Redis round trip: %q %v", value, err)
		}
		if ttl := deps.Redis.TTL(ctx, key).Val(); ttl <= 0 || ttl > time.Minute {
			t.Fatalf("invalid TTL: %s", ttl)
		}
	})
	t.Run("readiness with real dependencies", func(t *testing.T) {
		h := app.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), map[string]app.Check{"mysql": deps.SQL.PingContext, "redis": func(ctx context.Context) error { return deps.Redis.Ping(ctx).Err() }})
		res := ut.PerformRequest(h.Engine, "GET", "/readyz", nil)
		if res.Code != 200 {
			t.Fatalf("unready: %d %s", res.Code, res.Body)
		}
		if err := deps.Redis.Close(); err != nil {
			t.Fatal(err)
		}
		res = ut.PerformRequest(h.Engine, "GET", "/readyz", nil)
		if res.Code != 503 {
			t.Fatalf("closed Redis must make service unready: %d", res.Code)
		}
	})
}
