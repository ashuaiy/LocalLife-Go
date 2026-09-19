package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

// Business orchestration is isolated here. Real rollback, locks and constraints are verified against MySQL.
type orderTxStub struct {
	activity                                                    model.SeckillVoucher
	now                                                         time.Time
	existing                                                    bool
	decremented                                                 bool
	stockCalls, createCalls                                     int
	created                                                     model.VoucherOrder
	activityErr, clockErr, existingErr, decrementErr, createErr error
	locked                                                      bool
}

func (x *orderTxStub) LockActivity(context.Context, uint64) (model.SeckillVoucher, error) {
	x.locked = true
	return x.activity, x.activityErr
}
func (x *orderTxStub) Now(context.Context) (time.Time, error) {
	if !x.locked {
		return time.Time{}, errors.New("clock read before lock")
	}
	return x.now, x.clockErr
}
func (x *orderTxStub) HasOrder(context.Context, uint64, uint64) (bool, error) {
	return x.existing, x.existingErr
}
func (x *orderTxStub) DecrementStock(context.Context, uint64) (bool, error) {
	x.stockCalls++
	return x.decremented, x.decrementErr
}
func (x *orderTxStub) CreateOrder(_ context.Context, row model.VoucherOrder) (model.VoucherOrder, error) {
	x.createCalls++
	x.created = row
	row.ID = 99
	return row, x.createErr
}

type orderRepoStub struct {
	tx                                 *orderTxStub
	err                                error
	rows                               []model.VoucherOrder
	user, id, voucher                  uint64
	offset, limit, transactions, reads int
}

func (r *orderRepoStub) WithinTransaction(ctx context.Context, fn func(OrderTransaction) error) error {
	r.transactions++
	if err := fn(r.tx); err != nil {
		return err
	}
	return r.err
}
func (r *orderRepoStub) ByID(_ context.Context, user, id uint64) (model.VoucherOrder, error) {
	r.reads++
	r.user, r.id = user, id
	if r.err != nil {
		return model.VoucherOrder{}, r.err
	}
	return r.rows[0], nil
}
func (r *orderRepoStub) List(_ context.Context, user, voucher uint64, offset, limit int) ([]model.VoucherOrder, error) {
	r.reads++
	r.user, r.voucher, r.offset, r.limit = user, voucher, offset, limit
	return r.rows, r.err
}
func validOrderTx() *orderTxStub {
	begin := time.Date(2026, 9, 17, 0, 0, 0, 0, time.UTC)
	return &orderTxStub{activity: model.SeckillVoucher{VoucherID: 3, Stock: 2, BeginTime: begin, EndTime: begin.Add(time.Hour)}, now: begin, decremented: true}
}
func TestOrderActivityAndStockDecisions(t *testing.T) {
	for _, tc := range []struct {
		name    string
		change  func(*orderTxStub)
		code    string
		creates int
	}{
		{"at start", func(x *orderTxStub) {}, "", 1},
		{"before end", func(x *orderTxStub) { x.now = x.activity.EndTime.Add(-time.Millisecond) }, "", 1},
		{"before start", func(x *orderTxStub) { x.now = x.activity.BeginTime.Add(-time.Millisecond) }, "activity_not_started", 0},
		{"at end", func(x *orderTxStub) { x.now = x.activity.EndTime }, "activity_ended", 0},
		{"after end", func(x *orderTxStub) { x.now = x.activity.EndTime.Add(time.Millisecond) }, "activity_ended", 0},
		{"duplicate", func(x *orderTxStub) { x.existing = true }, "already_purchased", 0},
		{"empty", func(x *orderTxStub) { x.activity.Stock = 0; x.decremented = false }, "sold_out", 0},
		{"conditional update lost", func(x *orderTxStub) { x.decremented = false }, "sold_out", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			x := validOrderTx()
			tc.change(x)
			r := &orderRepoStub{tx: x}
			got, err := NewOrder(r).Place(context.Background(), 7, 3)
			if tc.code == "" {
				if err != nil || got.ID != 99 || got.UserID != 7 || got.VoucherID != 3 || got.Status != 1 || !got.CreatedAt.Equal(x.now) || x.stockCalls != 1 {
					t.Fatalf("order=%+v err=%v", got, err)
				}
			} else {
				_, code, _ := apperror.Describe(err)
				if code != tc.code {
					t.Fatalf("code=%s err=%v", code, err)
				}
			}
			if x.createCalls != tc.creates {
				t.Fatalf("creates=%d", x.createCalls)
			}
		})
	}
}
func TestOrderFailuresPropagateWithoutSuccess(t *testing.T) {
	want := apperror.New(apperror.Dependency, errors.New("private cause"))
	for _, phase := range []string{"activity", "clock", "duplicate", "stock", "create", "commit"} {
		t.Run(phase, func(t *testing.T) {
			x := validOrderTx()
			r := &orderRepoStub{tx: x}
			switch phase {
			case "activity":
				x.activityErr = want
			case "clock":
				x.clockErr = want
			case "duplicate":
				x.existingErr = want
			case "stock":
				x.decrementErr = want
			case "create":
				x.createErr = want
			case "commit":
				r.err = want
			}
			got, err := NewOrder(r).Place(context.Background(), 7, 3)
			if !errors.Is(err, want) || got.ID != 0 {
				t.Fatalf("order=%+v err=%v", got, err)
			}
		})
	}
}
func TestOrderIdentityPaginationAndValidation(t *testing.T) {
	r := &orderRepoStub{tx: validOrderTx(), rows: []model.VoucherOrder{{ID: 1, UserID: 7, VoucherID: 3, Status: 1}, {ID: 2}, {ID: 3}}}
	s := NewOrder(r)
	item, err := s.Detail(context.Background(), 7, 1)
	if err != nil || item.ID != 1 || r.user != 7 || r.id != 1 {
		t.Fatalf("detail=%+v err=%v", item, err)
	}
	page, err := s.List(context.Background(), 7, 3, 2, 2)
	if err != nil || len(page.Items) != 2 || !page.HasMore || page.Page != 2 || page.PageSize != 2 || r.user != 7 || r.voucher != 3 || r.offset != 2 || r.limit != 3 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	r.rows = nil
	page, err = s.List(context.Background(), 7, 0, 1, 10)
	if err != nil || page.Items == nil || len(page.Items) != 0 || page.HasMore {
		t.Fatalf("empty=%+v err=%v", page, err)
	}
	for _, action := range []func() error{
		func() error { _, err := s.Place(context.Background(), 0, 3); return err },
		func() error { _, err := s.Detail(context.Background(), 0, 1); return err },
		func() error { _, err := s.List(context.Background(), 0, 0, 1, 10); return err },
	} {
		expectStatus(t, action(), 401)
	}
	_, err = s.Place(context.Background(), 7, 0)
	expectStatus(t, err, 400)
	_, err = s.Detail(context.Background(), 7, 0)
	expectStatus(t, err, 400)
	for _, pair := range [][2]int{{0, 10}, {1001, 10}, {1, 0}, {1, 51}} {
		_, err = s.List(context.Background(), 7, 0, pair[0], pair[1])
		expectStatus(t, err, 400)
	}
	if r.transactions != 0 {
		t.Fatal("invalid purchase reached transaction")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = s.Place(ctx, 7, 3)
	expectStatus(t, err, 408)
}
