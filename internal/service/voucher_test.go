package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

// Only the persistence boundary is stubbed; joined SQL is exercised with MySQL in integration tests.
type voucherRepoStub struct {
	rows                 []model.VoucherWithActivity
	err                  error
	shopID               uint64
	offset, limit, calls int
}

func (r *voucherRepoStub) ByID(context.Context, uint64) (model.VoucherWithActivity, error) {
	r.calls++
	if r.err != nil {
		return model.VoucherWithActivity{}, r.err
	}
	return r.rows[0], nil
}
func (r *voucherRepoStub) List(_ context.Context, shopID uint64, offset, limit int) ([]model.VoucherWithActivity, error) {
	r.calls++
	r.shopID, r.offset, r.limit = shopID, offset, limit
	return r.rows, r.err
}

func voucherTestRow(stock int64, begin, end time.Time) model.VoucherWithActivity {
	id := uint64(7)
	return model.VoucherWithActivity{
		Voucher:    model.Voucher{ID: id, ShopID: 3, Title: "双人套餐", PayValue: 9900, ActualValue: 15000},
		ActivityID: &id, Stock: &stock, BeginTime: &begin, EndTime: &end,
	}
}

func TestVoucherActivityBoundaries(t *testing.T) {
	begin := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	end := begin.Add(time.Hour)
	for _, tc := range []struct {
		name   string
		now    time.Time
		stock  int64
		status string
	}{
		{"before start", begin.Add(-time.Millisecond), 2, "upcoming"},
		{"at start", begin, 2, "active"},
		{"before end", end.Add(-time.Millisecond), 2, "active"},
		{"at end", end, 2, "ended"},
		{"after end", end.Add(time.Millisecond), 2, "ended"},
		{"sold out", begin, 0, "sold_out"},
		{"future and empty", begin.Add(-time.Millisecond), 0, "upcoming"},
		{"ended and empty", end, 0, "ended"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &voucherRepoStub{rows: []model.VoucherWithActivity{voucherTestRow(tc.stock, begin, end)}}
			s := NewVoucher(repo)
			s.now = func() time.Time { return tc.now }
			got, err := s.Detail(context.Background(), 7)
			if err != nil || got.ID != 7 || got.Title != "双人套餐" || got.Seckill == nil {
				t.Fatalf("detail=%+v err=%v", got, err)
			}
			if got.Seckill.Status != tc.status || got.Seckill.Stock != tc.stock || !got.Seckill.BeginTime.Equal(begin) || !got.Seckill.EndTime.Equal(end) {
				t.Fatalf("activity=%+v want=%s", got.Seckill, tc.status)
			}
		})
	}
}

func TestVoucherOrdinaryAndMoneyPrecision(t *testing.T) {
	repo := &voucherRepoStub{rows: []model.VoucherWithActivity{{Voucher: model.Voucher{ID: ^uint64(0), ShopID: 3, Title: "普通券", PayValue: 9007199254740993, ActualValue: ^uint64(0)}}}}
	s := NewVoucher(repo)
	got, err := s.Detail(context.Background(), ^uint64(0))
	if err != nil || got.Seckill != nil {
		t.Fatalf("ordinary=%+v err=%v", got, err)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["id"] != "18446744073709551615" || decoded["shop_id"] != "3" || decoded["pay_value"] != "9007199254740993" || decoded["actual_value"] != "18446744073709551615" {
		t.Fatalf("precision lost: %s", raw)
	}
	if value, ok := decoded["seckill"]; !ok || value != nil {
		t.Fatalf("ordinary activity must be null: %s", raw)
	}
}

func TestVoucherPagination(t *testing.T) {
	begin := time.Date(2026, 9, 17, 10, 0, 0, 0, time.UTC)
	row := voucherTestRow(2, begin, begin.Add(time.Hour))
	repo := &voucherRepoStub{rows: []model.VoucherWithActivity{row, row, row}}
	s := NewVoucher(repo)
	clockCalls := 0
	s.now = func() time.Time { clockCalls++; return begin }
	page, err := s.List(context.Background(), 3, 2, 2)
	if err != nil || len(page.Items) != 2 || !page.HasMore || page.Page != 2 || page.PageSize != 2 || page.Items[0].Seckill.Status != "active" || clockCalls != 1 {
		t.Fatalf("page=%+v clocks=%d err=%v", page, clockCalls, err)
	}
	if repo.shopID != 3 || repo.offset != 2 || repo.limit != 3 {
		t.Fatalf("query=%+v", repo)
	}
	repo.rows = nil
	page, err = s.List(context.Background(), 0, 1, 10)
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("empty=%+v err=%v", page, err)
	}
}

func TestVoucherInvalidInputsAndFailures(t *testing.T) {
	repo := &voucherRepoStub{}
	s := NewVoucher(repo)
	for _, pair := range [][2]int{{0, 10}, {-1, 10}, {1001, 10}, {1, 0}, {1, 51}} {
		_, err := s.List(context.Background(), 0, pair[0], pair[1])
		expectStatus(t, err, 400)
	}
	_, err := s.Detail(context.Background(), 0)
	expectStatus(t, err, 400)
	if repo.calls != 0 {
		t.Fatal("invalid input reached database")
	}
	for _, kind := range []apperror.Kind{apperror.NotFound, apperror.Dependency} {
		repo.err = apperror.New(kind, nil)
		_, err := s.Detail(context.Background(), 7)
		want, _, _ := apperror.Describe(repo.err)
		expectStatus(t, err, want)
		_, err = s.List(context.Background(), 3, 1, 10)
		expectStatus(t, err, want)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Detail(ctx, 7)
	expectStatus(t, err, 408)
	_, err = s.List(ctx, 0, 1, 10)
	expectStatus(t, err, 408)
}

func TestVoucherRejectsIncompleteActivity(t *testing.T) {
	begin := time.Now().UTC()
	row := voucherTestRow(2, begin, begin.Add(time.Hour))
	row.Stock = nil
	s := NewVoucher(&voucherRepoStub{rows: []model.VoucherWithActivity{row}})
	_, err := s.Detail(context.Background(), 7)
	expectStatus(t, err, 503)
	_, err = s.List(context.Background(), 3, 1, 10)
	expectStatus(t, err, 503)
}
