package integration

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/platform"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func blogFixtures(t *testing.T, deps *platform.Connections, ctx context.Context) ([]model.User, []model.Shop) {
	t.Helper()
	_, shops := shopFixtures(t, deps, ctx)
	users := []model.User{{Phone: "blog-" + rand.Text(), Nickname: "author"}, {Phone: "blog-" + rand.Text(), Nickname: "reader"}}
	if err := deps.DB.WithContext(ctx).Create(&users).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		ids := []uint64{users[0].ID, users[1].ID}
		for _, query := range []string{"DELETE FROM blog_like WHERE user_id IN ?", "DELETE FROM blog WHERE user_id IN ?", "DELETE FROM users WHERE id IN ?"} {
			if err := deps.DB.WithContext(clean).Exec(query, ids).Error; err != nil {
				t.Error(err)
			}
		}
	})
	return users, shops
}

func TestBlogPersistencePaginationAndConcurrentLikes(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, shops := blogFixtures(t, deps, ctx)
	repo := repository.NewBlog(deps.DB)
	b := service.NewBlog(repo, repository.NewShop(deps.DB), nil)
	var rows []service.BlogView
	for range 3 {
		row, err := b.Publish(ctx, users[0].ID, service.BlogInput{ShopID: shops[0].ID, Title: "新探店", Content: strings.Repeat("多字节正文", 1000)})
		if err != nil || row.ID == 0 || row.UserID != users[0].ID || row.CreatedAt.IsZero() || row.LikeCount != 0 {
			t.Fatalf("publish=%+v err=%v", row, err)
		}
		rows = append(rows, row)
	}
	other, err := b.Publish(ctx, users[1].ID, service.BlogInput{ShopID: shops[1].ID, Title: "其他商户", Content: "内容"})
	if err != nil {
		t.Fatal(err)
	}
	// Force the same timestamp to verify the secondary ID ordering across page boundaries.
	if err := deps.DB.WithContext(ctx).Model(&model.Blog{}).Where("user_id IN ?", []uint64{users[0].ID, users[1].ID}).Update("created_at", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)).Error; err != nil {
		t.Fatal(err)
	}
	first, err := b.List(ctx, shops[0].ID, users[0].ID, 1, 2)
	if err != nil || len(first.Items) != 2 || !first.HasMore || first.Items[0].ID != rows[2].ID || first.Items[1].ID != rows[1].ID {
		t.Fatalf("page1=%+v err=%v", first, err)
	}
	second, err := b.List(ctx, shops[0].ID, users[0].ID, 2, 2)
	if err != nil || len(second.Items) != 1 || second.HasMore || second.Items[0].ID != rows[0].ID {
		t.Fatalf("page2=%+v err=%v", second, err)
	}
	empty, err := b.List(ctx, shops[0].ID, users[1].ID, 1, 10)
	if err != nil || empty.Items == nil || len(empty.Items) != 0 {
		t.Fatalf("filter intersection=%+v err=%v", empty, err)
	}
	blogID := rows[0].ID
	var wg sync.WaitGroup
	for range 20 {
		wg.Go(func() {
			if _, err := b.Like(ctx, blogID, users[1].ID, true); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	detail, err := b.Detail(ctx, blogID)
	if err != nil || detail.LikeCount != 1 {
		t.Fatalf("same user duplicate likes: %+v err=%v", detail, err)
	}
	if _, err := b.Like(ctx, blogID, users[0].ID, true); err != nil {
		t.Fatal(err)
	}
	detail, err = b.Detail(ctx, blogID)
	if err != nil || detail.LikeCount != 2 {
		t.Fatalf("different users=%+v err=%v", detail, err)
	}
	state, err := b.LikeState(ctx, blogID, users[1].ID)
	if err != nil || !state.Liked {
		t.Fatalf("like state=%+v err=%v", state, err)
	}
	for range 20 {
		wg.Go(func() {
			if _, err := b.Like(ctx, blogID, users[1].ID, false); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	detail, err = b.Detail(ctx, blogID)
	if err != nil || detail.LikeCount != 1 {
		t.Fatalf("unlike removed another user or wrong count: %+v err=%v", detail, err)
	}
	state, err = b.LikeState(ctx, blogID, users[1].ID)
	if err != nil || state.Liked {
		t.Fatalf("unliked state=%+v err=%v", state, err)
	}
	if err := deps.DB.WithContext(ctx).Delete(&model.Blog{}, other.ID).Error; err != nil {
		t.Fatal(err)
	}
	_, err = b.Like(ctx, other.ID, users[1].ID, true)
	status, _, _ := apperror.Describe(err)
	if status != 404 {
		t.Fatalf("missing blog like=%v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	_, err = repo.ByID(canceled, blogID)
	status, _, _ = apperror.Describe(err)
	if status != 408 {
		t.Fatalf("repository lost cancellation=%v", err)
	}
}

func TestBlogHTTPWithRealAuthentication(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, shops := blogFixtures(t, deps, ctx)
	cfg, _ := config.Load()
	authStore := cache.NewAuth(deps.Redis)
	bytes := make([]byte, 32)
	if _, err := rand.Read(bytes); err != nil {
		t.Fatal(err)
	}
	token := base64.RawURLEncoding.EncodeToString(bytes)
	if err := authStore.SaveSession(ctx, token, users[0].ID, time.Minute); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = authStore.DeleteSession(context.Background(), token) })
	auth := service.NewAuth(repository.NewUser(deps.DB), authStore, cfg.Auth)
	b := service.NewBlog(repository.NewBlog(deps.DB), repository.NewShop(deps.DB), nil)
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler.NewBlog(b, auth).Register(h)
	request := func(method, path, body, bearer string, want int) json.RawMessage {
		t.Helper()
		res := ut.PerformRequest(h.Engine, method, path, &ut.Body{Body: strings.NewReader(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Authorization", Value: "Bearer " + bearer})
		if res.Code != want {
			t.Fatalf("%s %s => %d %s", method, path, res.Code, res.Body)
		}
		var envelope struct {
			Data      json.RawMessage
			RequestID string `json:"request_id"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil || envelope.RequestID == "" {
			t.Fatalf("invalid envelope=%s", res.Body)
		}
		return envelope.Data
	}
	body := fmt.Sprintf(`{"shop_id":"%d","title":"真实链路","content":"发布探店内容"}`, shops[0].ID)
	request("POST", "/api/v1/blogs", body, "", 401)
	var published service.BlogView
	if err := json.Unmarshal(request("POST", "/api/v1/blogs", body, token, 200), &published); err != nil || published.UserID != users[0].ID || published.ID == 0 {
		t.Fatalf("published=%+v err=%v", published, err)
	}
	path := fmt.Sprintf("/api/v1/blogs/%d", published.ID)
	request("PUT", path+"/like", "", token, 200)
	request("PUT", path+"/like", "", token, 200)
	var detail service.BlogView
	if err := json.Unmarshal(request("GET", path, "", "", 200), &detail); err != nil || detail.LikeCount != 1 || detail.UserID != users[0].ID {
		t.Fatalf("detail=%+v err=%v", detail, err)
	}
	request("DELETE", path+"/like", "", token, 200)
	var state service.LikeResult
	if err := json.Unmarshal(request("GET", path+"/like", "", token, 200), &state); err != nil || state.Liked {
		t.Fatalf("state=%+v err=%v", state, err)
	}
	if err := authStore.DeleteSession(ctx, token); err != nil {
		t.Fatal(err)
	}
	request("POST", "/api/v1/blogs", body, token, 401)
}
