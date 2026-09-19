package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

type blogHTTPStub struct {
	userID, blogID, shopID, filterUser uint64
	input                              service.BlogInput
	page, size, calls                  int
	liked                              bool
	err                                error
}

func (s *blogHTTPStub) Publish(_ context.Context, userID uint64, input service.BlogInput) (service.BlogView, error) {
	s.calls++
	s.userID, s.input = userID, input
	return service.BlogView{ID: 9, UserID: userID, ShopID: input.ShopID, Title: input.Title, Content: input.Content}, s.err
}
func (s *blogHTTPStub) Detail(_ context.Context, id uint64) (service.BlogView, error) {
	s.calls++
	s.blogID = id
	return service.BlogView{ID: id}, s.err
}
func (s *blogHTTPStub) List(_ context.Context, shopID, userID uint64, page, size int) (service.BlogPage, error) {
	s.calls++
	s.shopID, s.filterUser, s.page, s.size = shopID, userID, page, size
	return service.BlogPage{Items: []service.BlogView{}, Page: page, PageSize: size}, s.err
}
func (s *blogHTTPStub) Like(_ context.Context, id, userID uint64, liked bool) (service.LikeResult, error) {
	s.calls++
	s.blogID, s.userID, s.liked = id, userID, liked
	return service.LikeResult{Liked: liked}, s.err
}
func (s *blogHTTPStub) LikeState(_ context.Context, id, userID uint64) (service.LikeResult, error) {
	s.calls++
	s.blogID, s.userID = id, userID
	return service.LikeResult{Liked: s.liked}, s.err
}

func TestBlogHTTPAuthorizationAndContract(t *testing.T) {
	cfg, _ := config.LoadFrom(func(string) (string, bool) { return "", false })
	var logs bytes.Buffer
	h := appserver.NewServer(cfg, slog.New(slog.NewJSONHandler(&logs, nil)), nil)
	s := &blogHTTPStub{}
	NewBlog(s, &authStub{t: t}).Register(h)
	request := func(method, path, body, token string, want int) []byte {
		t.Helper()
		res := ut.PerformRequest(h.Engine, method, path, &ut.Body{Body: strings.NewReader(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Authorization", Value: token})
		if res.Code != want {
			t.Fatalf("%s %s => %d %s", method, path, res.Code, res.Body)
		}
		if !json.Valid(res.Body.Bytes()) {
			t.Fatal("invalid JSON response")
		}
		if token != "" && res.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("personal response can be cached")
		}
		return res.Body.Bytes()
	}
	request("GET", "/api/v1/blogs", "", "", 200)
	if s.page != 1 || s.size != 10 {
		t.Fatalf("defaults=%+v", s)
	}
	request("GET", "/api/v1/blogs?shop_id=3&user_id=7&page=2&page_size=5", "", "", 200)
	if s.shopID != 3 || s.filterUser != 7 || s.page != 2 || s.size != 5 {
		t.Fatalf("filters=%+v", s)
	}
	request("GET", "/api/v1/blogs/18446744073709551615", "", "", 200)
	for _, tc := range []struct{ method, path string }{{"POST", "/api/v1/blogs"}, {"PUT", "/api/v1/blogs/9/like"}, {"DELETE", "/api/v1/blogs/9/like"}, {"GET", "/api/v1/blogs/9/like"}} {
		calls := s.calls
		request(tc.method, tc.path, "", "", 401)
		if s.calls != calls {
			t.Fatal("unauthenticated request reached service")
		}
	}
	privateBody := "探店正文-不应出现在日志"
	content := strings.Repeat("好", 2000) + privateBody
	encoded, _ := json.Marshal(service.BlogInput{ShopID: 3, Title: "新探店", Content: content})
	response := request("POST", "/api/v1/blogs", string(encoded), "Bearer private-token", 200)
	if s.userID != 7 || s.input.Content != content || !bytes.Contains(response, []byte(`"user_id":"7"`)) {
		t.Fatal("trusted author or content missing")
	}
	for _, method := range []string{"PUT", "PUT", "GET", "DELETE", "DELETE", "GET"} {
		request(method, "/api/v1/blogs/9/like", "", "Bearer private-token", 200)
	}
	if s.userID != 7 || s.blogID != 9 || s.liked {
		t.Fatalf("like identity=%+v", s)
	}
	for _, body := range []string{`{"shop_id":"3","title":"x","content":"y","user_id":"99"}`, `{"shop_id":3,"title":"x","content":"y"}`, `{} {}`, `{broken`, strings.Repeat(" ", 65537)} {
		request("POST", "/api/v1/blogs", body, "Bearer private-token", 400)
	}
	for _, path := range []string{"/api/v1/blogs/0", "/api/v1/blogs/abc", "/api/v1/blogs?page=0", "/api/v1/blogs?page_size=51", "/api/v1/blogs?shop_id=0", "/api/v1/blogs?user_id=", "/api/v1/blogs?page=1&page=2"} {
		request("GET", path, "", "", 400)
	}
	s.err = apperror.New(apperror.NotFound, nil)
	request("GET", "/api/v1/blogs/99", "", "", 404)
	s.err = apperror.New(apperror.Dependency, nil)
	request("PUT", "/api/v1/blogs/9/like", "", "Bearer private-token", 503)
	if strings.Contains(logs.String(), privateBody) || strings.Contains(logs.String(), "private-token") {
		t.Fatal("body or token leaked to logs")
	}
}
