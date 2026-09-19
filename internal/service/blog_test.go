package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type blogRepoStub struct {
	row                    model.Blog
	rows                   []model.Blog
	err                    error
	writes                 int
	liked                  bool
	blogID, userID, shopID uint64
	offset, limit          int
}

func (r *blogRepoStub) Create(_ context.Context, row model.Blog) (model.Blog, error) {
	r.writes++
	row.ID = 9
	r.row = row
	return row, r.err
}
func (r *blogRepoStub) ByID(context.Context, uint64) (model.Blog, error) { return r.row, r.err }
func (r *blogRepoStub) List(_ context.Context, shopID, userID uint64, offset, limit int) ([]model.Blog, error) {
	r.shopID, r.userID, r.offset, r.limit = shopID, userID, offset, limit
	return r.rows, r.err
}
func (r *blogRepoStub) SetLike(_ context.Context, blogID, userID uint64, liked bool) (bool, error) {
	r.writes++
	changed := r.liked != liked
	r.blogID, r.userID, r.liked = blogID, userID, liked
	return changed, r.err
}
func (r *blogRepoStub) IsLiked(_ context.Context, blogID, userID uint64) (bool, error) {
	r.blogID, r.userID = blogID, userID
	return r.liked, r.err
}

func TestBlogPublishValidationAndAuthor(t *testing.T) {
	repo := &blogRepoStub{}
	shops := &shopRepoStub{rows: []model.Shop{{ID: 3}}}
	b := NewBlog(repo, shops, nil)
	input := BlogInput{ShopID: 3, Title: "  探店记录  ", Content: "\n内容\n第二行\n"}
	got, err := b.Publish(context.Background(), 7, input)
	if err != nil || got.ID != 9 || got.UserID != 7 || got.ShopID != 3 || got.Title != "探店记录" || got.Content != "内容\n第二行" {
		t.Fatalf("published=%+v err=%v", got, err)
	}
	for _, bad := range []BlogInput{
		{ShopID: 0, Title: "title", Content: "text"}, {ShopID: 3, Title: "  ", Content: "text"}, {ShopID: 3, Title: "title", Content: "\n\t"},
		{ShopID: 3, Title: strings.Repeat("中", 256), Content: "text"}, {ShopID: 3, Title: "title", Content: strings.Repeat("文", 10001)},
		{ShopID: 3, Title: string([]byte{0xff}), Content: "text"},
	} {
		_, err := b.Publish(context.Background(), 7, bad)
		expectStatus(t, err, 400)
	}
	if repo.writes != 1 {
		t.Fatal("invalid publication persisted")
	}
	_, err = b.Publish(context.Background(), 0, input)
	expectStatus(t, err, 401)
	shops.err = apperror.New(apperror.NotFound, nil)
	_, err = b.Publish(context.Background(), 7, input)
	expectStatus(t, err, 404)
	if repo.writes != 1 {
		t.Fatal("missing shop publication persisted")
	}
	shops.err = nil
	_, err = b.Publish(context.Background(), 7, BlogInput{ShopID: 3, Title: strings.Repeat("中", 255), Content: strings.Repeat("文", 10000)})
	if err != nil {
		t.Fatalf("valid Unicode limits: %v", err)
	}
}

func TestBlogReadsPaginationAndLikeIdentity(t *testing.T) {
	repo := &blogRepoStub{row: model.Blog{ID: 9, UserID: 7, ShopID: 3, LikeCount: 2}, rows: []model.Blog{{ID: 8}, {ID: 7}, {ID: 6}}}
	b := NewBlog(repo, &shopRepoStub{}, nil)
	got, err := b.Detail(context.Background(), 9)
	if err != nil || got.ID != 9 || got.LikeCount != 2 {
		t.Fatalf("detail=%+v err=%v", got, err)
	}
	page, err := b.List(context.Background(), 3, 7, 2, 2)
	if err != nil || len(page.Items) != 2 || !page.HasMore || page.Items[0].ID != 8 || repo.offset != 2 || repo.limit != 3 || repo.shopID != 3 || repo.userID != 7 {
		t.Fatalf("page=%+v repo=%+v err=%v", page, repo, err)
	}
	repo.rows = nil
	page, err = b.List(context.Background(), 0, 0, 1, 10)
	if err != nil || page.Items == nil || len(page.Items) != 0 {
		t.Fatalf("empty=%+v err=%v", page, err)
	}
	for _, v := range [][2]int{{0, 10}, {1001, 10}, {1, 0}, {1, 51}} {
		_, err := b.List(context.Background(), 0, 0, v[0], v[1])
		expectStatus(t, err, 400)
	}
	for _, liked := range []bool{true, true, false, false} {
		result, err := b.Like(context.Background(), 9, 7, liked)
		if err != nil || result.Liked != liked || repo.blogID != 9 || repo.userID != 7 {
			t.Fatalf("like=%+v err=%v", result, err)
		}
		state, err := b.LikeState(context.Background(), 9, 7)
		if err != nil || state.Liked != liked {
			t.Fatalf("like state=%+v err=%v", state, err)
		}
	}
	_, err = b.Like(context.Background(), 9, 0, true)
	expectStatus(t, err, 401)
	_, err = b.LikeState(context.Background(), 9, 0)
	expectStatus(t, err, 401)
	_, err = b.Detail(context.Background(), 0)
	expectStatus(t, err, 400)
	_, err = b.Like(context.Background(), 0, 7, true)
	expectStatus(t, err, 400)
	repo.err = apperror.New(apperror.NotFound, nil)
	writes := repo.writes
	_, err = b.Like(context.Background(), 99, 7, true)
	expectStatus(t, err, 404)
	if repo.writes != writes {
		t.Fatal("missing blog accepted like")
	}
	_, err = b.LikeState(context.Background(), 99, 7)
	expectStatus(t, err, 404)
	repo.err = apperror.New(apperror.Dependency, nil)
	_, err = b.List(context.Background(), 0, 0, 1, 10)
	expectStatus(t, err, 503)
}

type blogPublisherStub struct {
	row model.Blog
	err error
}

func (p *blogPublisherStub) Push(_ context.Context, row model.Blog) error { p.row = row; return p.err }
func TestBlogPublishesPersistedRecordToFeed(t *testing.T) {
	repo := &blogRepoStub{}
	publisher := &blogPublisherStub{}
	b := NewBlog(repo, &shopRepoStub{rows: []model.Shop{{ID: 3}}}, publisher)
	_, err := b.Publish(context.Background(), 7, BlogInput{ShopID: 3, Title: "title", Content: "text"})
	if err != nil || publisher.row.ID != 9 || publisher.row.UserID != 7 {
		t.Fatalf("fanout=%+v err=%v", publisher.row, err)
	}
	publisher.err = errors.New("redis down")
	_, err = b.Publish(context.Background(), 7, BlogInput{ShopID: 3, Title: "title", Content: "text"})
	expectStatus(t, err, 503)
	if repo.writes != 2 {
		t.Fatal("feed failure must not pretend the persisted blog was rolled back")
	}
}
