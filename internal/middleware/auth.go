package middleware

import (
	"context"
	"strings"

	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/response"
	"github.com/cloudwego/hertz/pkg/app"
)

type Authenticator interface {
	Authenticate(context.Context, string) (uint64, error)
}
type userIDKey struct{}

func UserID(ctx context.Context) uint64 { id, _ := ctx.Value(userIDKey{}).(uint64); return id }

func BearerToken(header string) (string, bool) {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return "", false
	}
	return parts[1], true
}

func RequireAuth(auth Authenticator) app.HandlerFunc {
	return func(ctx context.Context, c *app.RequestContext) {
		token, ok := BearerToken(string(c.GetHeader("Authorization")))
		if !ok {
			response.Fail(ctx, c, apperror.New(apperror.Unauthorized, nil))
			c.Abort()
			return
		}
		id, err := auth.Authenticate(ctx, token)
		if err != nil {
			response.Fail(ctx, c, err)
			c.Abort()
			return
		}
		c.Next(context.WithValue(ctx, userIDKey{}, id))
	}
}
