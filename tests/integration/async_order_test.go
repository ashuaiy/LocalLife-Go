package integration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"github.com/redis/go-redis/v9"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"
)

func TestAsyncOrderRealHTTPFlow(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 2, 1)
	id := v[0].ID
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	authStore := cache.NewAuth(deps.Redis)
	tokens := make([]string, 2)
	for i, u := range users {
		var raw [32]byte
		rand.Read(raw[:])
		tokens[i] = base64.RawURLEncoding.EncodeToString(raw[:])
		if err := authStore.SaveSession(ctx, tokens[i], u.ID, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, token := range tokens {
			authStore.DeleteSession(context.Background(), token)
		}
		deps.Redis.Del(context.Background(), fmt.Sprintf("locallife:seckill:{%d}:state", id), fmt.Sprintf("locallife:seckill:{%d}:events", id))
	})
	s := service.NewAsyncOrder(repository.NewAsyncOrder(deps.DB), cache.NewSeckill(deps.Redis))
	auth := service.NewAuth(repository.NewUser(deps.DB), authStore, cfg.Auth)
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler.NewAsyncOrder(s, auth).Register(h)
	handler.NewOrder(service.NewOrder(repository.NewOrder(deps.DB)), auth).Register(h)
	request := func(method, path, token string, want int) service.SeckillView {
		t.Helper()
		res := ut.PerformRequest(h.Engine, method, path, nil, ut.Header{Key: "Authorization", Value: "Bearer " + token})
		var body struct{ Data service.SeckillView }
		if res.Code != want || json.Unmarshal(res.Body.Bytes(), &body) != nil {
			t.Fatalf("%s=%d %s", path, res.Code, res.Body)
		}
		return body.Data
	}
	accept := fmt.Sprintf("/api/v1/vouchers/%d/seckill-async", id)
	resultPath := fmt.Sprintf("/api/v1/vouchers/%d/seckill-result", id)
	if err := s.Enable(ctx, id); err != nil {
		t.Fatal(err)
	}
	ticket := request("POST", accept, tokens[0], 202)
	if ticket.Status != "pending" || ticket.Ticket == "" || ticket.OrderID != nil {
		t.Fatalf("invalid ticket %+v", ticket)
	}
	request("POST", accept, tokens[0], 409)
	request("POST", fmt.Sprintf("/api/v1/vouchers/%d/seckill", id), tokens[1], 409)
	request("GET", fmt.Sprintf("%s?user_id=%d", resultPath, users[0].ID), tokens[1], 404)
	if got := request("GET", resultPath, tokens[0], 200); got.Status != "pending" || got.Ticket != ticket.Ticket {
		t.Fatalf("pending %+v", got)
	}
	store := cache.NewSeckill(deps.Redis)
	events, err := store.Read(ctx, id, "http", 1)
	if err != nil || len(events) != 1 {
		t.Fatalf("read %v", err)
	}
	if err := s.Process(ctx, events[0]); err != nil {
		t.Fatal(err)
	}
	result := request("GET", resultPath, tokens[0], 200)
	if result.Status != "created" || result.OrderID == nil {
		t.Fatalf("result %+v", result)
	}
	request("GET", fmt.Sprintf("/api/v1/orders/%d", *result.OrderID), tokens[1], 404)
	if err := authStore.DeleteSession(ctx, tokens[0]); err != nil {
		t.Fatal(err)
	}
	request("GET", resultPath, tokens[0], 401)
}

func TestAsyncOrderConcurrentCompletionAndRedelivery(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 30, 10)
	id := v[0].ID
	store := cache.NewSeckill(deps.Redis)
	repo := repository.NewAsyncOrder(deps.DB)
	s := service.NewAsyncOrder(repo, store)
	t.Cleanup(func() {
		deps.Redis.Del(context.Background(), fmt.Sprintf("locallife:seckill:{%d}:state", id), fmt.Sprintf("locallife:seckill:{%d}:events", id))
	})
	if err := s.Enable(ctx, id); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	results := make(chan error, len(users)*2)
	for _, u := range users {
		for range 2 {
			wg.Go(func() { _, err := s.Accept(ctx, u.ID, id); results <- err })
		}
	}
	wg.Wait()
	close(results)
	accepted := 0
	for err := range results {
		if err == nil {
			accepted++
		} else {
			_, code, _ := apperror.Describe(err)
			if code != "already_purchased" && code != "sold_out" {
				t.Fatalf("unexpected admission failure: %v", err)
			}
		}
	}
	if accepted != 10 {
		t.Fatalf("accepted=%d", accepted)
	}
	orderInventory(t, deps, ctx, id, 10, 0)
	// Simulate a process that commits one order but exits before acknowledging it.
	events, err := store.Read(ctx, id, "crashed", 20)
	if err != nil || len(events) != 10 {
		t.Fatalf("events=%d err=%v", len(events), err)
	}
	pending, err := s.Result(ctx, events[0].UserID, id)
	if err != nil || pending.Status != "pending" {
		t.Fatalf("pending=%+v err=%v", pending, err)
	}
	first, err := repo.Apply(ctx, events[0])
	if err != nil {
		t.Fatal(err)
	}
	recovered, _, err := store.Claim(ctx, id, "restarted", "0-0", 0, 20)
	if err != nil || len(recovered) != 10 {
		t.Fatalf("recovered=%d err=%v", len(recovered), err)
	}
	for _, event := range recovered {
		if err := s.Process(ctx, event); err != nil {
			t.Fatal(err)
		}
		if err := s.Process(ctx, event); err != nil {
			t.Fatal(err)
		}
		result, err := s.Result(ctx, event.UserID, id)
		if err != nil || result.Status != "created" || result.OrderID == nil {
			t.Fatalf("result=%+v err=%v", result, err)
		}
	}
	again, err := s.Result(ctx, events[0].UserID, id)
	if err != nil || again.OrderID == nil || *again.OrderID != *first.OrderID {
		t.Fatalf("idempotent result=%+v err=%v", again, err)
	}
	orderInventory(t, deps, ctx, id, 0, 10)
	if n := deps.Redis.XPending(ctx, fmt.Sprintf("locallife:seckill:{%d}:events", id), "orders").Val().Count; n != 0 {
		t.Fatalf("pending=%d", n)
	}
	if err := s.Enable(ctx, id); err != nil {
		t.Fatalf("repeat enable: %v", err)
	}
	if stock := deps.Redis.HGet(ctx, fmt.Sprintf("locallife:seckill:{%d}:state", id), "stock").Val(); stock != "0" {
		t.Fatalf("stock reset: %s", stock)
	}
}

func TestAsyncOrderTransactionFailureRetainsDelivery(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 1, 1)
	id := v[0].ID
	repo := repository.NewAsyncOrder(deps.DB)
	store := cache.NewSeckill(deps.Redis)
	s := service.NewAsyncOrder(repo, store)
	state, stream := fmt.Sprintf("locallife:seckill:{%d}:state", id), fmt.Sprintf("locallife:seckill:{%d}:events", id)
	t.Cleanup(func() { deps.Redis.Del(context.Background(), state, stream) })
	if err := s.Enable(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, users[0].ID, id); err != nil {
		t.Fatal(err)
	}
	events, err := store.Read(ctx, id, "before-db-error", 1)
	if err != nil || len(events) != 1 {
		t.Fatalf("read %v", err)
	}
	// Force the final insert's UNIQUE constraint to fail AFTER stock/order writes.
	// This needs only normal application privileges, not a global trigger setting.
	conflicting := model.SeckillResult{VoucherID: id, EventID: "1-0", Generation: events[0].Generation, UserID: users[0].ID, Status: "failed", Reason: "injected_conflict", AcceptedAt: events[0].AcceptedAt}
	if err := deps.DB.Create(&conflicting).Error; err != nil {
		t.Fatal(err)
	}
	orderCode(t, s.Process(ctx, events[0]), "dependency_failure")
	orderInventory(t, deps, ctx, id, 1, 0)
	if n := deps.Redis.XPending(ctx, stream, "orders").Val().Count; n != 1 {
		t.Fatalf("delivery lost: pending=%d", n)
	}
	if deps.Redis.HGet(ctx, state, "stock").Val() != "0" {
		t.Fatal("uncertain database failure must not compensate")
	}
	if err := deps.DB.Delete(&conflicting).Error; err != nil {
		t.Fatal(err)
	}
	if err := s.Process(ctx, events[0]); err != nil {
		t.Fatal(err)
	}
	orderInventory(t, deps, ctx, id, 0, 1)
	deps.Redis.Del(ctx, state, stream)
	result, err := s.Result(ctx, users[0].ID, id)
	if err != nil || result.Status != "created" {
		t.Fatalf("committed result depends on redis: %+v %v", result, err)
	}
}

func TestAsyncOrderActivationRejectsUsedAndInvalidActivities(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 1, 2)
	if _, err := service.NewOrder(repository.NewOrder(deps.DB)).Place(ctx, users[0].ID, v[0].ID); err != nil {
		t.Fatal(err)
	}
	s := service.NewAsyncOrder(repository.NewAsyncOrder(deps.DB), cache.NewSeckill(deps.Redis))
	orderCode(t, s.Enable(ctx, v[0].ID), "conflict")
	orderCode(t, s.Enable(ctx, v[1].ID), "not_found")
	orderCode(t, s.Enable(ctx, v[3].ID), "conflict")
	row, err := repository.NewAsyncOrder(deps.DB).Activity(ctx, v[0].ID)
	if err != nil || row.AsyncState != 0 {
		t.Fatalf("failed enable fenced used activity: %+v %v", row, err)
	}
}

func TestAsyncOrderMalformedDeliveryDoesNotBlockOthers(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 1, 1)
	id := v[0].ID
	store := cache.NewSeckill(deps.Redis)
	s := service.NewAsyncOrder(repository.NewAsyncOrder(deps.DB), store)
	state, stream := fmt.Sprintf("locallife:seckill:{%d}:state", id), fmt.Sprintf("locallife:seckill:{%d}:events", id)
	t.Cleanup(func() { deps.Redis.Del(context.Background(), state, stream) })
	if err := s.Enable(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := deps.Redis.XAdd(ctx, &redis.XAddArgs{Stream: stream, Values: map[string]any{"broken": "event"}}).Err(); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, users[0].ID, id); err != nil {
		t.Fatal(err)
	}
	events, err := store.Read(ctx, id, "reader", 20)
	if err == nil || len(events) != 1 {
		t.Fatalf("valid events=%d err=%v", len(events), err)
	}
	if err := s.Process(ctx, events[0]); err != nil {
		t.Fatal(err)
	}
	if deps.Redis.XPending(ctx, stream, "orders").Val().Count != 1 {
		t.Fatal("malformed entry must remain pending")
	}
	orderInventory(t, deps, ctx, id, 0, 1)
}

func TestAsyncOrderMissingGroupRejectsAdmission(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 1, 1)
	id := v[0].ID
	s := service.NewAsyncOrder(repository.NewAsyncOrder(deps.DB), cache.NewSeckill(deps.Redis))
	state, stream := fmt.Sprintf("locallife:seckill:{%d}:state", id), fmt.Sprintf("locallife:seckill:{%d}:events", id)
	t.Cleanup(func() { deps.Redis.Del(context.Background(), state, stream) })
	if err := s.Enable(ctx, id); err != nil {
		t.Fatal(err)
	}
	if err := deps.Redis.XGroupDestroy(ctx, stream, "orders").Err(); err != nil {
		t.Fatal(err)
	}
	_, err := s.Accept(ctx, users[0].ID, id)
	orderCode(t, err, "dependency_failure")
	if deps.Redis.HGet(ctx, state, "stock").Val() != "1" || deps.Redis.XLen(ctx, stream).Val() != 0 {
		t.Fatal("admission changed a queue without its group")
	}
}

func TestAsyncOrderWorkerLifecycle(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 1, 1)
	id := v[0].ID
	s := service.NewAsyncOrder(repository.NewAsyncOrder(deps.DB), cache.NewSeckill(deps.Redis))
	t.Cleanup(func() {
		deps.Redis.Del(context.Background(), fmt.Sprintf("locallife:seckill:{%d}:state", id), fmt.Sprintf("locallife:seckill:{%d}:events", id))
	})
	if err := s.Enable(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, users[0].ID, id); err != nil {
		t.Fatal(err)
	}
	workerCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.RunWorker(workerCtx, "lifecycle", slog.New(slog.NewTextHandler(io.Discard, nil))) }()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		select {
		case <-deadline.C:
			t.Fatal("worker did not create order")
		case <-poll.C:
			result, err := s.Result(ctx, users[0].ID, id)
			if err != nil {
				t.Fatal(err)
			}
			if result.Status == "created" {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Fatal(err)
					}
				case <-time.After(time.Second):
					t.Fatal("worker did not stop")
				}
				orderInventory(t, deps, ctx, id, 0, 1)
				return
			}
		}
	}
}

func TestAsyncOrderFailureCompensatesOnce(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 1, 1)
	id := v[0].ID
	store := cache.NewSeckill(deps.Redis)
	repo := repository.NewAsyncOrder(deps.DB)
	s := service.NewAsyncOrder(repo, store)
	state, stream := fmt.Sprintf("locallife:seckill:{%d}:state", id), fmt.Sprintf("locallife:seckill:{%d}:events", id)
	t.Cleanup(func() { deps.Redis.Del(context.Background(), state, stream) })
	if err := s.Enable(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, users[0].ID, id); err != nil {
		t.Fatal(err)
	}
	events, err := store.Read(ctx, id, "consumer", 1)
	if err != nil || len(events) != 1 {
		t.Fatal(err)
	}
	if err := deps.DB.Delete(&users[0]).Error; err != nil {
		t.Fatal(err)
	}
	// A committed failure can survive a crash before compensation/ACK.
	failed, err := repo.Apply(ctx, events[0])
	if err != nil || failed.Status != "failed" {
		t.Fatalf("%+v %v", failed, err)
	}
	for range 2 {
		if err := s.Process(ctx, events[0]); err != nil {
			t.Fatal(err)
		}
	}
	if stock := deps.Redis.HGet(ctx, state, "stock").Val(); stock != "1" {
		t.Fatalf("compensated stock=%s", stock)
	}
	result, err := s.Result(ctx, users[0].ID, id)
	if err != nil || result.Status != "failed" || result.Reason != "user_not_found" {
		t.Fatalf("%+v %v", result, err)
	}
	orderInventory(t, deps, ctx, id, 1, 0)
}

func TestAsyncOrderActivationAndRedisLoss(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 2, 2)
	id := v[0].ID
	store := cache.NewSeckill(deps.Redis)
	repo := repository.NewAsyncOrder(deps.DB)
	s := service.NewAsyncOrder(repo, store)
	state, stream := fmt.Sprintf("locallife:seckill:{%d}:state", id), fmt.Sprintf("locallife:seckill:{%d}:events", id)
	t.Cleanup(func() { deps.Redis.Del(context.Background(), state, stream) })
	// Resume a crash after preparing Redis and before activating SQL.
	activity, err := repo.Stage(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if err := store.Prepare(ctx, model.SeckillSeed{VoucherID: id, Generation: activity.AsyncGeneration, Stock: activity.AsyncCapacity, BeginTime: activity.BeginTime, EndTime: activity.EndTime}); err != nil {
		t.Fatal(err)
	}
	if err := s.Enable(ctx, id); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Accept(ctx, users[0].ID, id); err != nil {
		t.Fatal(err)
	}
	deps.Redis.Del(ctx, state, stream)
	orderCode(t, s.Enable(ctx, id), "dependency_failure")
	_, err = s.Accept(ctx, users[1].ID, id)
	orderCode(t, err, "dependency_failure")
	if n := deps.Redis.Exists(ctx, state, stream).Val(); n != 0 {
		t.Fatal("lost inventory was recreated")
	}
	// Generation mismatch must fail before making a Redis reservation.
	seed := model.SeckillSeed{VoucherID: id, Generation: "ffffffffffffffffffffffffffffffff", Stock: 2, BeginTime: time.Now().Add(-time.Hour), EndTime: time.Now().Add(time.Hour)}
	if err := store.Prepare(ctx, seed); err != nil {
		t.Fatal(err)
	}
	_, err = s.Accept(ctx, users[1].ID, id)
	orderCode(t, err, "dependency_failure")
	if stock := deps.Redis.HGet(ctx, state, "stock").Val(); stock != "2" {
		t.Fatalf("stale generation stock=%s", stock)
	}
}

func TestAsyncOrderBlocksSynchronousWriter(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, vouchers := orderFixtures(t, deps, ctx, 1, 2)
	for _, state := range []int{1, 2} {
		if err := deps.DB.WithContext(ctx).Exec("UPDATE seckill_voucher SET async_state=?, async_generation=?, async_capacity=stock WHERE voucher_id=?", state, "0123456789abcdef0123456789abcdef", vouchers[0].ID).Error; err != nil {
			t.Fatal(err)
		}
		_, err := service.NewOrder(repository.NewOrder(deps.DB)).Place(ctx, users[0].ID, vouchers[0].ID)
		orderCode(t, err, "conflict")
		orderInventory(t, deps, ctx, vouchers[0].ID, 2, 0)
	}
}
