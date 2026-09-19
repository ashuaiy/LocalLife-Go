package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"

	"github.com/ashuaiy/local-life-go/internal/middleware"
	"github.com/ashuaiy/local-life-go/internal/service"
	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/app/server"
)

type AuthService interface {
	middleware.Authenticator
	RequestCode(context.Context, string) (service.CodeResult, error)
	Login(context.Context, string, string) (service.LoginResult, error)
	Me(context.Context, uint64) (service.PublicUser, error)
	Logout(context.Context, string) error
}
type Auth struct{ service AuthService }

func NewAuth(auth AuthService) *Auth { return &Auth{service: auth} }

func (a *Auth) Register(h *server.Hertz) {
	group := h.Group("/api/v1", func(ctx context.Context, c *app.RequestContext) { c.Header("Cache-Control", "no-store"); c.Next(ctx) })
	group.POST("/auth/code", a.code)
	group.POST("/auth/login", a.login)
	group.POST("/auth/logout", middleware.RequireAuth(a.service), a.logout)
	group.GET("/users/me", middleware.RequireAuth(a.service), a.me)
}

func decodeJSON(c *app.RequestContext, target any) error {
	return decodeJSONLimit(c, target, 1024)
}

func decodeJSONLimit(c *app.RequestContext, target any, maxBytes int) error {
	mediaType, _, err := mime.ParseMediaType(string(c.ContentType()))
	if err != nil || mediaType != "application/json" || len(c.Request.Body()) > maxBytes {
		return apperror.New(apperror.Validation, nil)
	}
	decoder := json.NewDecoder(bytes.NewReader(c.Request.Body()))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return apperror.New(apperror.Validation, nil)
	}
	if err := decoder.Decode(new(any)); !errors.Is(err, io.EOF) {
		return apperror.New(apperror.Validation, nil)
	}
	return nil
}

func (a *Auth) code(ctx context.Context, c *app.RequestContext) {
	var input struct {
		Phone string `json:"phone"`
	}
	if err := decodeJSON(c, &input); err != nil {
		response.Fail(ctx, c, err)
		return
	}
	result, err := a.service.RequestCode(ctx, input.Phone)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func (a *Auth) login(ctx context.Context, c *app.RequestContext) {
	var input struct {
		Phone string `json:"phone"`
		Code  string `json:"code"`
	}
	if err := decodeJSON(c, &input); err != nil {
		response.Fail(ctx, c, err)
		return
	}
	result, err := a.service.Login(ctx, input.Phone, input.Code)
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, result)
}
func (a *Auth) me(ctx context.Context, c *app.RequestContext) {
	user, err := a.service.Me(ctx, middleware.UserID(ctx))
	if err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, user)
}
func (a *Auth) logout(ctx context.Context, c *app.RequestContext) {
	token, _ := middleware.BearerToken(string(c.GetHeader("Authorization")))
	if err := a.service.Logout(ctx, token); err != nil {
		response.Fail(ctx, c, err)
		return
	}
	response.Success(ctx, c, nil)
}
