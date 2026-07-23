package tork_test

import (
	"log/slog"
	"net/http"
	"strings"
	"testing"

	"github.com/tork-go/web"
)

// An option used where it does not belong is a mistake in the caller's
// source, and is reported with the line it was written on.
func TestOptionsRejectedOutsideTheirScope(t *testing.T) {
	t.Run("app-only option on a router", func(t *testing.T) {
		router := tork.NewRouter(tork.Title("not the API"))
		router.GET("/", hello)

		app := newApp()
		app.Include(router)

		msg := buildError(t, app)
		if !strings.Contains(msg, "tork.Title is not valid on a router") {
			t.Errorf("error = %q", msg)
		}
		if !strings.Contains(msg, "tests/option_test.go:") {
			t.Errorf("error does not name the line it was written on: %q", msg)
		}
	})

	t.Run("route-only option on a router", func(t *testing.T) {
		router := tork.NewRouter(tork.Summary("not an operation"))
		router.GET("/", hello)

		app := newApp()
		app.Include(router)

		if msg := buildError(t, app); !strings.Contains(msg, "tork.Summary is not valid on a router") {
			t.Errorf("error = %q", msg)
		}
	})

	t.Run("router-only option on a route", func(t *testing.T) {
		app := newApp()
		app.GET("/", hello, tork.Prefix("/nope"))

		if msg := buildError(t, app); !strings.Contains(msg, "tork.Prefix is not valid on a route") {
			t.Errorf("error = %q", msg)
		}
	})

	t.Run("route-only option on the application", func(t *testing.T) {
		app := newApp(tork.OperationID("nope"))
		app.GET("/", hello)

		if msg := buildError(t, app); !strings.Contains(msg, "tork.OperationID is not valid on an application") {
			t.Errorf("error = %q", msg)
		}
	})

	t.Run("app-only option on a route", func(t *testing.T) {
		app := newApp()
		app.GET("/", hello, tork.Logger(slog.Default()))

		if msg := buildError(t, app); !strings.Contains(msg, "tork.Logger is not valid on a route") {
			t.Errorf("error = %q", msg)
		}
	})
}

// Every misplaced option is reported, not just the first, so one build fixes
// one round of mistakes.
func TestEveryBadOptionIsReportedAtOnce(t *testing.T) {
	app := newApp()
	app.GET("/", hello, tork.Prefix("/a"), tork.Title("b"))

	msg := buildError(t, app)
	if !strings.Contains(msg, "tork.Prefix is not valid") || !strings.Contains(msg, "tork.Title is not valid") {
		t.Errorf("error = %q", msg)
	}
}

func TestOptionsRejectNilAndEmptyArguments(t *testing.T) {
	tests := []struct {
		name string
		opt  tork.Option
		want string
	}{
		{"Clock", tork.Clock(nil), "tork.Clock: clock must not be nil"},
		{"Logger", tork.Logger(nil), "tork.Logger: logger must not be nil"},
		{"OnError", tork.OnError(nil), "tork.OnError: mapper must not be nil"},
		{"WriteErrorsWith", tork.WriteErrorsWith(nil), "tork.WriteErrorsWith: writer must not be nil"},
		{"Use", tork.Use(nil), "tork.Use: middleware 0 is nil"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := tork.New(tt.opt)
			app.GET("/", hello)

			if msg := buildError(t, app); !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q, want it to contain %q", msg, tt.want)
			}
		})
	}
}

func TestEmptyOperationIDIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/", hello, tork.OperationID(""))

	if msg := buildError(t, app); !strings.Contains(msg, "operation ID must not be empty") {
		t.Errorf("error = %q", msg)
	}
}

func TestUseRejectsANilMiddlewareAmongGoodOnes(t *testing.T) {
	passthrough := func(next http.Handler) http.Handler { return next }

	app := newApp(tork.Use(passthrough, nil))
	app.GET("/", hello)

	if msg := buildError(t, app); !strings.Contains(msg, "middleware 1 is nil") {
		t.Errorf("error = %q", msg)
	}
}
