package service

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/ashuaiy/local-life-go/internal/model"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
)

type Blogs interface {
	Create(context.Context, model.Blog) (model.Blog, error)
	ByID(context.Context, uint64) (model.Blog, error)
	List(ctx context.Context, shopID, userID uint64, offset, limit int) ([]model.Blog, error)
	SetLike(ctx context.Context, blogID, userID uint64, liked bool) (bool, error)
	IsLiked(ctx context.Context, blogID, userID uint64) (bool, error)
}
type BlogShops interface {
	ByID(context.Context, uint64) (model.Shop, error)
}
type Blog struct {
	blogs         Blogs
	shops         BlogShops
	publisher     BlogPublisher
	profiles      PublicProfiles
	notifications LikeNotifier
}

type PublicProfiles interface {
	Profile(context.Context, uint64) (model.Profile, error)
}
type LikeNotifier interface {
	NotifyLike(context.Context, uint64, uint64, uint64, model.Profile) error
}

func (b *Blog) WithNotifications(profiles PublicProfiles, sink LikeNotifier) *Blog {
	b.profiles = profiles
	b.notifications = sink
	return b
}

type BlogPublisher interface {
	Push(context.Context, model.Blog) error
}

func NewBlog(blogs Blogs, shops BlogShops, publisher BlogPublisher) *Blog {
	return &Blog{blogs: blogs, shops: shops, publisher: publisher}
}

type BlogInput struct {
	Images  []string `json:"images"`
	ShopID  uint64   `json:"shop_id,string"`
	Title   string   `json:"title"`
	Content string   `json:"content"`
}
type BlogView struct {
	Images    []string  `json:"images"`
	Nickname  string    `json:"nickname"`
	Avatar    string    `json:"avatar"`
	ID        uint64    `json:"id,string"`
	UserID    uint64    `json:"user_id,string"`
	ShopID    uint64    `json:"shop_id,string"`
	Title     string    `json:"title"`
	Content   string    `json:"content"`
	LikeCount int64     `json:"like_count"`
	CreatedAt time.Time `json:"created_at"`
}
type BlogPage struct {
	Items    []BlogView `json:"items"`
	Page     int        `json:"page"`
	PageSize int        `json:"page_size"`
	HasMore  bool       `json:"has_more"`
}
type LikeResult struct {
	Liked bool `json:"liked"`
}

func (b *Blog) Publish(ctx context.Context, userID uint64, input BlogInput) (BlogView, error) {
	if userID == 0 {
		return BlogView{}, apperror.New(apperror.Unauthorized, nil)
	}
	title, content := strings.TrimSpace(input.Title), strings.TrimSpace(input.Content)
	if !validBlogImages(input.Images) || input.ShopID == 0 || !validBlogText(title, 255) || !validBlogText(content, 10000) {
		return BlogView{}, apperror.New(apperror.Validation, nil)
	}
	if _, err := b.shops.ByID(ctx, input.ShopID); err != nil {
		return BlogView{}, err
	}
	images, _ := json.Marshal(input.Images)
	row, err := b.blogs.Create(ctx, model.Blog{Images: string(images), UserID: userID, ShopID: input.ShopID, Title: title, Content: content})
	if err != nil {
		return BlogView{}, err
	}
	if b.publisher != nil {
		if err := b.publisher.Push(ctx, row); err != nil {
			return BlogView{}, feedError(err)
		}
	}
	return blogView(row), nil
}
func (b *Blog) Detail(ctx context.Context, id uint64) (BlogView, error) {
	if id == 0 {
		return BlogView{}, apperror.New(apperror.Validation, nil)
	}
	row, err := b.blogs.ByID(ctx, id)
	if err != nil {
		return BlogView{}, err
	}
	return blogView(row), nil
}
func (b *Blog) List(ctx context.Context, shopID, userID uint64, page, pageSize int) (BlogPage, error) {
	if page < 1 || page > 1000 || pageSize < 1 || pageSize > 50 {
		return BlogPage{}, apperror.New(apperror.Validation, nil)
	}
	rows, err := b.blogs.List(ctx, shopID, userID, (page-1)*pageSize, pageSize+1)
	if err != nil {
		return BlogPage{}, err
	}
	result := BlogPage{Items: make([]BlogView, 0, min(len(rows), pageSize)), Page: page, PageSize: pageSize, HasMore: len(rows) > pageSize}
	if result.HasMore {
		rows = rows[:pageSize]
	}
	for _, row := range rows {
		result.Items = append(result.Items, blogView(row))
	}
	return result, nil
}
func (b *Blog) Like(ctx context.Context, blogID, userID uint64, liked bool) (LikeResult, error) {
	if userID == 0 {
		return LikeResult{}, apperror.New(apperror.Unauthorized, nil)
	}
	row, err := b.Detail(ctx, blogID)
	if err != nil {
		return LikeResult{}, err
	}
	changed, err := b.blogs.SetLike(ctx, blogID, userID, liked)
	if err != nil {
		return LikeResult{}, err
	}
	if changed && liked && userID != row.UserID && b.notifications != nil {
		b.notifyLike(ctx, blogID, userID, row.UserID)
	}
	return LikeResult{Liked: liked}, nil
}

func (b *Blog) notifyLike(ctx context.Context, blogID, userID, authorID uint64) {
	budget := 100 * time.Millisecond
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining < 20*time.Millisecond {
			return
		}
		budget = min(budget, remaining/2)
	}
	// Optional delivery must not exhaust the request deadline after the like has committed.
	notifyCtx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	profile, err := b.profiles.Profile(notifyCtx, userID)
	if err == nil {
		err = b.notifications.NotifyLike(notifyCtx, blogID, userID, authorID, profile)
	}
	if err != nil {
		slog.WarnContext(ctx, "like persisted; notification delivery failed")
	}
}
func (b *Blog) LikeState(ctx context.Context, blogID, userID uint64) (LikeResult, error) {
	if userID == 0 {
		return LikeResult{}, apperror.New(apperror.Unauthorized, nil)
	}
	if _, err := b.Detail(ctx, blogID); err != nil {
		return LikeResult{}, err
	}
	liked, err := b.blogs.IsLiked(ctx, blogID, userID)
	if err != nil {
		return LikeResult{}, err
	}
	return LikeResult{Liked: liked}, nil
}
func validBlogText(value string, max int) bool {
	return value != "" && utf8.ValidString(value) && utf8.RuneCountInString(value) <= max
}
func blogView(row model.Blog) BlogView {
	images := []string{}
	_ = json.Unmarshal([]byte(row.Images), &images)
	if images == nil {
		images = []string{}
	}
	return BlogView{Images: images, Nickname: row.Nickname, Avatar: row.Avatar, ID: row.ID, UserID: row.UserID, ShopID: row.ShopID, Title: row.Title, Content: row.Content, LikeCount: row.LikeCount, CreatedAt: row.CreatedAt}
}

func validBlogImages(images []string) bool {
	if len(images) > 9 {
		return false
	}
	for _, value := range images {
		if len(value) > 512 {
			return false
		}
		u, err := url.Parse(value)
		if err != nil || u.User != nil || u.Fragment != "" {
			return false
		}
		if u.Scheme == "https" && u.Host != "" {
			continue
		}
		if !validMediaURL(value) {
			return false
		}
	}
	return true
}
