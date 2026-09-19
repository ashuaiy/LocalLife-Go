package service

import (
	"context"
	"testing"

	"github.com/ashuaiy/local-life-go/internal/model"
)

type followRepoStub struct {
	actor, target uint64
	following     bool
	writes        int
	rows          []model.User
	offset, limit int
}

func (r *followRepoStub) Set(_ context.Context, user, target uint64, following bool) error {
	r.actor, r.target, r.following = user, target, following
	r.writes++
	return nil
}
func (r *followRepoStub) State(context.Context, uint64, uint64) (bool, error) {
	return r.following, nil
}
func (r *followRepoStub) Following(_ context.Context, _ uint64, offset, limit int) ([]model.User, error) {
	r.offset, r.limit = offset, limit
	return r.rows, nil
}

type followUsersStub struct {
	exists bool
	err    error
}

func (u followUsersStub) Exists(context.Context, uint64) (bool, error) { return u.exists, u.err }
func TestFollowIdentityAndPagination(t *testing.T) {
	repo := &followRepoStub{rows: []model.User{{ID: 2, Phone: "private", Nickname: "a"}, {ID: 3}, {ID: 4}}}
	f := NewFollow(repo, followUsersStub{exists: true})
	for _, desired := range []bool{true, true, false, false} {
		result, err := f.Set(context.Background(), 1, 2, desired)
		if err != nil || result.Following != desired || repo.actor != 1 || repo.target != 2 {
			t.Fatalf("follow=%+v err=%v", result, err)
		}
		state, err := f.State(context.Background(), 1, 2)
		if err != nil || state.Following != desired {
			t.Fatalf("state=%+v err=%v", state, err)
		}
	}
	writes := repo.writes
	_, err := f.Set(context.Background(), 1, 1, true)
	expectStatus(t, err, 400)
	_, err = f.Set(context.Background(), 0, 2, true)
	expectStatus(t, err, 401)
	_, err = f.Set(context.Background(), 1, 0, true)
	expectStatus(t, err, 400)
	_, err = NewFollow(repo, followUsersStub{}).Set(context.Background(), 1, 99, true)
	expectStatus(t, err, 404)
	if writes != repo.writes {
		t.Fatal("invalid follow wrote to DB")
	}
	page, err := f.List(context.Background(), 1, 2, 2)
	if err != nil || len(page.Items) != 2 || !page.HasMore || page.Items[0].ID != 2 || repo.offset != 2 || repo.limit != 3 {
		t.Fatalf("page=%+v err=%v", page, err)
	}
	repo.rows = nil
	page, err = f.List(context.Background(), 1, 1, 10)
	if err != nil || page.Items == nil || page.HasMore {
		t.Fatalf("empty=%+v err=%v", page, err)
	}
	_, err = f.List(context.Background(), 0, 1, 10)
	expectStatus(t, err, 401)
	_, err = f.List(context.Background(), 1, 1001, 10)
	expectStatus(t, err, 400)
}
