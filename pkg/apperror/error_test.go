package apperror

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestDescribeWrappedErrors(t *testing.T) {
	for _, tc := range []struct {
		kind   Kind
		status int
	}{
		{Validation, 400}, {Unauthorized, 401}, {NotFound, 404}, {Conflict, 409}, {RateLimited, 429},
		{Dependency, 503}, {Internal, 500}, {MethodNotAllowed, 405},
		{Kind("activity_not_started"), 409}, {Kind("activity_ended"), 409},
		{Kind("sold_out"), 409}, {Kind("already_purchased"), 409},
	} {
		t.Run(string(tc.kind), func(t *testing.T) {
			cause := errors.New("password=private")
			err := fmt.Errorf("service: %w", New(tc.kind, cause))
			status, code, message := Describe(err)
			if status != tc.status || code != string(tc.kind) || message == "" || message == cause.Error() {
				t.Fatalf("bad mapping: %d %s %s", status, code, message)
			}
			if !errors.Is(err, cause) {
				t.Fatal("underlying error lost")
			}
		})
	}
}

func TestDescribeUnknownErrorsAndTimeouts(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{errors.New("secret"), 500, "internal"}, {New(Kind("unknown"), nil), 500, "internal"},
		{fmt.Errorf("db: %w", context.DeadlineExceeded), 504, "timeout"},
		{context.Canceled, 408, "request_canceled"},
	} {
		status, code, message := Describe(tc.err)
		if status != tc.status || code != tc.code || message == "secret" {
			t.Fatalf("bad error mapping: %d %q %q", status, code, message)
		}
	}
}
