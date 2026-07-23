package tork_test

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/tork-go/web"
)

// Principal is a request-scoped value a guard or an authenticator produces.
type Principal struct{ name string }

// authenticate reads a token from a header and turns it into a Principal, or
// refuses the request — the (T, error) form of a dependency.
type AuthInput struct {
	Token string `header:"X-Token"`
}

func authenticate(_ context.Context, in AuthInput) (Principal, error) {
	if in.Token == "" {
		return Principal{}, tork.Unauthorized("a token is required")
	}
	return Principal{name: in.Token}, nil
}

// alwaysAuthenticated is the same value without the possibility of failure, so
// tests that are not about refusal do not have to send a header.
func alwaysAuthenticated(context.Context) (Principal, error) {
	return Principal{name: "bob"}, nil
}

func greetPrincipal(_ context.Context, p Principal) (greeting, error) {
	return greeting{Message: p.name}, nil
}

func TestDependencyProvidesAValueFromAHeader(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(authenticate))
	items.GET("/", greetPrincipal)
	app := newApp()
	app.Include(items)

	handler := handlerOf(t, app)

	req := httptest.NewRequest("GET", "/items", nil)
	req.Header.Set("X-Token", "alice")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Body.String() != `{"message":"alice"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// A dependency that returns an error stops the request, and that error travels
// through the envelope exactly as a handler's would.
func TestDependencyErrorIsServedThroughTheEnvelope(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(authenticate))
	items.GET("/", greetPrincipal)
	app := newApp()
	app.Include(items)

	rec := do(t, app, "GET", "/items", nil) // no token
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != "UNAUTHORIZED" {
		t.Errorf("code = %q", e.Code)
	}
}

// A guard returns only an error: it runs for its effect and refuses the
// request without contributing a value, and the handler never runs.
func TestGuardStopsBeforeTheHandler(t *testing.T) {
	var handlerRan bool
	guard := func(context.Context) error { return tork.Forbidden("no entry") }

	items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(guard))
	items.GET("/", func(context.Context) (greeting, error) {
		handlerRan = true
		return greeting{Message: "hi"}, nil
	})
	app := newApp()
	app.Include(items)

	rec := do(t, app, "GET", "/items", nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d", rec.Code)
	}
	if handlerRan {
		t.Error("the handler ran despite the guard refusing")
	}
}

// A dependency taking a value form without an error is injected like any other.
func TestDependencyValueFormWithoutError(t *testing.T) {
	dep := func(context.Context) Principal { return Principal{name: "v"} }

	items := tork.NewRouter(tork.Prefix("/x"), tork.Depends(dep))
	items.GET("/", greetPrincipal)
	app := newApp()
	app.Include(items)

	if rec := do(t, app, "GET", "/x", nil); rec.Body.String() != `{"message":"v"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// A dependency may take its own input struct, and a field problem in it is
// reported the way any other rejected input is.
type CountInput struct {
	Count int `header:"X-Count"`
}

func TestDependencyInputFieldErrorIsReported(t *testing.T) {
	dep := func(_ context.Context, in CountInput) (Principal, error) {
		return Principal{name: fmt.Sprint(in.Count)}, nil
	}
	items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(dep))
	items.GET("/", greetPrincipal)
	app := newApp()
	app.Include(items)

	req := httptest.NewRequest("GET", "/items", nil)
	req.Header.Set("X-Count", "notanumber")
	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}
	fes := decodeFieldErrors(t, rec)
	if len(fes) != 1 || fes[0].Field != "header.X-Count" || fes[0].Issue != "invalid_integer" {
		t.Errorf("field errors = %+v", fes)
	}
}

// A dependency reading a body of the wrong media type fails the request there,
// the same hard failure a handler's body would.
type NoteBody struct {
	tork.JSONBody
	Text string `json:"text"`
}

func TestDependencyBodyMediaTypeErrorStopsTheRequest(t *testing.T) {
	dep := func(_ context.Context, b NoteBody) (Principal, error) {
		return Principal{name: b.Text}, nil
	}
	items := tork.NewRouter(tork.Prefix("/notes"), tork.Depends(dep))
	items.POST("/", func(context.Context) (greeting, error) { return greeting{}, nil })
	app := newApp()
	app.Include(items)

	rec := post(t, app, "POST", "/notes", "text/plain", "hello")
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Errorf("status = %d", rec.Code)
	}
}

// A dependency can consume what an outer one produced: Session is built from
// the Principal the authenticator provided.
type Session struct{ principal Principal }

func openSession(_ context.Context, p Principal) (Session, error) {
	return Session{principal: p}, nil
}

func TestNestedDependencies(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"),
		tork.Depends(alwaysAuthenticated), // provides Principal
		tork.Depends(openSession),         // consumes Principal, provides Session
	)
	items.GET("/", func(_ context.Context, s Session) (greeting, error) {
		return greeting{Message: s.principal.name}, nil
	})
	app := newApp()
	app.Include(items)

	if rec := do(t, app, "GET", "/items", nil); rec.Body.String() != `{"message":"bob"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// One dependency declared on a router serves every route under it.
func TestDependencyUsedByTwoHandlers(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(alwaysAuthenticated))
	items.GET("/a", func(_ context.Context, p Principal) (greeting, error) {
		return greeting{Message: "a:" + p.name}, nil
	})
	items.GET("/b", func(_ context.Context, p Principal) (greeting, error) {
		return greeting{Message: "b:" + p.name}, nil
	})
	app := newApp()
	app.Include(items)

	handler := handlerOf(t, app)
	for path, want := range map[string]string{"/items/a": `{"message":"a:bob"}`, "/items/b": `{"message":"b:bob"}`} {
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, httptest.NewRequest("GET", path, nil))
		if rec.Body.String() != want {
			t.Errorf("%s body = %s, want %s", path, rec.Body, want)
		}
	}
}

// Dependencies declared further out run first, so an inner one can rely on
// what an outer one established.
func TestDependenciesRunOutermostFirst(t *testing.T) {
	var order []string
	record := func(name string) func(context.Context) error {
		return func(context.Context) error {
			order = append(order, name)
			return nil
		}
	}

	app := newApp(tork.Depends(record("app")))
	items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(record("router")))
	items.GET("/", hello, tork.Depends(record("route")))
	app.Include(items)

	do(t, app, "GET", "/items", nil)
	if !equalStrings(order, []string{"app", "router", "route"}) {
		t.Errorf("order = %v, want [app router route]", order)
	}
}

// A request-scoped value is built once per request and shared by every reader
// within it: the id counts up one per request, and the flag a dependency set
// on the value is visible to the handler.
type SessionScope struct {
	id   int
	seen bool
}

func TestRequestScopedValueIsBuiltOncePerRequestAndShared(t *testing.T) {
	var builds int32
	newSession := func(context.Context) (*SessionScope, error) {
		return &SessionScope{id: int(atomic.AddInt32(&builds, 1))}, nil
	}
	recordSession := func(_ context.Context, s *SessionScope) error {
		s.seen = true
		return nil
	}

	items := tork.NewRouter(tork.Prefix("/x"), tork.Depends(newSession), tork.Depends(recordSession))
	items.GET("/", func(_ context.Context, s *SessionScope) (greeting, error) {
		return greeting{Message: fmt.Sprintf("%d-%v", s.id, s.seen)}, nil
	})
	app := newApp()
	app.Include(items)

	if rec := do(t, app, "GET", "/x", nil); rec.Body.String() != `{"message":"1-true"}` {
		t.Errorf("first request body = %s", rec.Body)
	}
	if rec := do(t, app, "GET", "/x", nil); rec.Body.String() != `{"message":"2-true"}` {
		t.Errorf("second request body = %s", rec.Body)
	}
}

// A dependency's cleanup runs after the response, in reverse of the order the
// dependencies registered them, even when the handler failed.
func TestRequestCleanupsRunLIFOEvenWhenHandlerFails(t *testing.T) {
	var order []string
	depA := func(context.Context) (Principal, tork.Cleanup, error) {
		return Principal{}, func(context.Context) error { order = append(order, "a"); return nil }, nil
	}
	depB := func(context.Context) (Session, tork.Cleanup, error) {
		return Session{}, func(context.Context) error { order = append(order, "b"); return nil }, nil
	}

	items := tork.NewRouter(tork.Prefix("/x"), tork.Depends(depA), tork.Depends(depB))
	items.GET("/", func(context.Context) (greeting, error) {
		return greeting{}, tork.Internal()
	})
	app := newApp()
	app.Include(items)

	rec := do(t, app, "GET", "/x", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if !equalStrings(order, []string{"b", "a"}) {
		t.Errorf("cleanup order = %v, want [b a]", order)
	}
}

// A request cleanup that fails is logged, not served: the response is already
// decided by the time it runs, so it cannot change it.
func TestRequestCleanupFailureDoesNotChangeTheResponse(t *testing.T) {
	dep := func(context.Context) (Principal, tork.Cleanup, error) {
		return Principal{name: "p"}, func(context.Context) error {
			return errors.New("cleanup boom")
		}, nil
	}
	items := tork.NewRouter(tork.Prefix("/x"), tork.Depends(dep))
	items.GET("/", greetPrincipal)
	app := newApp()
	app.Include(items)

	rec := do(t, app, "GET", "/x", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != `{"message":"p"}` {
		t.Errorf("status %d body %s — a cleanup failure must not change the response", rec.Code, rec.Body)
	}
}

// A cleanup-form dependency may return no cleanup, which is not the same as
// registering one.
func TestDependencyCleanupFormWithNoCleanup(t *testing.T) {
	dep := func(context.Context) (Principal, tork.Cleanup, error) {
		return Principal{name: "nc"}, nil, nil
	}
	items := tork.NewRouter(tork.Prefix("/x"), tork.Depends(dep))
	items.GET("/", greetPrincipal)
	app := newApp()
	app.Include(items)

	if rec := do(t, app, "GET", "/x", nil); rec.Body.String() != `{"message":"nc"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// Every way a dependency can be the wrong shape names what is wrong with it.
func TestDependencyShapesRefused(t *testing.T) {
	tests := []struct {
		name string
		opt  tork.Option
		want string
	}{
		{"nil", tork.Depends(nil), "a dependency is nil, not a function"},
		{"not a function", tork.Depends(42), "a dependency is a int, not a function"},
		{"variadic", tork.Depends(func(...int) error { return nil }), "a dependency is variadic"},
		{"no results", tork.Depends(func(context.Context) {}), "a dependency returns 0 values"},
		{"second not error", tork.Depends(func(context.Context) (Principal, string) { return Principal{}, "" }), "the second result of a dependency must be error, not string"},
		{"middle not cleanup", tork.Depends(func(context.Context) (Principal, string, error) { return Principal{}, "", nil }), "the second result of a three-result dependency must be tork.Cleanup, not string"},
		{"third not error", tork.Depends(func(context.Context) (Principal, tork.Cleanup, string) { return Principal{}, nil, "" }), "the third result of a dependency must be error, not string"},
		{"four results", tork.Depends(func(context.Context) (int, int, int, int) { return 0, 0, 0, 0 }), "a dependency returns 4 values"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newApp()
			app.GET("/", hello, tt.opt)

			if msg := buildError(t, app); !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q, want it to contain %q", msg, tt.want)
			}
		})
	}
}

// A dependency parameter nothing provides is reported by the type it needs,
// the same as a handler's.
func TestDependencyParameterNothingProvides(t *testing.T) {
	dep := func(_ context.Context, r *Repository) (Principal, error) {
		return Principal{name: r.name}, nil
	}
	app := newApp()
	app.GET("/", greetPrincipal, tork.Depends(dep))

	if msg := buildError(t, app); !strings.Contains(msg, "a dependency's parameter 1: nothing provides *tork_test.Repository") {
		t.Errorf("error = %q", msg)
	}
}

// A dependency may draw on a singleton, which is how a request-scoped value is
// built from an application-wide service.
func TestDependencyCanUseASingleton(t *testing.T) {
	dep := func(_ context.Context, repo *Repository) (Principal, error) {
		return Principal{name: repo.name}, nil
	}
	app := newApp(tork.Provide(newRepository))
	app.GET("/", greetPrincipal, tork.Depends(dep))

	if rec := do(t, app, "GET", "/", nil); rec.Body.String() != `{"message":"repo"}` {
		t.Errorf("body = %s", rec.Body)
	}
}
