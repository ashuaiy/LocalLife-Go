package service

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

func (s *Shop) loadCache(ctx context.Context, id uint64) (model.ShopSnapshot, error) {
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	return s.store.Load(bounded, id)
}
func (s *Shop) publish(ctx context.Context, id uint64, snapshot model.ShopSnapshot, entry model.ShopCacheEntry) {
	bounded, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	_, _ = s.store.CompareAndSet(bounded, id, snapshot.Token, entry, time.Until(entry.ExpiresAt))
}

// refresh is a bounded singleflight group. One owned goroutine per distinct ID
// serves both cold waiters and stale readers; callers never own its cancellation.
func (s *Shop) refresh(id uint64) (*shopRefresh, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, apperror.New(apperror.Dependency, nil)
	}
	if call := s.inflight[id]; call != nil {
		return call, nil
	}
	if len(s.inflight) >= s.maxRefresh {
		return nil, apperror.New(apperror.RateLimited, nil)
	}
	call := &shopRefresh{done: make(chan struct{})}
	s.inflight[id] = call
	go s.rebuild(id, call)
	return call, nil
}
func (s *Shop) rebuild(id uint64, call *shopRefresh) {
	defer func() {
		if recover() != nil {
			call.err = apperror.New(apperror.Internal, nil)
		}
		s.mu.Lock()
		defer s.mu.Unlock()
		delete(s.inflight, id)
		close(call.done)
		if s.closed && len(s.inflight) == 0 {
			close(s.drained)
		}
	}()
	ctx, cancel := context.WithTimeout(s.background, 2*time.Second)
	defer cancel()
	snapshot, cacheErr := s.loadCache(ctx, id)
	now := time.Now()
	// Another completed fill may have appeared between the request's miss and this job.
	if cacheErr == nil && snapshot.Entry != nil && now.Before(snapshot.Entry.ExpiresAt) && now.Before(snapshot.Entry.RefreshAfter) {
		if snapshot.Entry.Value == nil {
			call.err = apperror.New(apperror.NotFound, nil)
		} else {
			call.row = *snapshot.Entry.Value
		}
		return
	}
	call.row, call.err = s.shops.ByID(ctx, id)
	if ctx.Err() != nil {
		call.err = ctx.Err()
	}
	if s.background.Err() != nil {
		return
	}
	now = time.Now()
	if call.err == nil {
		fresh := now.Add(30*time.Minute + time.Duration(rand.Int64N(int64(5*time.Minute))))
		s.publish(ctx, id, snapshot, model.ShopCacheEntry{ID: id, Value: &call.row, RefreshAfter: fresh, ExpiresAt: fresh.Add(5 * time.Minute)})
		return
	}
	var classified *apperror.Error
	if errors.As(call.err, &classified) && classified.Kind == apperror.NotFound {
		expires := now.Add(30*time.Second + time.Duration(rand.Int64N(int64(15*time.Second))))
		s.publish(ctx, id, snapshot, model.ShopCacheEntry{ID: id, RefreshAfter: expires, ExpiresAt: expires})
		return
	}
	// A failed refresh can postpone a retry briefly, but cannot extend the stale window.
	if cacheErr == nil && snapshot.Entry != nil && snapshot.Entry.Value != nil && now.Before(snapshot.Entry.ExpiresAt) {
		retry := *snapshot.Entry
		retry.RefreshAfter = now.Add(5 * time.Second)
		if retry.RefreshAfter.After(retry.ExpiresAt) {
			retry.RefreshAfter = retry.ExpiresAt
		}
		// The query budget may be exhausted. Allow one bounded cache write using
		// the service lifetime so a deadline still backs off, but shutdown cancels it.
		s.publish(s.background, id, snapshot, retry)
	}
}

// Close is called after HTTP draining and before dependency pools are closed.
func (s *Shop) Close(ctx context.Context) error {
	s.mu.Lock()
	if !s.closed {
		s.closed = true
		s.stop()
		if len(s.inflight) == 0 {
			close(s.drained)
		}
	}
	s.mu.Unlock()
	select {
	case <-s.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
