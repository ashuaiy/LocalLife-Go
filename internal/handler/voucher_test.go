package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/requestid"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

type voucherHTTPRepo struct {
	t             *testing.T
	calls         int
	shopID        uint64
	offset, limit int
	err           error
}

func (r *voucherHTTPRepo) check(ctx context.Context) {
	r.t.Helper()
	r.calls++
	if _, ok := ctx.Deadline(); !ok {
		r.t.Error("missing deadline")
	}
	if requestid.FromContext(ctx) == "" {
		r.t.Error("missing request ID")
	}
}
func (r *voucherHTTPRepo) ByID(ctx context.Context, id uint64) (model.VoucherWithActivity, error) {
	r.check(ctx)
	return model.VoucherWithActivity{Voucher: model.Voucher{ID: id, ShopID: 3, Title: "普通券", PayValue: 9900, ActualValue: 15000}}, r.err
}
func (r *voucherHTTPRepo) List(ctx context.Context, shopID uint64, offset, limit int) ([]model.VoucherWithActivity, error) {
	r.check(ctx)
	r.shopID, r.offset, r.limit = shopID, offset, limit
	return nil, r.err
}

func TestVoucherHTTPContract(t *testing.T) {
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	repo := &voucherHTTPRepo{t: t}
	NewVoucher(service.NewVoucher(repo)).Register(h)
	for _, path := range []string{"/api/v1/vouchers", "/api/v1/vouchers?shop_id=3&page=2&page_size=5", "/api/v1/vouchers/18446744073709551615"} {
		res := ut.PerformRequest(h.Engine, "GET", path, nil)
		if res.Code != 200 || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body)
		}
		var body struct {
			Code      string
			RequestID string `json:"request_id"`
			Data      json.RawMessage
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Code != "ok" || body.RequestID == "" {
			t.Fatalf("envelope=%s err=%v", res.Body, err)
		}
		if path == "/api/v1/vouchers" {
			if repo.shopID != 0 || repo.offset != 0 || repo.limit != 11 {
				t.Fatalf("defaults=%+v", repo)
			}
			var page struct {
				Items   []any
				HasMore bool `json:"has_more"`
			}
			if err := json.Unmarshal(body.Data, &page); err != nil || page.Items == nil || len(page.Items) != 0 || page.HasMore {
				t.Fatalf("empty=%s err=%v", body.Data, err)
			}
		}
		if path == "/api/v1/vouchers?shop_id=3&page=2&page_size=5" && (repo.shopID != 3 || repo.offset != 5 || repo.limit != 6) {
			t.Fatalf("filter=%+v", repo)
		}
		if path == "/api/v1/vouchers/18446744073709551615" {
			var item struct {
				ID       string
				PayValue string `json:"pay_value"`
			}
			if err := json.Unmarshal(body.Data, &item); err != nil || item.ID != "18446744073709551615" || item.PayValue != "9900" {
				t.Fatalf("precision=%s err=%v", body.Data, err)
			}
		}
	}
	calls := repo.calls
	for _, path := range []string{
		"/api/v1/vouchers/0", "/api/v1/vouchers/-1", "/api/v1/vouchers/abc", "/api/v1/vouchers/18446744073709551616",
		"/api/v1/vouchers?shop_id=0", "/api/v1/vouchers?shop_id=", "/api/v1/vouchers?shop_id=1&shop_id=2", "/api/v1/vouchers?shop_id=18446744073709551616",
		"/api/v1/vouchers?page=0", "/api/v1/vouchers?page=1001", "/api/v1/vouchers?page=1.5", "/api/v1/vouchers?page=", "/api/v1/vouchers?page=1&page=2",
		"/api/v1/vouchers?page_size=0", "/api/v1/vouchers?page_size=51", "/api/v1/vouchers?page_size=1&page_size=2", "/api/v1/vouchers?shop_id=%zz",
	} {
		res := ut.PerformRequest(h.Engine, "GET", path, nil)
		if res.Code != 400 {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body)
		}
	}
	if repo.calls != calls {
		t.Fatal("invalid query reached repository")
	}
	for _, tc := range []struct {
		err    error
		status int
	}{
		{apperror.New(apperror.NotFound, nil), 404},
		{apperror.New(apperror.Dependency, nil), 503},
		{context.Canceled, 408},
		{context.DeadlineExceeded, 504},
	} {
		repo.err = tc.err
		for _, path := range []string{"/api/v1/vouchers", "/api/v1/vouchers/7"} {
			res := ut.PerformRequest(h.Engine, "GET", path, nil)
			if res.Code != tc.status {
				t.Fatalf("error %s: %d %s", path, res.Code, res.Body)
			}
		}
	}
}
