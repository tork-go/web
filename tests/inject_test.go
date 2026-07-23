package tork_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tork-go/web"
)

// Repository and Greeter are a two-link chain: a Greeter is built from a
// Repository, which is built from nothing.
type Repository struct{ name string }

type Greeter struct{ repo *Repository }

func newRepository() *Repository        { return &Repository{name: "repo"} }
func newGreeter(r *Repository) *Greeter { return &Greeter{repo: r} }

// greetFromRepo and greetFromChain return an injected value's name, which is
// how a served request proves what was wired into it.
func greetFromRepo(_ context.Context, r *Repository) (greeting, error) {
	return greeting{Message: r.name}, nil
}

func greetFromChain(_ context.Context, g *Greeter) (greeting, error) {
	return greeting{Message: g.repo.name}, nil
}

func TestProvideResolvesAChain(t *testing.T) {
	app := newApp(tork.Provide(newRepository, newGreeter))
	app.GET("/", greetFromChain)

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	if rec.Body.String() != `{"message":"repo"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// A constructor written in the (T, error) form that does not fail is injected
// like any other.
func TestProviderWithErrorFormThatSucceeds(t *testing.T) {
	app := newApp(tork.Provide(func() (*Repository, error) {
		return &Repository{name: "ok"}, nil
	}))
	app.GET("/", greetFromRepo)

	if rec := do(t, app, "GET", "/", nil); rec.Body.String() != `{"message":"ok"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// A constructor written in the cleanup form may return no cleanup at all,
// which is not the same as failing.
func TestProviderWithCleanupFormReturningNoCleanup(t *testing.T) {
	app := newApp(tork.Provide(func() (*Repository, tork.Cleanup, error) {
		return &Repository{name: "nocleanup"}, nil, nil
	}))
	app.GET("/", greetFromRepo)

	if rec := do(t, app, "GET", "/", nil); rec.Body.String() != `{"message":"nocleanup"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// A cleanup-form constructor may also fail, and its error stops the build the
// same way any other constructor's does.
func TestCleanupFormConstructorCanFail(t *testing.T) {
	app := newApp(tork.Provide(func() (*Repository, tork.Cleanup, error) {
		return nil, nil, errors.New("repo unavailable")
	}))
	app.GET("/", greetFromRepo)

	_, err := app.Handler()
	if err == nil || !strings.Contains(err.Error(), "repo unavailable") {
		t.Fatalf("err = %v", err)
	}
}

// Store sits at the bottom of a diamond: Left and Right both need it, and Top
// needs both. It must be built once and shared, not once per path to it.
type Store struct{}
type Left struct{ s *Store }
type Right struct{ s *Store }
type Top struct {
	l *Left
	r *Right
}

func newLeft(s *Store) *Left        { return &Left{s: s} }
func newRight(s *Store) *Right      { return &Right{s: s} }
func newTop(l *Left, r *Right) *Top { return &Top{l: l, r: r} }
func fromDiamond(_ context.Context, top *Top) (greeting, error) {
	if top.l.s == top.r.s {
		return greeting{Message: "shared"}, nil
	}
	return greeting{Message: "distinct"}, nil
}

func TestProvideBuildsADiamondNodeOnce(t *testing.T) {
	var storeBuilds int32
	newStore := func() *Store {
		atomic.AddInt32(&storeBuilds, 1)
		return &Store{}
	}

	app := newApp(tork.Provide(newStore, newLeft, newRight, newTop))
	app.GET("/", fromDiamond)

	if rec := do(t, app, "GET", "/", nil); rec.Body.String() != `{"message":"shared"}` {
		t.Errorf("body = %s", rec.Body)
	}
	if storeBuilds != 1 {
		t.Errorf("store built %d times, want 1", storeBuilds)
	}
}

// A prebuilt value is injected as a singleton of its own type without a
// constructor.
type Config struct{ Name string }

func TestProvideValueInjectsAPrebuiltValue(t *testing.T) {
	app := newApp(tork.ProvideValue(Config{Name: "prod"}))
	app.GET("/", func(_ context.Context, c Config) (greeting, error) {
		return greeting{Message: c.Name}, nil
	})

	if rec := do(t, app, "GET", "/", nil); rec.Body.String() != `{"message":"prod"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// A singleton is constructed once, and reading it from many requests at once is
// race-free — this test is meant to be run under -race.
func TestSingletonIsBuiltOnceUnderConcurrentRequests(t *testing.T) {
	var builds int32
	app := newApp(tork.Provide(func() *Repository {
		atomic.AddInt32(&builds, 1)
		return &Repository{name: "once"}
	}))
	app.GET("/", greetFromRepo)

	handler := handlerOf(t, app)

	var wg sync.WaitGroup
	for range 50 {
		wg.Go(func() {
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))
			if rec.Body.String() != `{"message":"once"}` {
				t.Errorf("body = %s", rec.Body)
			}
		})
	}
	wg.Wait()

	if builds != 1 {
		t.Errorf("built %d times, want 1", builds)
	}
}

// Routes lists the route table without running any constructor, so describing
// an API never opens a database; Handler is what constructs.
func TestRoutesDoesNotConstructSingletons(t *testing.T) {
	var builds int32
	app := newApp(tork.Provide(func() *Repository {
		atomic.AddInt32(&builds, 1)
		return &Repository{}
	}))
	app.GET("/", greetFromRepo)

	if _, err := app.Routes(); err != nil {
		t.Fatalf("Routes: %v", err)
	}
	if builds != 0 {
		t.Errorf("Routes constructed %d singletons, want 0", builds)
	}

	if _, err := app.Handler(); err != nil {
		t.Fatalf("Handler: %v", err)
	}
	if builds != 1 {
		t.Errorf("Handler constructed %d singletons, want 1", builds)
	}
}

// A constructor that fails stops the build, and the value already built above
// it is cleaned up rather than leaked.
func TestConstructionFailureUnwindsPartials(t *testing.T) {
	var closed bool
	app := newApp(tork.Provide(
		func() (*Repository, tork.Cleanup, error) {
			return &Repository{}, func(context.Context) error { closed = true; return nil }, nil
		},
		func(*Repository) (*Greeter, error) {
			return nil, errors.New("greeter unavailable")
		},
	))
	app.GET("/", greetFromChain)

	_, err := app.Handler()
	if err == nil || !strings.Contains(err.Error(), "building") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(err.Error(), "greeter unavailable") {
		t.Errorf("err does not wrap the cause: %v", err)
	}
	if !closed {
		t.Error("the first provider's cleanup did not run during unwind")
	}
}

// Singleton cleanups run in reverse of construction, and every one runs even
// when an earlier one fails.
func TestSingletonCleanupsRunLIFOOnClose(t *testing.T) {
	var order []string
	app := newApp(tork.Provide(
		func() (*Repository, tork.Cleanup, error) {
			return &Repository{}, func(context.Context) error {
				order = append(order, "repo")
				return nil
			}, nil
		},
		func(*Repository) (*Greeter, tork.Cleanup, error) {
			return &Greeter{}, func(context.Context) error {
				order = append(order, "greeter")
				return errors.New("greeter close failed")
			}, nil
		},
	))
	app.GET("/", func(context.Context, *Greeter) (greeting, error) { return greeting{}, nil })

	if _, err := app.Handler(); err != nil {
		t.Fatalf("build: %v", err)
	}

	err := app.Close(context.Background())
	if !equalStrings(order, []string{"greeter", "repo"}) {
		t.Errorf("cleanup order = %v, want [greeter repo]", order)
	}
	if err == nil || !strings.Contains(err.Error(), "greeter close failed") {
		t.Errorf("Close error = %v", err)
	}
}

// An application that never built has nothing to close.
func TestCloseWithoutABuildIsANoOp(t *testing.T) {
	if err := newApp().Close(context.Background()); err != nil {
		t.Errorf("Close = %v", err)
	}
}

// Serving wires shutdown to Close, so a singleton's cleanup runs when the
// server stops.
func TestServeClosesSingletonsOnShutdown(t *testing.T) {
	var closed int32
	app := newApp(tork.Provide(func() (*Repository, tork.Cleanup, error) {
		return &Repository{name: "x"}, func(context.Context) error {
			atomic.AddInt32(&closed, 1)
			return nil
		}, nil
	}))
	app.GET("/", greetFromRepo)

	address := listenAddress(t)
	ctx, stop := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Serve(ctx, address) }()

	getWhenReady(t, "http://"+address+"/")
	stop()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return")
	}

	if closed != 1 {
		t.Errorf("singleton closed %d times, want 1", closed)
	}
}

// Every way a provider can be the wrong shape names what is wrong with it.
func TestProviderShapesRefused(t *testing.T) {
	tests := []struct {
		name string
		opt  tork.Option
		want string
	}{
		{"nil", tork.Provide(nil), "a provider is nil, not a function"},
		{"not a function", tork.Provide(42), "a provider is a int, not a function"},
		{"variadic", tork.Provide(func(...int) *Repository { return nil }), "a provider is variadic"},
		{"error only", tork.Provide(func() error { return nil }), "a function returning only an error is a tork.Depends guard"},
		{"no results", tork.Provide(func() {}), "a provider returns 0 values"},
		{"second not error", tork.Provide(func() (*Repository, string) { return nil, "" }), "the second result of a provider must be error, not string"},
		{"middle not cleanup", tork.Provide(func() (*Repository, string, error) { return nil, "", nil }), "the second result of a three-result provider must be tork.Cleanup, not string"},
		{"third not error", tork.Provide(func() (*Repository, tork.Cleanup, string) { return nil, nil, "" }), "the third result of a provider must be error, not string"},
		{"four results", tork.Provide(func() (int, int, int, int) { return 0, 0, 0, 0 }), "a provider returns 4 values"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newApp(tt.opt)
			app.GET("/", hello)

			if msg := buildError(t, app); !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q, want it to contain %q", msg, tt.want)
			}
		})
	}
}

// Two providers for one type name each other, so the fix — delete one — is
// obvious from either line.
func TestDuplicateProviderNamesBothSites(t *testing.T) {
	app := newApp(
		tork.Provide(newRepository),
		tork.Provide(func() *Repository { return &Repository{} }),
	)
	app.GET("/", hello)

	if msg := buildError(t, app); !strings.Contains(msg, "a provider for *tork_test.Repository is already declared at") {
		t.Errorf("error = %q", msg)
	}
}

// A constructor parameter nothing provides is reported by the type it needs.
func TestMissingProviderIsReported(t *testing.T) {
	app := newApp(tork.Provide(newGreeter)) // needs *Repository, which is not provided
	app.GET("/", hello)

	if msg := buildError(t, app); !strings.Contains(msg,
		"the provider for *tork_test.Greeter needs *tork_test.Repository, which nothing provides") {
		t.Errorf("error = %q", msg)
	}
}

// ProbeInput is a request input, so a singleton that takes one is depending on
// the request — a scope violation.
type ProbeInput struct {
	Token string `header:"X-Token"`
}

func TestSingletonCannotDependOnTheRequest(t *testing.T) {
	tests := []struct {
		name string
		ctor any
		want string
	}{
		{"context", func(context.Context) *Repository { return nil }, "takes context.Context, which exists only per request"},
		{"request", func(*tork.Request) *Repository { return nil }, "takes *tork.Request, which exists only per request"},
		{"input struct", func(ProbeInput) *Repository { return nil }, "takes tork_test.ProbeInput, which exists only per request"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newApp(tork.Provide(tt.ctor))
			app.GET("/", hello)

			if msg := buildError(t, app); !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q, want it to contain %q", msg, tt.want)
			}
		})
	}
}

// A cycle is reported as the path that closes it, so a reader sees every type
// on the loop rather than only that one exists.
type NodeA struct{ b *NodeB }
type NodeB struct{ a *NodeA }

func newNodeA(b *NodeB) *NodeA { return &NodeA{b: b} }
func newNodeB(a *NodeA) *NodeB { return &NodeB{a: a} }

func TestProviderCycleIsReportedWithThePath(t *testing.T) {
	app := newApp(tork.Provide(newNodeA, newNodeB))
	app.GET("/", hello)

	msg := buildError(t, app)
	if !strings.Contains(msg, "providers form a cycle: *tork_test.NodeA -> *tork_test.NodeB -> *tork_test.NodeA") {
		t.Errorf("error = %q", msg)
	}
}
