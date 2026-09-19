package integration

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"log/slog"
	"mime/multipart"
	"strings"
	"testing"
	"time"

	appserver "github.com/ashuaiy/local-life-go/internal/app"
	"github.com/ashuaiy/local-life-go/internal/cache"
	"github.com/ashuaiy/local-life-go/internal/config"
	"github.com/ashuaiy/local-life-go/internal/handler"
	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/internal/repository"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/cloudwego/hertz/pkg/common/ut"
)

func TestCommunityMigration(t *testing.T) {
	deps, ctx := authDependencies(t)
	users, shops := blogFixtures(t, deps, ctx)
	third := model.User{Phone: "cm-" + rand.Text(), Nickname: "common"}
	if err := deps.DB.WithContext(ctx).Create(&third).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		clean := context.Background()
		deps.DB.WithContext(clean).Where("user_id IN ?", []uint64{users[0].ID, users[1].ID}).Delete(&model.Follow{})
		deps.DB.WithContext(clean).Delete(&third)
		for _, u := range users {
			deps.Redis.Del(clean, fmt.Sprintf("locallife:messages:%d", u.ID), fmt.Sprintf("locallife:sign:%d:%s", u.ID, time.Now().In(time.FixedZone("CST", 8*3600)).Format("200601")))
		}
	})
	cfg, _ := config.Load()
	store := cache.NewAuth(deps.Redis)
	auth := service.NewAuth(repository.NewUser(deps.DB), store, cfg.Auth)
	tokens := make([]string, 2)
	for i, u := range users {
		b := make([]byte, 32)
		if _, err := rand.Read(b); err != nil {
			t.Fatal(err)
		}
		tokens[i] = base64.RawURLEncoding.EncodeToString(b)
		if err := store.SaveSession(ctx, tokens[i], u.ID, time.Minute); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = store.DeleteSession(context.Background(), tokens[i]) })
	}
	communityRepo := repository.NewCommunity(deps.DB)
	communityStore := cache.NewCommunity(deps.Redis)
	blog := service.NewBlog(repository.NewBlog(deps.DB), repository.NewShop(deps.DB), nil).WithNotifications(communityRepo, communityStore)
	h := appserver.NewServer(cfg, slog.New(slog.NewTextHandler(io.Discard, nil)), nil)
	handler.NewBlog(blog, auth).Register(h)
	handler.NewFollow(service.NewFollow(repository.NewFollow(deps.DB), repository.NewUser(deps.DB)), auth).Register(h)
	handler.NewCommunity(service.NewCommunity(communityRepo, communityStore), service.NewMedia(t.TempDir()), auth).Register(h)
	request := func(t *testing.T, method, path, body, token string, want int) json.RawMessage {
		t.Helper()
		res := ut.PerformRequest(h.Engine, method, path, &ut.Body{Body: strings.NewReader(body), Len: len(body)}, ut.Header{Key: "Content-Type", Value: "application/json"}, ut.Header{Key: "Authorization", Value: "Bearer " + token})
		if res.Code != want {
			t.Fatalf("%s %s: status=%d want=%d body=%s", method, path, res.Code, want, res.Body)
		}
		var envelope struct {
			Data json.RawMessage `json:"data"`
		}
		if err := json.Unmarshal(res.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	t.Run("profile and common follows", func(t *testing.T) {
		data := request(t, "GET", fmt.Sprintf("/api/v1/users/%d", users[0].ID), "", "", 200)
		if bytes.Contains(data, []byte("phone")) || !bytes.Contains(data, []byte("nickname")) {
			t.Fatalf("public profile: %s", data)
		}
		request(t, "GET", "/api/v1/users/18446744073709551615", "", "", 404)
		for i := range users {
			request(t, "PUT", fmt.Sprintf("/api/v1/users/%d/follow", third.ID), "", tokens[i], 200)
		}
		data = request(t, "GET", fmt.Sprintf("/api/v1/users/%d/common-following", users[1].ID), "", tokens[0], 200)
		var page struct {
			Items []service.PublicUser `json:"items"`
		}
		if err := json.Unmarshal(data, &page); err != nil || len(page.Items) != 1 || page.Items[0].ID != third.ID {
			t.Fatalf("common=%s err=%v", data, err)
		}
		request(t, "GET", fmt.Sprintf("/api/v1/users/%d/common-following?page=0", users[1].ID), "", tokens[0], 400)
	})
	t.Run("monthly bitmap check-in", func(t *testing.T) {
		request(t, "POST", "/api/v1/users/me/sign", "", "", 401)
		for range 2 {
			request(t, "POST", "/api/v1/users/me/sign", "", tokens[0], 200)
		}
		data := request(t, "GET", "/api/v1/users/me/sign", "", tokens[0], 200)
		var state struct {
			Signed bool `json:"signed"`
			Streak int  `json:"streak"`
		}
		if err := json.Unmarshal(data, &state); err != nil || !state.Signed || state.Streak != 1 {
			t.Fatalf("sign=%s err=%v", data, err)
		}
		data = request(t, "GET", "/api/v1/users/me/sign", "", tokens[1], 200)
		if err := json.Unmarshal(data, &state); err != nil || state.Signed || state.Streak != 0 {
			t.Fatalf("isolated sign=%s", data)
		}
	})
	t.Run("notification timeout preserves committed like", func(t *testing.T) {
		row, err := blog.Publish(ctx, users[0].ID, service.BlogInput{ShopID: shops[0].ID, Title: "slow notification", Content: "body"})
		if err != nil {
			t.Fatal(err)
		}
		blog.WithNotifications(communityRepo, slowLikeNotification{})
		defer blog.WithNotifications(communityRepo, communityStore)
		request(t, "PUT", fmt.Sprintf("/api/v1/blogs/%d/like", row.ID), "", tokens[1], 200)
	})
	t.Run("overflow notification cursor", func(t *testing.T) {
		res := ut.PerformRequest(h.Engine, "GET", "/api/v1/messages/sse", nil, ut.Header{Key: "Authorization", Value: "Bearer " + tokens[0]}, ut.Header{Key: "Last-Event-ID", Value: "18446744073709551616-0"})
		if res.Code != 400 {
			t.Fatalf("overflow cursor=%d %s", res.Code, res.Body)
		}
	})
	t.Run("hot blogs likers notifications images and ownership", func(t *testing.T) {
		var uploaded bytes.Buffer
		writer := multipart.NewWriter(&uploaded)
		part, err := writer.CreateFormFile("file", "photo.png")
		if err != nil {
			t.Fatal(err)
		}
		if err := png.Encode(part, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
			t.Fatal(err)
		}
		if err := writer.Close(); err != nil {
			t.Fatal(err)
		}
		upload := ut.PerformRequest(h.Engine, "POST", "/api/v1/uploads", &ut.Body{Body: bytes.NewReader(uploaded.Bytes()), Len: uploaded.Len()}, ut.Header{Key: "Content-Type", Value: writer.FormDataContentType()}, ut.Header{Key: "Authorization", Value: "Bearer " + tokens[0]})
		if upload.Code != 200 {
			t.Fatalf("upload: %d %s", upload.Code, upload.Body)
		}
		var result struct {
			Data struct {
				URL string `json:"url"`
			} `json:"data"`
		}
		if err := json.Unmarshal(upload.Body.Bytes(), &result); err != nil || result.Data.URL == "" {
			t.Fatalf("upload result: %v %s", err, upload.Body)
		}
		fetched := ut.PerformRequest(h.Engine, "GET", result.Data.URL, nil)
		if fetched.Code != 200 {
			t.Fatalf("image unavailable: %d", fetched.Code)
		}
		if _, err := png.Decode(bytes.NewReader(fetched.Body.Bytes())); err != nil {
			t.Fatal(err)
		}
		body := fmt.Sprintf(`{"shop_id":"%d","title":"with image","content":"content","images":[%q]}`, shops[0].ID, result.Data.URL)
		data := request(t, "POST", "/api/v1/blogs", body, tokens[0], 200)
		var row service.BlogView
		if err := json.Unmarshal(data, &row); err != nil {
			t.Fatal(err)
		}
		if !bytes.Contains(data, []byte(result.Data.URL)) {
			t.Fatalf("images not persisted: %s", data)
		}
		path := fmt.Sprintf("/api/v1/blogs/%d", row.ID)
		request(t, "PUT", path+"/like", "", tokens[1], 200)
		request(t, "PUT", path+"/like", "", tokens[1], 200)
		likers := request(t, "GET", path+"/likes", "", "", 200)
		if !bytes.Contains(likers, []byte(users[1].Nickname)) || bytes.Contains(likers, []byte("phone")) {
			t.Fatalf("likers=%s", likers)
		}
		hot := request(t, "GET", "/api/v1/blogs/hot", "", "", 200)
		if !bytes.Contains(hot, []byte("with image")) {
			t.Fatalf("hot=%s", hot)
		}
		messages := ut.PerformRequest(h.Engine, "GET", "/api/v1/messages/sse", nil, ut.Header{Key: "Authorization", Value: "Bearer " + tokens[0]})
		if messages.Code != 200 || strings.Count(messages.Body.String(), "event: like") != 1 {
			t.Fatalf("messages: %d %s", messages.Code, messages.Body)
		}
		id := ""
		for line := range strings.SplitSeq(messages.Body.String(), "\n") {
			if strings.HasPrefix(line, "id: ") {
				id = strings.TrimPrefix(line, "id: ")
			}
		}
		replay := ut.PerformRequest(h.Engine, "GET", "/api/v1/messages/sse", nil, ut.Header{Key: "Authorization", Value: "Bearer " + tokens[0]}, ut.Header{Key: "Last-Event-ID", Value: id})
		if replay.Code != 200 || strings.Contains(replay.Body.String(), "event: like") {
			t.Fatalf("replay: %d %s", replay.Code, replay.Body)
		}
		request(t, "DELETE", path, "", tokens[1], 404)
		request(t, "GET", path, "", "", 200)
		request(t, "DELETE", path, "", tokens[0], 200)
		request(t, "GET", path, "", "", 404)
	})
}

type slowLikeNotification struct{}

func (slowLikeNotification) NotifyLike(ctx context.Context, _, _, _ uint64, _ model.Profile) error {
	<-ctx.Done()
	return ctx.Err()
}
