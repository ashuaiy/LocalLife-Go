package handler

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/requestid"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

type shopHTTPStub struct {
	t          *testing.T
	calls      int
	id, typeID uint64
	page, size int
	err        error
}

func (s *shopHTTPStub) check(ctx context.Context) {
	s.t.Helper()
	s.calls++
	if _, ok := ctx.Deadline(); !ok {
		s.t.Error("missing deadline")
	}
	if requestid.FromContext(ctx) == "" {
		s.t.Error("missing request ID")
	}
}
func (s *shopHTTPStub) Types(ctx context.Context) ([]service.ShopTypeView, error) {
	s.check(ctx)
	return []service.ShopTypeView{{ID: 3, Name: "餐饮"}}, s.err
}
func (s *shopHTTPStub) List(ctx context.Context, typeID uint64, page, size int) (service.ShopPage, error) {
	s.check(ctx)
	s.typeID, s.page, s.size = typeID, page, size
	return service.ShopPage{Items: []service.ShopView{}, Page: page, PageSize: size}, s.err
}
func (s *shopHTTPStub) Detail(ctx context.Context, id uint64) (service.ShopView, error) {
	s.check(ctx)
	s.id = id
	return service.ShopView{ID: id, TypeID: 3, Name: "测试商户"}, s.err
}

func TestShopHTTPContract(t *testing.T) {
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	stub := &shopHTTPStub{t: t}
	NewShop(stub).Register(h)
	for _, path := range []string{"/api/v1/shop-types", "/api/v1/shops", "/api/v1/shops?type_id=3&page=2&page_size=5", "/api/v1/shops/18446744073709551615"} {
		res := ut.PerformRequest(h.Engine, "GET", path, nil)
		if res.Code != 200 || !json.Valid(res.Body.Bytes()) {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body)
		}
		if path == "/api/v1/shops" && (stub.page != 1 || stub.size != 10 || stub.typeID != 0) {
			t.Fatalf("defaults=%+v", stub)
		}
		if path == "/api/v1/shops?type_id=3&page=2&page_size=5" && (stub.page != 2 || stub.size != 5 || stub.typeID != 3) {
			t.Fatalf("filter=%+v", stub)
		}
		if path == "/api/v1/shops/18446744073709551615" {
			var body struct {
				Data struct {
					ID     string `json:"id"`
					TypeID string `json:"type_id"`
				}
				RequestID string `json:"request_id"`
			}
			if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil || body.Data.ID != "18446744073709551615" || body.Data.TypeID != "3" || body.RequestID == "" {
				t.Fatalf("ID must retain precision: %s err=%v", res.Body, err)
			}
		}
	}
	calls := stub.calls
	for _, path := range []string{
		"/api/v1/shops/0", "/api/v1/shops/-1", "/api/v1/shops/abc", "/api/v1/shops/18446744073709551616",
		"/api/v1/shops?page=0", "/api/v1/shops?page=1001", "/api/v1/shops?page=999999999999999999999999",
		"/api/v1/shops?page_size=51", "/api/v1/shops?page_size=-1", "/api/v1/shops?page=1.5",
		"/api/v1/shops?type_id=0", "/api/v1/shops?type_id=abc", "/api/v1/shops?type_id=18446744073709551616",
		"/api/v1/shops?page=", "/api/v1/shops?type_id=", "/api/v1/shops?page=1&page=2",
	} {
		res := ut.PerformRequest(h.Engine, "GET", path, nil)
		if res.Code != 400 {
			t.Fatalf("%s: %d %s", path, res.Code, res.Body)
		}
	}
	if stub.calls != calls {
		t.Fatal("invalid inputs reached service")
	}
	for _, tc := range []struct {
		err    error
		status int
	}{
		{apperror.New(apperror.NotFound, nil), 404},
		{apperror.New(apperror.Dependency, nil), 503},
		{context.DeadlineExceeded, 504},
	} {
		stub.err = tc.err
		for _, path := range []string{"/api/v1/shops/7", "/api/v1/shops", "/api/v1/shop-types"} {
			res := ut.PerformRequest(h.Engine, "GET", path, nil)
			if res.Code != tc.status {
				t.Fatalf("error %s: %d %s", path, res.Code, res.Body)
			}
		}
	}
}
