package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"testing"
	"time"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestVoucherQueriesAndActivityHTTP(t *testing.T) {
	deps, ctx := authDependencies(t)
	_, shops := shopFixtures(t, deps, ctx)
	rows := []model.Voucher{
		{ShopID: shops[0].ID, Title: "普通券", PayValue: 9007199254740993, ActualValue: ^uint64(0)},
		{ShopID: shops[0].ID, Title: "进行中", PayValue: 9900, ActualValue: 15000},
		{ShopID: shops[0].ID, Title: "未开始", PayValue: 9900, ActualValue: 15000},
		{ShopID: shops[0].ID, Title: "已结束", PayValue: 9900, ActualValue: 15000},
		{ShopID: shops[0].ID, Title: "已售罄", PayValue: 9900, ActualValue: 15000},
		{ShopID: shops[1].ID, Title: "其他商户", PayValue: 3900, ActualValue: 5000},
	}
	if err := deps.DB.WithContext(ctx).Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	ids := make([]uint64, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := deps.DB.WithContext(clean).Where("voucher_id IN ?", ids).Delete(&model.SeckillVoucher{}).Error; err != nil {
			t.Error(err)
		}
		if err := deps.DB.WithContext(clean).Where("id IN ?", ids).Delete(&model.Voucher{}).Error; err != nil {
			t.Error(err)
		}
	})
	now := time.Now().UTC().Truncate(time.Millisecond)
	activities := []model.SeckillVoucher{
		{VoucherID: rows[1].ID, Stock: 10, BeginTime: now.Add(-time.Hour), EndTime: now.Add(time.Hour)},
		{VoucherID: rows[2].ID, Stock: 5, BeginTime: now.Add(time.Hour), EndTime: now.Add(2 * time.Hour)},
		{VoucherID: rows[3].ID, Stock: 3, BeginTime: now.Add(-2 * time.Hour), EndTime: now.Add(-time.Hour)},
		{VoucherID: rows[4].ID, Stock: 0, BeginTime: now.Add(-time.Hour), EndTime: now.Add(time.Hour)},
	}
	if err := deps.DB.WithContext(ctx).Create(&activities).Error; err != nil {
		t.Fatal(err)
	}
	repo := repository.NewVoucher(deps.DB)
	s := service.NewVoucher(repo)
	for i, status := range []string{"ordinary", "active", "upcoming", "ended", "sold_out", "ordinary"} {
		got, err := s.Detail(ctx, rows[i].ID)
		if err != nil || got.ID != rows[i].ID || got.ShopID != rows[i].ShopID || got.PayValue != rows[i].PayValue || got.ActualValue != rows[i].ActualValue {
			t.Fatalf("detail=%+v err=%v", got, err)
		}
		if status == "ordinary" {
			if got.Seckill != nil {
				t.Fatalf("ordinary has activity: %+v", got)
			}
		} else if got.Seckill == nil || got.Seckill.Status != status || got.Seckill.Stock != activities[i-1].Stock || !got.Seckill.BeginTime.Equal(activities[i-1].BeginTime) || !got.Seckill.EndTime.Equal(activities[i-1].EndTime) {
			t.Fatalf("activity %s=%+v", status, got.Seckill)
		}
	}
	var seen []uint64
	for page := 1; page <= 3; page++ {
		got, err := s.List(ctx, shops[0].ID, page, 2)
		if err != nil || got.HasMore != (page < 3) {
			t.Fatalf("page=%+v err=%v", got, err)
		}
		for _, item := range got.Items {
			if item.ShopID != shops[0].ID {
				t.Fatal("cross-shop result")
			}
			seen = append(seen, item.ID)
		}
	}
	if len(seen) != 5 {
		t.Fatalf("seen=%v", seen)
	}
	for i, id := range seen {
		if id != rows[i].ID {
			t.Fatalf("unstable ordering: %v", seen)
		}
	}
	for _, shopID := range []uint64{shops[2].ID, ^uint64(0)} {
		empty, err := s.List(ctx, shopID, 1, 10)
		if err != nil || empty.Items == nil || len(empty.Items) != 0 || empty.HasMore {
			t.Fatalf("empty=%+v err=%v", empty, err)
		}
	}
	all, err := s.List(ctx, 0, 1, 50)
	if err != nil || len(all.Items) < len(rows) {
		t.Fatalf("all=%+v err=%v", all, err)
	}
	if err := deps.DB.WithContext(ctx).Model(&model.SeckillVoucher{}).Where("voucher_id = ?", rows[1].ID).Update("stock", 0).Error; err != nil {
		t.Fatal(err)
	}
	got, err := s.Detail(ctx, rows[1].ID)
	if err != nil || got.Seckill == nil || got.Seckill.Stock != 0 || got.Seckill.Status != "sold_out" {
		t.Fatalf("fresh stock=%+v err=%v", got, err)
	}
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler.NewVoucher(s).Register(h)
	for _, path := range []string{fmt.Sprintf("/api/v1/vouchers?shop_id=%d", shops[0].ID), fmt.Sprintf("/api/v1/vouchers/%d", rows[0].ID), fmt.Sprintf("/api/v1/vouchers/%d", rows[1].ID)} {
		res := ut.PerformRequest(h.Engine, "GET", path, nil)
		var body struct {
			Code      string
			RequestID string `json:"request_id"`
			Data      json.RawMessage
		}
		if res.Code != 200 || res.Header().Get("Cache-Control") != "no-store" || json.Unmarshal(res.Body.Bytes(), &body) != nil || body.Code != "ok" || body.RequestID == "" {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body)
		}
	}
	missing := ut.PerformRequest(h.Engine, "GET", "/api/v1/vouchers/18446744073709551615", nil)
	if missing.Code != 404 {
		t.Fatalf("missing=%d %s", missing.Code, missing.Body)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = repo.ByID(canceled, rows[0].ID)
	status, _, _ := apperror.Describe(err)
	if status != 408 {
		t.Fatalf("canceled query=%v", err)
	}
}

func TestVoucherDatabaseFailure(t *testing.T) {
	deps, ctx := authDependencies(t)
	if err := deps.SQL.Close(); err != nil {
		t.Fatal(err)
	}
	repo := repository.NewVoucher(deps.DB)
	_, detailErr := repo.ByID(ctx, 1)
	_, listErr := repo.List(ctx, 0, 0, 10)
	for _, err := range []error{detailErr, listErr} {
		status, code, message := apperror.Describe(err)
		if status != 503 || code != "dependency_failure" || message != "dependency unavailable" {
			t.Fatalf("unexpected failure mapping: %d %s %s %v", status, code, message, err)
		}
	}
}
