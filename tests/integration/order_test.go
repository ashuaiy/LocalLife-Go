package integration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func orderFixtures(t *testing.T, deps *platform.Connections, ctx context.Context, count int, stock int64) ([]model.User, []model.Voucher) {
	t.Helper()
	_, shops := shopFixtures(t, deps, ctx)
	users := make([]model.User, count)
	for i := range users {
		users[i].Phone = "o-" + rand.Text()
	}
	if err := deps.DB.WithContext(ctx).Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := deps.DB.Delete(&users).Error; err != nil {
			t.Error(err)
		}
	})
	vouchers := []model.Voucher{{ShopID: shops[0].ID, Title: "active", PayValue: 100, ActualValue: 200}, {ShopID: shops[0].ID, Title: "ordinary", PayValue: 100, ActualValue: 200}, {ShopID: shops[0].ID, Title: "future", PayValue: 100, ActualValue: 200}, {ShopID: shops[0].ID, Title: "ended", PayValue: 100, ActualValue: 200}}
	if err := deps.DB.WithContext(ctx).Create(&vouchers).Error; err != nil {
		t.Fatal(err)
	}
	ids := make([]uint64, len(vouchers))
	for i := range vouchers {
		ids[i] = vouchers[i].ID
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		for _, query := range []string{"DELETE FROM voucher_order WHERE voucher_id IN ?", "DELETE FROM seckill_voucher WHERE voucher_id IN ?", "DELETE FROM voucher WHERE id IN ?"} {
			if err := deps.DB.WithContext(clean).Exec(query, ids).Error; err != nil {
				t.Error(err)
			}
		}
	})
	now := time.Now().UTC()
	activities := []model.SeckillVoucher{{VoucherID: ids[0], Stock: stock, BeginTime: now.Add(-time.Hour), EndTime: now.Add(time.Hour)}, {VoucherID: ids[2], Stock: stock, BeginTime: now.Add(time.Hour), EndTime: now.Add(2 * time.Hour)}, {VoucherID: ids[3], Stock: stock, BeginTime: now.Add(-2 * time.Hour), EndTime: now.Add(-time.Hour)}}
	if err := deps.DB.WithContext(ctx).Create(&activities).Error; err != nil {
		t.Fatal(err)
	}
	return users, vouchers
}
func orderCode(t *testing.T, err error, want string) {
	t.Helper()
	_, code, _ := apperror.Describe(err)
	if code != want {
		t.Fatalf("want %s, got %s: %v", want, code, err)
	}
}
func orderInventory(t *testing.T, deps *platform.Connections, ctx context.Context, id uint64, wantStock, wantOrders int64) {
	t.Helper()
	var activity model.SeckillVoucher
	var count int64
	if err := deps.DB.WithContext(ctx).First(&activity, "voucher_id = ?", id).Error; err != nil {
		t.Fatal(err)
	}
	if err := deps.DB.WithContext(ctx).Model(&model.VoucherOrder{}).Where("voucher_id = ?", id).Count(&count).Error; err != nil {
		t.Fatal(err)
	}
	if activity.Stock != wantStock || count != wantOrders {
		t.Fatalf("stock=%d orders=%d want %d/%d", activity.Stock, count, wantStock, wantOrders)
	}
}
func TestOrderActivityRejectionsAndRollback(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 1, 2)
	s := service.NewOrder(repository.NewOrder(deps.DB))
	for _, tc := range []struct {
		id   uint64
		code string
	}{{v[1].ID, "not_found"}, {^uint64(0), "not_found"}, {v[2].ID, "activity_not_started"}, {v[3].ID, "activity_ended"}} {
		_, err := s.Place(ctx, users[0].ID, tc.id)
		orderCode(t, err, tc.code)
	}
	// Foreign-key failure occurs after the conditional decrement and must roll back that decrement.
	_, err := s.Place(ctx, ^uint64(0), v[0].ID)
	orderCode(t, err, "not_found")
	orderInventory(t, deps, ctx, v[0].ID, 2, 0)
	created, err := s.Place(ctx, users[0].ID, v[0].ID)
	if err != nil || created.ID == 0 || created.Status != 1 {
		t.Fatalf("create=%+v err=%v", created, err)
	}
	_, err = s.Place(ctx, users[0].ID, v[0].ID)
	orderCode(t, err, "already_purchased")
	orderInventory(t, deps, ctx, v[0].ID, 1, 1)
	// Simulate another writer bypassing the duplicate precheck: the UNIQUE constraint
	// must still reject the insert and roll back the preceding stock update.
	err = repository.NewOrder(deps.DB).WithinTransaction(ctx, func(tx service.OrderTransaction) error {
		if _, err := tx.LockActivity(ctx, v[0].ID); err != nil {
			return err
		}
		if _, err := tx.DecrementStock(ctx, v[0].ID); err != nil {
			return err
		}
		_, err := tx.CreateOrder(ctx, model.VoucherOrder{UserID: users[0].ID, VoucherID: v[0].ID, Status: 1})
		return err
	})
	orderCode(t, err, "already_purchased")
	orderInventory(t, deps, ctx, v[0].ID, 1, 1)
}
func TestOrderConcurrentNoOversellOrDuplicate(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 40, 15)
	s := service.NewOrder(repository.NewOrder(deps.DB))
	type result struct {
		row service.OrderView
		err error
	}
	results := make(chan result, 80)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for _, u := range users {
		for range 2 {
			wg.Go(func() { <-start; row, err := s.Place(ctx, u.ID, v[0].ID); results <- result{row, err} })
		}
	}
	close(start)
	wg.Wait()
	close(results)
	codes := map[string]int{}
	owners := map[uint64]bool{}
	for result := range results {
		if result.err == nil {
			codes["created"]++
			if result.row.ID == 0 || owners[result.row.UserID] {
				t.Fatalf("duplicate successful user=%+v", result.row)
			}
			owners[result.row.UserID] = true
			continue
		}
		_, code, _ := apperror.Describe(result.err)
		codes[code]++
	}
	if codes["created"] != 15 || codes["already_purchased"] != 15 || codes["sold_out"] != 50 || len(codes) != 3 {
		t.Fatalf("unexpected outcomes=%v", codes)
	}
	orderInventory(t, deps, ctx, v[0].ID, 0, 15)
	var distinct int64
	if err := deps.DB.WithContext(ctx).Model(&model.VoucherOrder{}).Where("voucher_id = ?", v[0].ID).Distinct("user_id").Count(&distinct).Error; err != nil || distinct != 15 {
		t.Fatalf("distinct users=%d err=%v", distinct, err)
	}
	t.Logf("80 attempts, 40 users, stock=15: created=%d duplicates=%d sold_out=%d final_stock=0 final_orders=15", codes["created"], codes["already_purchased"], codes["sold_out"])
}
func TestOrderHTTPRealAuthenticationAndOwnership(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 2, 5)
	cfg, _ := config.Load()
	store := cache.NewAuth(deps.Redis)
	tokens := make([]string, 2)
	for i, u := range users {
		raw := make([]byte, 32)
		if _, err := rand.Read(raw); err != nil {
			t.Fatal(err)
		}
		tokens[i] = base64.RawURLEncoding.EncodeToString(raw)
		if err := store.SaveSession(ctx, tokens[i], u.ID, time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		for _, token := range tokens {
			_ = store.DeleteSession(context.Background(), token)
		}
	})
	s := service.NewOrder(repository.NewOrder(deps.DB))
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler.NewOrder(s, service.NewAuth(repository.NewUser(deps.DB), store, cfg.Auth)).Register(h)
	request := func(method, path, token string, want int) json.RawMessage {
		t.Helper()
		res := ut.PerformRequest(h.Engine, method, path, nil, ut.Header{Key: "Authorization", Value: "Bearer " + token})
		var body struct {
			Data      json.RawMessage
			RequestID string `json:"request_id"`
		}
		if res.Code != want || res.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(res.Body.Bytes(), &body) != nil || body.RequestID == "" {
			t.Fatalf("%s=%d %s", path, res.Code, res.Body)
		}
		return body.Data
	}
	var created service.OrderView
	if err := json.Unmarshal(request("POST", fmt.Sprintf("/api/v1/vouchers/%d/seckill?user_id=%d", v[0].ID, users[1].ID), tokens[0], 200), &created); err != nil || created.UserID != users[0].ID || created.VoucherID != v[0].ID {
		t.Fatalf("identity=%+v err=%v", created, err)
	}
	request("POST", fmt.Sprintf("/api/v1/vouchers/%d/seckill", v[0].ID), tokens[0], 409)
	path := fmt.Sprintf("/api/v1/orders/%d", created.ID)
	request("GET", path, tokens[0], 200)
	request("GET", path, tokens[1], 404)
	for _, tc := range []struct {
		token string
		count int
	}{{tokens[0], 1}, {tokens[1], 0}} {
		var page service.OrderPage
		if err := json.Unmarshal(request("GET", fmt.Sprintf("/api/v1/orders?voucher_id=%d&user_id=%d", v[0].ID, users[0].ID), tc.token, 200), &page); err != nil || page.Items == nil || len(page.Items) != tc.count || page.HasMore {
			t.Fatalf("page=%+v err=%v", page, err)
		}
	}
	// A second activity checks real descending order and page boundaries.
	if err := deps.DB.WithContext(ctx).Model(&model.SeckillVoucher{}).Where("voucher_id = ?", v[2].ID).Updates(map[string]any{"begin_time": time.Now().UTC().Add(-time.Hour), "end_time": time.Now().UTC().Add(time.Hour)}).Error; err != nil {
		t.Fatal(err)
	}
	second, err := s.Place(ctx, users[0].ID, v[2].ID)
	if err != nil {
		t.Fatal(err)
	}
	firstPage, err := s.List(ctx, users[0].ID, 0, 1, 1)
	if err != nil || !firstPage.HasMore || len(firstPage.Items) != 1 || firstPage.Items[0].ID != second.ID {
		t.Fatalf("page1=%+v err=%v", firstPage, err)
	}
	secondPage, err := s.List(ctx, users[0].ID, 0, 2, 1)
	if err != nil || secondPage.HasMore || len(secondPage.Items) != 1 || secondPage.Items[0].ID != created.ID {
		t.Fatalf("page2=%+v err=%v", secondPage, err)
	}
	if err := store.DeleteSession(ctx, tokens[0]); err != nil {
		t.Fatal(err)
	}
	request("GET", path, tokens[0], 401)
	request("POST", fmt.Sprintf("/api/v1/vouchers/%d/seckill", v[0].ID), tokens[0], 401)
}
func TestOrderDatabaseUnavailable(t *testing.T) {
	deps, ctx := authDependencies(t)
	if err := deps.SQL.Close(); err != nil {
		t.Fatal(err)
	}
	s := service.NewOrder(repository.NewOrder(deps.DB))
	_, err := s.Place(ctx, 1, 1)
	orderCode(t, err, "dependency_failure")
	_, err = s.Detail(ctx, 1, 1)
	orderCode(t, err, "dependency_failure")
	_, err = s.List(ctx, 1, 0, 1, 10)
	orderCode(t, err, "dependency_failure")
}

type observedOrderRepository struct {
	service.Orders
	attempted chan struct{}
}
type observedOrderTransaction struct {
	service.OrderTransaction
	attempted chan struct{}
}

func (r observedOrderRepository) WithinTransaction(ctx context.Context, fn func(service.OrderTransaction) error) error {
	return r.Orders.WithinTransaction(ctx, func(tx service.OrderTransaction) error {
		return fn(observedOrderTransaction{OrderTransaction: tx, attempted: r.attempted})
	})
}
func (t observedOrderTransaction) LockActivity(ctx context.Context, id uint64) (model.SeckillVoucher, error) {
	close(t.attempted)
	return t.OrderTransaction.LockActivity(ctx, id)
}

func TestOrderLockWaitCancellationAndExpiry(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, v := orderFixtures(t, deps, ctx, 1, 2)
	repo := repository.NewOrder(deps.DB)
	blocker := deps.DB.WithContext(ctx).Begin()
	if blocker.Error != nil {
		t.Fatal(blocker.Error)
	}
	defer blocker.Rollback()
	var locked uint64
	if err := blocker.Raw("SELECT voucher_id FROM seckill_voucher WHERE voucher_id = ? FOR UPDATE", v[0].ID).Row().Scan(&locked); err != nil {
		t.Fatal(err)
	}
	short, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	_, err := service.NewOrder(repo).Place(short, users[0].ID, v[0].ID)
	cancel()
	orderCode(t, err, "timeout")
	if err := blocker.Rollback().Error; err != nil {
		t.Fatal(err)
	}
	orderInventory(t, deps, ctx, v[0].ID, 2, 0)

	// Persist a near-future end before blocking the purchaser; let that window close while it waits.
	if err := deps.DB.WithContext(ctx).Exec("UPDATE seckill_voucher SET end_time = UTC_TIMESTAMP(3) + INTERVAL 500000 MICROSECOND WHERE voucher_id = ?", v[0].ID).Error; err != nil {
		t.Fatal(err)
	}
	blocker = deps.DB.WithContext(ctx).Begin()
	if blocker.Error != nil {
		t.Fatal(blocker.Error)
	}
	defer blocker.Rollback()
	var end time.Time
	if err := blocker.Raw("SELECT end_time FROM seckill_voucher WHERE voucher_id = ? FOR UPDATE", v[0].ID).Row().Scan(&end); err != nil {
		t.Fatal(err)
	}
	attempted := make(chan struct{})
	result := make(chan error, 1)
	go func() {
		_, err := service.NewOrder(observedOrderRepository{Orders: repo, attempted: attempted}).Place(ctx, users[0].ID, v[0].ID)
		result <- err
	}()
	select {
	case <-attempted:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	for {
		var now time.Time
		if err := deps.DB.WithContext(ctx).Raw("SELECT UTC_TIMESTAMP(3)").Row().Scan(&now); err != nil {
			t.Fatal(err)
		}
		if !now.Before(end) {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("purchase escaped lock: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	if err := blocker.Commit().Error; err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-result:
		orderCode(t, err, "activity_ended")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	orderInventory(t, deps, ctx, v[0].ID, 2, 0)
}
