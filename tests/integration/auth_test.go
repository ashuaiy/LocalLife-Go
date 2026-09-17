package integration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/migration"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/internal/repository"
)

func authDependencies(t *testing.T) (*platform.Connections, context.Context) {
	t.Helper()
	if os.Getenv("RUN_INTEGRATION") != "1" {
		t.Skip("set RUN_INTEGRATION=1; see docs/testing.md")
	}
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(cfg.MySQL.Database, "_test") {
		t.Fatal("requires dedicated *_test database")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	t.Cleanup(cancel)
	if err := migration.Up(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	deps, err := platform.Open(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = deps.Close() })
	return deps, ctx
}

func TestAuthRedisAtomicCodeAndSessions(t *testing.T) {
	deps, ctx := authDependencies(t)
	store := cache.NewAuth(deps.Redis)
	phone := "test-" + rand.Text()
	issued, err := store.IssueCode(ctx, phone, "123456", time.Second, time.Second, 3)
	if err != nil || !issued {
		t.Fatalf("issue: %t %v", issued, err)
	}
	issued, err = store.IssueCode(ctx, phone, "999999", time.Second, time.Second, 3)
	if err != nil || issued {
		t.Fatal("cooldown must reject resend")
	}
	if ok, err := store.ConsumeCode(ctx, phone, "000000"); err != nil || ok {
		t.Fatal("wrong code accepted")
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			ok, err := store.ConsumeCode(ctx, phone, "123456")
			if err != nil {
				t.Error(err)
			}
			if ok {
				wins.Add(1)
			}
		}()
	}
	wg.Wait()
	if wins.Load() != 1 {
		t.Fatalf("code consumed %d times", wins.Load())
	}

	phone = "test-" + rand.Text()
	_, err = store.IssueCode(ctx, phone, "123456", time.Second, time.Second, 2)
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if ok, err := store.ConsumeCode(ctx, phone, "000000"); err != nil || ok {
			t.Fatal("incorrect code accepted")
		}
	}
	if ok, err := store.ConsumeCode(ctx, phone, "123456"); err != nil || ok {
		t.Fatal("exhausted code accepted")
	}

	phone = "test-" + rand.Text()
	_, err = store.IssueCode(ctx, phone, "123456", 30*time.Millisecond, 30*time.Millisecond, 2)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(70 * time.Millisecond)
	if ok, err := store.ConsumeCode(ctx, phone, "123456"); err != nil || ok {
		t.Fatal("expired code accepted")
	}

	token := base64.RawURLEncoding.EncodeToString([]byte(rand.Text() + "123456")[:32])
	if err := store.SaveSession(ctx, token, 42, time.Second); err != nil {
		t.Fatal(err)
	}
	id, found, err := store.SessionUser(ctx, token)
	if err != nil || !found || id != 42 {
		t.Fatalf("session=%d %t %v", id, found, err)
	}
	// Reading halfway through must not turn a fixed deadline into sliding expiry.
	time.Sleep(600 * time.Millisecond)
	_, found, err = store.SessionUser(ctx, token)
	if err != nil || !found {
		t.Fatal("session expired before its original TTL")
	}
	time.Sleep(500 * time.Millisecond)
	_, found, err = store.SessionUser(ctx, token)
	if err != nil || found {
		t.Fatal("expired session survived read")
	}
	if err := store.SaveSession(ctx, token, 42, time.Second); err != nil {
		t.Fatal(err)
	}
	if err := store.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	_, found, err = store.SessionUser(ctx, token)
	if err != nil || found {
		t.Fatal("logout did not remove session")
	}
}

func TestUserRepositoryConcurrentRegistration(t *testing.T) {
	deps, ctx := authDependencies(t)
	repo := repository.NewUser(deps.DB)
	phone := "test-" + rand.Text()
	t.Cleanup(func() {
		_ = deps.DB.WithContext(context.Background()).Exec("DELETE FROM users WHERE phone=?", phone).Error
	})
	ids := make(chan uint64, 16)
	errs := make(chan error, 16)
	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			user, err := repo.FindOrCreate(ctx, phone)
			if err != nil {
				errs <- err
				return
			}
			ids <- user.ID
		}()
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	var first uint64
	for id := range ids {
		if first == 0 {
			first = id
		}
		if id != first {
			t.Fatalf("different user IDs: %d %d", first, id)
		}
	}
	var count int64
	if err := deps.DB.WithContext(ctx).Table("users").Where("phone=?", phone).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("users=%d err=%v", count, err)
	}
	user, err := repo.ByID(ctx, first)
	if err != nil || user.Phone != phone {
		t.Fatal(fmt.Sprintf("load created user: %v", err))
	}
}
