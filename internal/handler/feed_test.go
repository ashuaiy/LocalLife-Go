package handler

import (
	"context"
	"io"
	"log/slog"
	"testing"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

type socialHTTPStub struct {
	user, target uint64
	state        bool
	page, size   int
	cursor       string
	err          error
}

func (s *socialHTTPStub) Set(_ context.Context, user, target uint64, state bool) (service.FollowResult, error) {
	s.user, s.target, s.state = user, target, state
	return service.FollowResult{Following: state}, s.err
}
func (s *socialHTTPStub) State(_ context.Context, user, target uint64) (service.FollowResult, error) {
	s.user, s.target = user, target
	return service.FollowResult{Following: s.state}, s.err
}
func (s *socialHTTPStub) List(_ context.Context, user uint64, page, size int) (service.FollowingPage, error) {
	s.user, s.page, s.size = user, page, size
	return service.FollowingPage{Items: []service.PublicUser{}, Page: page, PageSize: size}, s.err
}
func (s *socialHTTPStub) Read(_ context.Context, user uint64, cursor string, size int) (service.FeedPage, error) {
	s.user, s.cursor, s.size = user, cursor, size
	return service.FeedPage{Items: []service.BlogView{}}, s.err
}
func TestFollowFeedHTTPIdentityAndValidation(t *testing.T) {
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	s := &socialHTTPStub{}
	auth := &authStub{t: t}
	NewFollow(s, auth).Register(h)
	NewFeed(s, auth).Register(h)
	for _, tc := range []struct{ method, path string }{{"PUT", "/api/v1/users/3/follow"}, {"DELETE", "/api/v1/users/3/follow"}, {"GET", "/api/v1/users/3/follow"}, {"GET", "/api/v1/users/me/following"}, {"GET", "/api/v1/feed"}} {
		res := ut.PerformRequest(h.Engine, tc.method, tc.path, nil)
		if res.Code != 401 {
			t.Fatalf("unauthenticated %s: %d", tc.path, res.Code)
		}
		res = ut.PerformRequest(h.Engine, tc.method, tc.path, nil, ut.Header{Key: "Authorization", Value: "Bearer private-token"})
		if res.Code != 200 || s.user != 7 || res.Header().Get("Cache-Control") != "no-store" {
			t.Fatalf("%s => %d %s", tc.path, res.Code, res.Body)
		}
	}
	for _, path := range []string{"/api/v1/users/0/follow", "/api/v1/users/abc/follow", "/api/v1/users/me/following?page=0", "/api/v1/users/me/following?page_size=51", "/api/v1/feed?page_size=0", "/api/v1/feed?cursor=a&cursor=b"} {
		res := ut.PerformRequest(h.Engine, "GET", path, nil, ut.Header{Key: "Authorization", Value: "Bearer private-token"})
		if res.Code != 400 {
			t.Fatalf("invalid %s => %d", path, res.Code)
		}
	}
	res := ut.PerformRequest(h.Engine, "GET", "/api/v1/feed?cursor=example&page_size=5", nil, ut.Header{Key: "Authorization", Value: "Bearer private-token"})
	if res.Code != 200 || s.cursor != "example" || s.size != 5 {
		t.Fatal("cursor parameters not propagated")
	}
	s.err = apperror.New(apperror.Conflict, nil)
	res = ut.PerformRequest(h.Engine, "GET", "/api/v1/feed?cursor=expired", nil, ut.Header{Key: "Authorization", Value: "Bearer private-token"})
	if res.Code != 409 {
		t.Fatalf("expired cursor status=%d", res.Code)
	}
}
