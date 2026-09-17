// Package apperror defines errors independent of persistence and transport layers.
package apperror

import (
	"context"
	"errors"
)

type Kind string

const (
	Validation       Kind = "validation"
	Unauthorized     Kind = "unauthorized"
	NotFound         Kind = "not_found"
	Conflict         Kind = "conflict"
	RateLimited      Kind = "rate_limited"
	Dependency       Kind = "dependency_failure"
	Internal         Kind = "internal"
	MethodNotAllowed Kind = "method_not_allowed"
)

type Error struct {
	Kind  Kind
	Cause error
}

func New(kind Kind, cause error) *Error { return &Error{Kind: kind, Cause: cause} }
func (e *Error) Error() string          { return string(e.Kind) }
func (e *Error) Unwrap() error          { return e.Cause }

// Describe produces a safe public message. Driver errors and causes never escape.
func Describe(err error) (status int, code, message string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return 504, "timeout", "request timed out"
	}
	if errors.Is(err, context.Canceled) {
		return 408, "request_canceled", "request canceled"
	}
	var domainErr *Error
	if errors.As(err, &domainErr) {
		switch domainErr.Kind {
		case Validation:
			return 400, string(Validation), "invalid request"
		case Unauthorized:
			return 401, string(Unauthorized), "authentication required"
		case NotFound:
			return 404, string(NotFound), "resource not found"
		case Conflict:
			return 409, string(Conflict), "resource conflict"
		case RateLimited:
			return 429, string(RateLimited), "too many requests"
		case Dependency:
			return 503, string(Dependency), "dependency unavailable"
		case MethodNotAllowed:
			return 405, string(MethodNotAllowed), "method not allowed"
		}
	}
	return 500, string(Internal), "internal server error"
}
