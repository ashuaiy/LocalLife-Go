package handler

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ashuaiy/local-life-go/internal/middleware"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
	"net/url"
	"strings"
)

type Community struct {
	service *service.Community
	media   *service.Media
	auth    middleware.Authenticator
}

func NewCommunity(s *service.Community, m *service.Media, a middleware.Authenticator) *Community {
	return &Community{service: s, media: m, auth: a}
}
func (h *Community) Register(server *server.Hertz) {
	server.GET("/api/v1/users/:id", h.profile)
	server.GET("/api/v1/blogs/hot", h.hot)
	server.GET("/api/v1/blogs/:id/likes", h.likers)
	server.GET("/uploads/:name", h.image)
	protected := server.Group("/api/v1", func(ctx context.Context, c *app.RequestContext) { c.Header("Cache-Control", "no-store"); c.Next(ctx) }, middleware.RequireAuth(h.auth))
	protected.GET("/users/:id/common-following", h.common)
	protected.POST("/users/me/sign", h.sign(true))
	protected.GET("/users/me/sign", h.sign(false))
	protected.DELETE("/blogs/:id", h.deleteBlog)
	protected.POST("/uploads", h.upload)
	protected.GET("/messages/sse", h.messages)
}
func communityID(ctx context.Context, c *app.RequestContext) (uint64, bool) {
	id, err := shopPositiveNumber(c.Param("id"), ^uint64(0))
	if err != nil {
		response.Fail(ctx, c, err)
		return 0, false
	}
	return id, true
}
func communityPage(c *app.RequestContext) (int, int, error) {
	q, err := url.ParseQuery(string(c.Request.URI().QueryString()))
	if err != nil {
		return 0, 0, apperror.New(apperror.Validation, err)
	}
	page, e1 := shopQueryNumber(q, "page", 1, 1000)
	size, e2 := shopQueryNumber(q, "page_size", 10, 50)
	if e1 != nil || e2 != nil {
		return 0, 0, apperror.New(apperror.Validation, nil)
	}
	return int(page), int(size), nil
}
func communityResponse(ctx context.Context, c *app.RequestContext, data any, err error) {
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, data)
}
func (h *Community) profile(ctx context.Context, c *app.RequestContext) {
	id, ok := communityID(ctx, c)
	if !ok {
		return
	}
	data, err := h.service.Profile(ctx, id)
	communityResponse(ctx, c, data, err)
}
func (h *Community) hot(ctx context.Context, c *app.RequestContext) {
	page, size, err := communityPage(c)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	data, err := h.service.Hot(ctx, page, size)
	communityResponse(ctx, c, data, err)
}
func (h *Community) common(ctx context.Context, c *app.RequestContext) {
	id, ok := communityID(ctx, c)
	if !ok {
		return
	}
	page, size, err := communityPage(c)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	data, err := h.service.Common(ctx, middleware.UserID(ctx), id, page, size)
	communityResponse(ctx, c, data, err)
}
func (h *Community) likers(ctx context.Context, c *app.RequestContext) {
	id, ok := communityID(ctx, c)
	if !ok {
		return
	}
	data, err := h.service.Likers(ctx, id)
	communityResponse(ctx, c, data, err)
}
func (h *Community) deleteBlog(ctx context.Context, c *app.RequestContext) {
	id, ok := communityID(ctx, c)
	if !ok {
		return
	}
	communityResponse(ctx, c, nil, h.service.DeleteBlog(ctx, id, middleware.UserID(ctx)))
}
func (h *Community) sign(write bool) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		data, err := h.service.Sign(ctx, middleware.UserID(ctx), write)
		communityResponse(ctx, c, data, err)
	}
}
func (h *Community) upload(ctx context.Context, c *app.RequestContext) {
	if len(c.Request.Body()) > service.MaxImageBytes+64*1024 {
		response.Fail(ctx, c, apperror.New(apperror.Validation, nil))
		return
	}
	file, err := c.FormFile("file")
	if err != nil || file.Size > service.MaxImageBytes {
		response.Fail(ctx, c, apperror.New(apperror.Validation, err))
		return
	}
	reader, err := file.Open()
	if err != nil {
		response.Fail(ctx, c, apperror.New(apperror.Validation, err))
		return
	}
	defer reader.Close()
	path, err := h.media.Save(ctx, reader)
	communityResponse(ctx, c, map[string]string{"url": path}, err)
}
func (h *Community) image(ctx context.Context, c *app.RequestContext) {
	data, kind, err := h.media.Read(ctx, c.Param("name"))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cache-Control", "public, max-age=86400")
	c.Data(200, kind, data)
}
func (h *Community) messages(ctx context.Context, c *app.RequestContext) {
	messages, err := h.service.Messages(ctx, middleware.UserID(ctx), string(c.GetHeader("Last-Event-ID")))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	// A bounded SSE batch respects the ordinary HTTP deadline. Clients reconnect with Last-Event-ID.
	var body strings.Builder
	body.WriteString("retry: 1000\n\n")
	for _, message := range messages {
		data, _ := json.Marshal(message)
		fmt.Fprintf(&body, "id: %s\nevent: like\ndata: %s\n\n", message.ID, data)
	}
	c.Header("X-Accel-Buffering", "no")
	c.Data(200, "text/event-stream; charset=utf-8", []byte(body.String()))
}
