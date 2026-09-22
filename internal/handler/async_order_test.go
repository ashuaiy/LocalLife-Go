package handler

import (
	"context"
	"encoding/json"
	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/cloudwego/hertz/pkg/common/ut"
	"io"
	"log/slog"
	"strings"
	"testing"
)

type asyncHTTPStub struct {
	user, voucher uint64
	calls         int
}

func (s *asyncHTTPStub) Accept(_ context.Context, user, voucher uint64) (service.SeckillView, error) {
	s.calls++
	s.user = user
	s.voucher = voucher
	return service.SeckillView{VoucherID: voucher, Ticket: "123-0", Status: "pending"}, nil
}
func (s *asyncHTTPStub) Result(_ context.Context, user, voucher uint64) (service.SeckillView, error) {
	s.calls++
	s.user = user
	s.voucher = voucher
	id := ^uint64(0)
	return service.SeckillView{VoucherID: voucher, Ticket: "123-0", Status: "created", OrderID: &id}, nil
}
func TestAsyncOrderHTTPContract(t *testing.T) {
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	stub := &asyncHTTPStub{}
	NewAsyncOrder(stub, &authStub{t: t}).Register(h)
	auth := ut.Header{Key: "Authorization", Value: "Bearer private-token"}
	for _, tc := range []struct {
		method, path string
		status       int
	}{{"POST", "/api/v1/vouchers/3/seckill-async?user_id=999", 202}, {"GET", "/api/v1/vouchers/3/seckill-result?user_id=999", 200}} {
		if res := ut.PerformRequest(h.Engine, tc.method, tc.path, nil); res.Code != 401 {
			t.Fatalf("unauthenticated=%d", res.Code)
		}
		res := ut.PerformRequest(h.Engine, tc.method, tc.path, nil, auth)
		if res.Code != tc.status || stub.user != 7 || stub.voucher != 3 || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%d %s", res.Code, res.Body)
		}
		var body struct {
			Data      map[string]any
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Data["voucher_id"] != "3" || body.RequestID == "" {
			t.Fatalf("%s %v", res.Body, err)
		}
		if tc.method == "GET" && body.Data["order_id"] != "18446744073709551615" {
			t.Fatalf("lost order ID precision: %s", res.Body)
		}
		if tc.method == "POST" && body.Data["order_id"] != nil {
			t.Fatal("reservation is not an order")
		}
	}
	calls := stub.calls
	for _, tc := range []struct{ method, path, body string }{{"POST", "/api/v1/vouchers/0/seckill-async", ""}, {"POST", "/api/v1/vouchers/3/seckill-async", "{}"}, {"GET", "/api/v1/vouchers/abc/seckill-result", ""}} {
		res := ut.PerformRequest(h.Engine, tc.method, tc.path, &ut.Body{Body: strings.NewReader(tc.body), Len: len(tc.body)}, auth)
		if res.Code != 400 {
			t.Fatalf("invalid input=%d", res.Code)
		}
	}
	if stub.calls != calls {
		t.Fatal("invalid input reached service")
	}
}
