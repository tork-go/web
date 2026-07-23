package tork_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tork-go/web"
)

func TestHandlerSignaturesRefused(t *testing.T) {
	tests := []struct {
		name    string
		handler any
		want    string
	}{
		{
			name:    "nil",
			handler: nil,
			want:    "handler is nil",
		},
		{
			name:    "not a function",
			handler: "hello",
			want:    "handler is a string, not a function",
		},
		{
			name:    "variadic",
			handler: func(context.Context, ...string) (greeting, error) { return greeting{}, nil },
			want:    "handler is variadic",
		},
		{
			name:    "unsupported parameter",
			handler: func(context.Context, int) (greeting, error) { return greeting{}, nil },
			want:    "parameter 1: nothing provides int",
		},
		{
			name:    "no results",
			handler: func(context.Context) {},
			want:    "returns 0 values; a handler returns (T, error) or error",
		},
		{
			name:    "three results",
			handler: func(context.Context) (greeting, greeting, error) { return greeting{}, greeting{}, nil },
			want:    "returns 3 values; a handler returns (T, error) or error",
		},
		{
			name:    "one non-error result",
			handler: func(context.Context) greeting { return greeting{} },
			want:    "returns tork_test.greeting alone; a handler returns (T, error) or error",
		},
		{
			name:    "second result is not an error",
			handler: func(context.Context) (greeting, string) { return greeting{}, "" },
			want:    "returns (tork_test.greeting, string); the second result must be error",
		},
		{
			name:    "two errors",
			handler: func(context.Context) (error, error) { return nil, nil },
			want:    "returns (error, error); a handler returns (T, error) or error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newApp()
			app.GET("/", tt.handler, tork.OperationID("under.test"))

			if msg := buildError(t, app); !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q, want it to contain %q", msg, tt.want)
			}
		})
	}
}

func TestHandlerWithNoParametersIsAccepted(t *testing.T) {
	app := newApp()
	app.GET("/", func() (greeting, error) { return greeting{Message: "hi"}, nil })

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != 200 || rec.Body.String() != `{"message":"hi"}` {
		t.Errorf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestHandlerReceivesTheRequestContext(t *testing.T) {
	type key struct{}

	app := newApp(tork.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), key{}, "carried")))
		})
	}))
	app.GET("/", func(ctx context.Context) (greeting, error) {
		value, _ := ctx.Value(key{}).(string)
		return greeting{Message: value}, nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Body.String() != `{"message":"carried"}` {
		t.Errorf("body = %s", rec.Body)
	}
}
