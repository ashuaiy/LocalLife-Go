package response

import (
	"context"

	"github.com/ashuaiy/local-life-go/pkg/apperror"
	"github.com/ashuaiy/local-life-go/pkg/requestid"
	"github.com/cloudwego/hertz/pkg/app"
)

type Envelope struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Data      any    `json:"data"`
	RequestID string `json:"request_id"`
}

const errorKey = "locallife.error_code"

func Success(ctx context.Context, c *app.RequestContext, data any) {
	c.JSON(200, Envelope{Code: "ok", Message: "success", Data: data, RequestID: requestid.FromContext(ctx)})
}

func Fail(ctx context.Context, c *app.RequestContext, err error) {
	status, code, message := apperror.Describe(err)
	c.Set(errorKey, code)
	c.JSON(status, Envelope{Code: code, Message: message, Data: nil, RequestID: requestid.FromContext(ctx)})
}

func ErrorCode(c *app.RequestContext) string { return c.GetString(errorKey) }
