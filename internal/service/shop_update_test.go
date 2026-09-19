package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type shopEditRepoStub struct {
	edit    model.ShopTextEdit
	updated bool
	err     error
}

func (r *shopEditRepoStub) UpdateText(_ context.Context, id uint64, edit model.ShopTextEdit) (model.Shop, error) {
	r.edit = edit
	if r.err != nil {
		return model.Shop{}, r.err
	}
	r.updated = true
	return model.Shop{ID: id, TypeID: 3, Name: *edit.Name}, nil
}

type shopInvalidatorStub struct {
	repo   *shopEditRepoStub
	called bool
	err    error
}

func (s *shopInvalidatorStub) Invalidate(context.Context, uint64) error {
	if !s.repo.updated {
		panic("invalidated before database commit")
	}
	s.called = true
	return s.err
}
func TestShopUpdateInvalidatesAfterDatabase(t *testing.T) {
	r := &shopEditRepoStub{}
	store := &shopInvalidatorStub{repo: r}
	s := NewShopMaintenance(r, store)
	name, address := "  新名称  ", ""
	got, err := s.Update(context.Background(), 7, model.ShopTextEdit{Name: &name, Address: &address})
	if err != nil || got.Name != "新名称" || !store.called || r.edit.Address == nil || *r.edit.Address != "" {
		t.Fatalf("update=%+v err=%v", got, err)
	}
	store.called = false
	r.err = apperror.New(apperror.NotFound, nil)
	_, err = s.Update(context.Background(), 7, model.ShopTextEdit{Name: &name})
	expectStatus(t, err, 404)
	if store.called {
		t.Fatal("invalidated on failed database update")
	}
	r.err = nil
	store.err = errors.New("redis down")
	got, err = s.Update(context.Background(), 7, model.ShopTextEdit{Name: &name})
	expectStatus(t, err, 503)
	if got.ID != 7 {
		t.Fatal("caller cannot identify committed update")
	}
}
func TestShopUpdateValidation(t *testing.T) {
	r := &shopEditRepoStub{}
	s := NewShopMaintenance(r, &shopInvalidatorStub{repo: r})
	for _, value := range []string{" ", strings.Repeat("字", 129), string([]byte{0xff})} {
		_, err := s.Update(context.Background(), 7, model.ShopTextEdit{Name: &value})
		expectStatus(t, err, 400)
	}
	address := strings.Repeat("字", 513)
	_, err := s.Update(context.Background(), 7, model.ShopTextEdit{Address: &address})
	expectStatus(t, err, 400)
	_, err = s.Update(context.Background(), 7, model.ShopTextEdit{})
	expectStatus(t, err, 400)
	name := "ok"
	_, err = s.Update(context.Background(), 0, model.ShopTextEdit{Name: &name})
	expectStatus(t, err, 400)
	if r.updated {
		t.Fatal("invalid edit reached database")
	}
}

type blockingShopInvalidator struct{ budget time.Duration }

func (s *blockingShopInvalidator) Invalidate(ctx context.Context, _ uint64) error {
	deadline, ok := ctx.Deadline()
	if !ok {
		return errors.New("missing invalidation deadline")
	}
	s.budget = time.Until(deadline)
	<-ctx.Done()
	return ctx.Err()
}

func TestShopUpdateInvalidationTimeoutPreservesCommittedResult(t *testing.T) {
	r := &shopEditRepoStub{}
	store := &blockingShopInvalidator{}
	s := NewShopMaintenance(r, store)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	name := "new"
	got, err := s.Update(ctx, 7, model.ShopTextEdit{Name: &name})
	if got.ID != 7 || !r.updated || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("committed result=%+v err=%v", got, err)
	}
	if store.budget <= 0 || store.budget > 100*time.Millisecond {
		t.Fatalf("invalidation budget=%s, want at most 100ms", store.budget)
	}
}
