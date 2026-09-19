package handler

import (
	"context"
	"encoding/json"
	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type orderHTTPStub struct {
	user, id, voucher uint64
	page, size, calls int
	err               error
}

func (s *orderHTTPStub) Place(_ context.Context, user, voucher uint64) (service.OrderView, error) {
	s.calls++
	s.user, s.voucher = user, voucher
	return service.OrderView{ID: ^uint64(0), UserID: user, VoucherID: voucher, Status: 1}, s.err
}
func (s *orderHTTPStub) Detail(_ context.Context, user, id uint64) (service.OrderView, error) {
	s.calls++
	s.user, s.id = user, id
	return service.OrderView{ID: id, UserID: user, VoucherID: 3, Status: 1}, s.err
}
func (s *orderHTTPStub) List(_ context.Context, user, voucher uint64, page, size int) (service.OrderPage, error) {
	s.calls++
	s.user, s.voucher, s.page, s.size = user, voucher, page, size
	return service.OrderPage{Items: []service.OrderView{}, Page: page, PageSize: size}, s.err
}
func TestOrderHTTPContract(t *testing.T) {
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	s := &orderHTTPStub{}
	NewOrder(s, &authStub{t: t}).Register(h)
	for _, tc := range []struct{ method, path string }{{"POST", "/api/v1/vouchers/3/seckill?user_id=999"}, {"GET", "/api/v1/orders?user_id=999"}, {"GET", "/api/v1/orders/18446744073709551615"}} {
		res := ut.PerformRequest(h.Engine, tc.method, tc.path, nil)
		if res.Code != 401 {
			t.Fatalf("unauthenticated %s=%d", tc.path, res.Code)
		}
		res = ut.PerformRequest(h.Engine, tc.method, tc.path, nil, ut.Header{Key: "Authorization", Value: "Bearer private-token"})
		if res.Code != 200 || s.user != 7 || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s=%d %s", tc.path, res.Code, res.Body)
		}
		if tc.method == "POST" {
			var body struct {
				Data struct{ ID, UserID, VoucherID string }
			}
			// ID field alone is enough to catch float/integer JSON precision loss.
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Data.ID != "18446744073709551615" {
				t.Fatalf("ID=%s err=%v", res.Body, err)
			}
		}
	}
	res := ut.PerformRequest(h.Engine, "GET", "/api/v1/orders?voucher_id=3&page=2&page_size=5", nil, ut.Header{Key: "Authorization", Value: "Bearer private-token"})
	if res.Code != 200 || s.voucher != 3 || s.page != 2 || s.size != 5 {
		t.Fatalf("query=%+v response=%s", s, res.Body)
	}
	calls := s.calls
	for _, tc := range []struct{ method, path, body string }{
		{"POST", "/api/v1/vouchers/0/seckill", ""}, {"POST", "/api/v1/vouchers/18446744073709551616/seckill", ""}, {"POST", "/api/v1/vouchers/3/seckill", `{"user_id":"999"}`},
		{"GET", "/api/v1/orders/0", ""}, {"GET", "/api/v1/orders/abc", ""}, {"GET", "/api/v1/orders?page=0", ""}, {"GET", "/api/v1/orders?page=1001", ""}, {"GET", "/api/v1/orders?page_size=51", ""}, {"GET", "/api/v1/orders?voucher_id=0", ""}, {"GET", "/api/v1/orders?voucher_id=", ""}, {"GET", "/api/v1/orders?page=1&page=2", ""},
	} {
		res := ut.PerformRequest(h.Engine, tc.method, tc.path, &ut.Body{Body: strings.NewReader(tc.body), Len: len(tc.body)}, ut.Header{Key: "Authorization", Value: "Bearer private-token"})
		if res.Code != 400 {
			t.Fatalf("%s=%d %s", tc.path, res.Code, res.Body)
		}
	}
	if s.calls != calls {
		t.Fatal("invalid input reached service")
	}
	for _, kind := range []apperror.Kind{apperror.ActivityNotStarted, apperror.ActivityEnded, apperror.SoldOut, apperror.AlreadyPurchased} {
		s.err = apperror.New(kind, nil)
		res := ut.PerformRequest(h.Engine, "POST", "/api/v1/vouchers/3/seckill", nil, ut.Header{Key: "Authorization", Value: "Bearer private-token"})
		var body struct{ Code string }
		if res.Code != 409 || json.Unmarshal(res.Body.Bytes(), &body) != nil || body.Code != string(kind) {
			t.Fatalf("conflict=%d %s", res.Code, res.Body)
		}
	}
}
