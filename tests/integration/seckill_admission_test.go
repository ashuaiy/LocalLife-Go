package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/redis/go-redis/v9"
)

func admissionFixture(t testing.TB, stock int64) (*platform.Connections, context.Context, *cache.Seckill, model.SeckillSeed, string, string) {
	t.Helper()
	deps, ctx := authDependencies(t)
	_, shops := shopFixtures(t, deps, ctx)
	// This Redis-only primitive test uses a fixture-unique identifier; no orders are created.
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		t.Fatal(err)
	}
	now, err := deps.Redis.Time(ctx).Result()
	if err != nil {
		t.Fatal(err)
	}
	seed := model.SeckillSeed{VoucherID: shops[0].ID, Generation: hex.EncodeToString(raw), Stock: stock, BeginTime: now.Add(-time.Minute), EndTime: now.Add(time.Minute)}
	prefix := fmt.Sprintf("locallife:seckill:{%d}", seed.VoucherID)
	state, events := prefix+":state", prefix+":events"
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := deps.Redis.Del(clean, state, events).Err(); err != nil {
			t.Error(err)
		}
	})
	return deps, ctx, cache.NewSeckill(deps.Redis), seed, state, events
}

func TestSeckillAdmissionConcurrent(t *testing.T) {
	deps, ctx, s, seed, state, events := admissionFixture(t, 15)
	if err := s.Prepare(ctx, seed); err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		event model.SeckillEvent
		err   error
	}
	results := make(chan outcome, 80)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for userID := uint64(1); userID <= 40; userID++ {
		for range 2 {
			wg.Go(func() { <-start; event, err := s.Reserve(ctx, seed.VoucherID, userID); results <- outcome{event, err} })
		}
	}
	close(start)
	wg.Wait()
	close(results)
	codes := map[string]int{}
	accepted := map[uint64]string{}
	for result := range results {
		if result.err == nil {
			if result.event.ID == "" || result.event.VoucherID != seed.VoucherID || result.event.Generation != seed.Generation || accepted[result.event.UserID] != "" || result.event.AcceptedAt.Before(seed.BeginTime) || !result.event.AcceptedAt.Before(seed.EndTime) {
				t.Fatalf("invalid admission=%+v", result.event)
			}
			accepted[result.event.UserID] = result.event.ID
			codes["accepted"]++
		} else {
			_, code, _ := apperror.Describe(result.err)
			codes[code]++
		}
	}
	if codes["accepted"] != 15 || codes["already_purchased"] != 15 || codes["sold_out"] != 50 || len(codes) != 3 {
		t.Fatalf("outcomes=%v", codes)
	}
	if stock, err := deps.Redis.HGet(ctx, state, "stock").Int64(); err != nil || stock != 0 {
		t.Fatalf("stock=%d err=%v", stock, err)
	}
	entries, err := deps.Redis.XRange(ctx, events, "-", "+").Result()
	if err != nil || len(entries) != 15 {
		t.Fatalf("events=%d err=%v", len(entries), err)
	}
	for _, entry := range entries {
		userID, err := strconv.ParseUint(fmt.Sprint(entry.Values["user_id"]), 10, 64)
		if err != nil || accepted[userID] != entry.ID || entry.Values["voucher_id"] != strconv.FormatUint(seed.VoucherID, 10) || entry.Values["generation"] != seed.Generation {
			t.Fatalf("unexpected event=%+v", entry)
		}
		receipt, err := s.Receipt(ctx, seed.VoucherID, userID)
		if err != nil || receipt.ID != entry.ID || receipt.Generation != seed.Generation {
			t.Fatalf("receipt=%+v err=%v", receipt, err)
		}
	}
	groups, err := deps.Redis.XInfoGroups(ctx, events).Result()
	if err != nil || len(groups) != 1 || groups[0].Name != "orders" || groups[0].LastDeliveredID != "0-0" {
		t.Fatalf("groups=%+v err=%v", groups, err)
	}
	if err := s.Prepare(ctx, seed); err == nil {
		t.Fatal("preparation replenished an existing activity")
	} else {
		orderCode(t, err, "conflict")
	}
	if n, _ := deps.Redis.HGet(ctx, state, "stock").Int64(); n != 0 {
		t.Fatal("retry changed stock")
	}
	t.Log("80 attempts: accepted=15 duplicate=15 sold_out=50; stock=0 stream=15; receipts and consumer group verified")
}

func TestSeckillAdmissionRejectsWithoutMutation(t *testing.T) {
	for _, scenario := range []string{"future", "ended", "empty", "missing_state", "missing_stream", "wrong_stream_type", "invalid_stock", "xadd_failure"} {
		t.Run(scenario, func(t *testing.T) {
			deps, ctx, s, seed, state, events := admissionFixture(t, 2)
			want := "dependency_failure"
			switch scenario {
			case "future":
				seed.BeginTime = time.Now().Add(time.Minute)
				seed.EndTime = seed.BeginTime.Add(time.Minute)
				want = "activity_not_started"
			case "ended":
				seed.EndTime = time.Now().Add(-time.Second)
				seed.BeginTime = seed.EndTime.Add(-time.Minute)
				want = "activity_ended"
			case "empty":
				seed.Stock = 0
				want = "sold_out"
			}
			if err := s.Prepare(ctx, seed); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "missing_state":
				if err := deps.Redis.Del(ctx, state).Err(); err != nil {
					t.Fatal(err)
				}
			case "missing_stream":
				if err := deps.Redis.Del(ctx, events).Err(); err != nil {
					t.Fatal(err)
				}
			case "wrong_stream_type":
				if err := deps.Redis.Set(ctx, events, "wrong", 0).Err(); err != nil {
					t.Fatal(err)
				}
			case "invalid_stock":
				if err := deps.Redis.HSet(ctx, state, "stock", "1e3").Err(); err != nil {
					t.Fatal(err)
				}
			case "xadd_failure":
				if err := deps.Redis.XAdd(ctx, &redis.XAddArgs{Stream: events, ID: "18446744073709551615-18446744073709551615", Values: map[string]any{"sentinel": "1"}}).Err(); err != nil {
					t.Fatal(err)
				}
			}
			before, _ := deps.Redis.HGetAll(ctx, state).Result()
			_, err := s.Reserve(ctx, seed.VoucherID, 7)
			orderCode(t, err, want)
			after, _ := deps.Redis.HGetAll(ctx, state).Result()
			if fmt.Sprint(before) != fmt.Sprint(after) {
				t.Fatalf("rejection mutated state: before=%v after=%v", before, after)
			}
			if _, ok := after["buyer:7"]; ok {
				t.Fatal("rejection recorded buyer")
			}
			if scenario != "wrong_stream_type" {
				count, err := deps.Redis.XLen(ctx, events).Result()
				wantCount := int64(0)
				if scenario == "xadd_failure" {
					wantCount = 1
				}
				if err != nil || count != wantCount {
					t.Fatalf("event count=%d err=%v", count, err)
				}
			}
		})
	}
}

func TestSeckillAdmissionValidationAndReceipt(t *testing.T) {
	deps, ctx, s, seed, state, events := admissionFixture(t, 2)
	for _, change := range []func(*model.SeckillSeed){
		func(s *model.SeckillSeed) { s.Stock = -1 }, func(s *model.SeckillSeed) { s.Stock = 9007199254740992 },
		func(s *model.SeckillSeed) { s.VoucherID = 0 }, func(s *model.SeckillSeed) { s.Generation = "bad" }, func(s *model.SeckillSeed) { s.EndTime = s.BeginTime },
	} {
		invalid := seed
		change(&invalid)
		orderCode(t, s.Prepare(ctx, invalid), "validation")
	}
	if n, err := deps.Redis.Exists(ctx, state, events).Result(); err != nil || n != 0 {
		t.Fatal("invalid initialization wrote state")
	}
	if err := s.Prepare(ctx, seed); err != nil {
		t.Fatal(err)
	}
	_, err := s.Receipt(ctx, seed.VoucherID, 7)
	orderCode(t, err, "not_found")
	_, err = s.Reserve(ctx, 0, 7)
	orderCode(t, err, "validation")
	_, err = s.Reserve(ctx, seed.VoucherID, 0)
	orderCode(t, err, "validation")
	// IDs stay strings across Lua and can use the full uint64 range without rounding.
	userID := ^uint64(0)
	event, err := s.Reserve(ctx, seed.VoucherID, userID)
	if err != nil || event.UserID != userID {
		t.Fatalf("large ID admission=%+v err=%v", event, err)
	}
	_, err = s.Reserve(ctx, seed.VoucherID, userID)
	orderCode(t, err, "already_purchased")
	receipt, err := s.Receipt(ctx, seed.VoucherID, userID)
	if err != nil || receipt != event {
		t.Fatalf("receipt=%+v event=%+v err=%v", receipt, event, err)
	}
	if ttl, err := deps.Redis.TTL(ctx, state).Result(); err != nil || ttl != -1 {
		t.Fatalf("reservation state expired: ttl=%s err=%v", ttl, err)
	}
	if err := deps.Redis.XDel(ctx, events, event.ID).Err(); err != nil {
		t.Fatal(err)
	}
	_, err = s.Receipt(ctx, seed.VoucherID, userID)
	orderCode(t, err, "dependency_failure")
	if err := deps.Redis.HSet(ctx, state, "stock", strings.Repeat("9", 30)).Err(); err != nil {
		t.Fatal(err)
	}
	_, err = s.Reserve(ctx, seed.VoucherID, 9)
	orderCode(t, err, "dependency_failure")
}

func TestSeckillAdmissionACLDenialHasNoPartialWrites(t *testing.T) {
	for _, command := range []string{"hset", "xadd", "xdel"} {
		t.Run(command, func(t *testing.T) {
			deps, ctx, s, seed, state, events := admissionFixture(t, 2)
			username := "admission-test-" + rand.Text()
			password := rand.Text()
			if err := deps.Redis.Do(ctx, "ACL", "SETUSER", username, "on", ">"+password, "+@all", "~locallife:seckill:{"+strconv.FormatUint(seed.VoucherID, 10)+"}:*", "-"+command).Err(); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				clean, cancel := context.WithTimeout(context.Background(), time.Second)
				defer cancel()
				if err := deps.Redis.Do(clean, "ACL", "DELUSER", username).Err(); err != nil {
					t.Error(err)
				}
			})
			opts := *deps.Redis.Options()
			opts.Username = username
			opts.Password = password
			client := redis.NewClient(&opts)
			t.Cleanup(func() { _ = client.Close() })
			if actual, err := client.Do(ctx, "ACL", "WHOAMI").Text(); err != nil || actual != username {
				t.Fatal("test client did not authenticate as restricted user")
			}
			restricted := cache.NewSeckill(client)
			if command == "hset" {
				orderCode(t, restricted.Prepare(ctx, seed), "dependency_failure")
				if n, err := deps.Redis.Exists(ctx, state, events).Result(); err != nil || n != 0 {
					t.Fatal("denied preparation left partial keys")
				}
			}
			if err := s.Prepare(ctx, seed); err != nil {
				t.Fatal(err)
			}
			_, err := restricted.Reserve(ctx, seed.VoucherID, 7)
			orderCode(t, err, "dependency_failure")
			if stock, err := deps.Redis.HGet(ctx, state, "stock").Int64(); err != nil || stock != 2 {
				t.Fatalf("stock=%d err=%v", stock, err)
			}
			if n, err := deps.Redis.XLen(ctx, events).Result(); err != nil || n != 0 {
				t.Fatalf("events=%d err=%v", n, err)
			}
			if marked, err := deps.Redis.HExists(ctx, state, "buyer:7").Result(); err != nil || marked {
				t.Fatal("denied admission marked buyer")
			}
		})
	}
}

func TestSeckillAdmissionMaximumIDs(t *testing.T) {
	deps, ctx, s, seed, _, _ := admissionFixture(t, 1)
	seed.VoucherID = ^uint64(0)
	prefix := "locallife:seckill:{" + strconv.FormatUint(seed.VoucherID, 10) + "}"
	keys := []string{prefix + ":state", prefix + ":events"}
	if n, err := deps.Redis.Exists(ctx, keys...).Result(); err != nil || n != 0 {
		t.Fatal("maximum-ID fixture keys already exist; refusing to overwrite")
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := deps.Redis.Del(clean, keys...).Err(); err != nil {
			t.Error(err)
		}
	})
	if err := s.Prepare(ctx, seed); err != nil {
		t.Fatal(err)
	}
	event, err := s.Reserve(ctx, seed.VoucherID, ^uint64(0))
	if err != nil || event.VoucherID != seed.VoucherID || event.UserID != ^uint64(0) {
		t.Fatalf("large IDs=%+v err=%v", event, err)
	}
	receipt, err := s.Receipt(ctx, seed.VoucherID, ^uint64(0))
	if err != nil || receipt != event {
		t.Fatalf("large IDs receipt=%+v err=%v", receipt, err)
	}
	entries, err := deps.Redis.XRange(ctx, keys[1], "-", "+").Result()
	if err != nil || len(entries) != 1 || entries[0].Values["voucher_id"] != "18446744073709551615" || entries[0].Values["user_id"] != "18446744073709551615" {
		t.Fatalf("event IDs not preserved: %+v err=%v", entries, err)
	}
}

func BenchmarkSeckillLuaAdmission(b *testing.B) {
	deps, ctx, s, seed, state, events := admissionFixture(b, int64(b.N)+1)
	if err := s.Prepare(ctx, seed); err != nil {
		b.Fatal(err)
	}
	if _, err := s.Reserve(ctx, seed.VoucherID, 1); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := s.Reserve(ctx, seed.VoucherID, uint64(i)+2); err != nil {
			b.Fatal(err)
		}
	}
	b.StopTimer()
	if n, err := deps.Redis.XLen(ctx, events).Result(); err != nil || n != int64(b.N)+1 {
		b.Fatalf("events=%d err=%v", n, err)
	}
	if stock, err := deps.Redis.HGet(ctx, state, "stock").Int64(); err != nil || stock != 0 {
		b.Fatalf("stock=%d err=%v", stock, err)
	}
}
