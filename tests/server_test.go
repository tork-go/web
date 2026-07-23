package tork_test

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tork-go/web"
)

func TestSuccessIsJSONWithStatus200(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Body.String(); got != `{"message":"hello"}` {
		t.Errorf("body = %s", got)
	}
}

// A handler that returns only an error has nothing to say when it succeeds.
func TestErrorOnlyHandlerAnswers204(t *testing.T) {
	app := newApp()
	app.DELETE("/items/{item_id}", func(context.Context) error { return nil })

	rec := do(t, app, "DELETE", "/items/42", nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %s", rec.Body)
	}
}

func TestRootRouteMatchesOnlyTheRoot(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	if rec := do(t, app, "GET", "/", nil); rec.Code != http.StatusOK {
		t.Errorf("root status = %d", rec.Code)
	}
	// "/" is ServeMux's subtree pattern, so a framework that registered it
	// literally would answer this with the root handler.
	if rec := do(t, app, "GET", "/anything", nil); rec.Code != http.StatusNotFound {
		t.Errorf("unmatched path status = %d, want 404", rec.Code)
	}
}

func TestUnmatchedPathAnswersRouteNotFound(t *testing.T) {
	app := newApp()
	app.GET("/items", hello)

	rec := do(t, app, "GET", "/nope", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	e := decodeError(t, rec)
	if e.Code != "ROUTE_NOT_FOUND" {
		t.Errorf("code = %q", e.Code)
	}
	if e.Message != "No route matches GET /nope." {
		t.Errorf("message = %q", e.Message)
	}
	if e.Path != "/nope" || e.Status != 404 {
		t.Errorf("envelope = %+v", e)
	}
}

func TestKnownPathWithAnotherMethodAnswers405(t *testing.T) {
	app := newApp()
	app.GET("/items", hello)
	app.POST("/items", farewell, tork.OperationID("items.create"))

	rec := do(t, app, "DELETE", "/items", nil)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD, OPTIONS, POST" {
		t.Errorf("Allow = %q", got)
	}
	e := decodeError(t, rec)
	if e.Code != "METHOD_NOT_ALLOWED" {
		t.Errorf("code = %q", e.Code)
	}
	if !strings.Contains(e.Message, "DELETE is not allowed here") {
		t.Errorf("message = %q", e.Message)
	}
}

func TestOptionsIsAnsweredFromTheDeclaredMethods(t *testing.T) {
	app := newApp()
	app.GET("/items", hello)
	app.POST("/items", farewell, tork.OperationID("items.create"))

	rec := do(t, app, "OPTIONS", "/items", nil)
	if rec.Code != http.StatusNoContent {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Allow"); got != "GET, HEAD, OPTIONS, POST" {
		t.Errorf("Allow = %q", got)
	}
}

// A declared OPTIONS route is more specific than the method fallback, so it
// wins.
func TestDeclaredOptionsRouteWins(t *testing.T) {
	app := newApp()
	app.GET("/items", hello)
	app.OPTIONS("/items", farewell, tork.OperationID("items.options"))

	rec := do(t, app, "OPTIONS", "/items", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Body.String() != `{"message":"goodbye"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// ServeMux serves HEAD from a GET route, which is where this behaviour comes
// from; the test is here so a change to the registration cannot lose it.
func TestHeadIsServedByTheGetRoute(t *testing.T) {
	app := newApp()
	app.GET("/items", hello)

	rec := do(t, app, "HEAD", "/items", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestMiddlewareRunsOutermostFirst(t *testing.T) {
	var order []string
	record := func(name string) tork.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	items := tork.NewRouter(tork.Prefix("/items"), tork.Use(record("router")))
	items.GET("/", hello, tork.Use(record("route")))

	app := newApp(tork.Use(record("app")))
	app.Include(items)

	do(t, app, "GET", "/items", nil)
	if !equalStrings(order, []string{"app", "router", "route"}) {
		t.Errorf("order = %v", order)
	}
}

func TestMiddlewareDeclaredTogetherRunsInOrder(t *testing.T) {
	var order []string
	record := func(name string) tork.Middleware {
		return func(next http.Handler) http.Handler {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name)
				next.ServeHTTP(w, r)
			})
		}
	}

	app := newApp(tork.Use(record("first"), record("second")))
	app.GET("/", hello)

	do(t, app, "GET", "/", nil)
	if !equalStrings(order, []string{"first", "second"}) {
		t.Errorf("order = %v", order)
	}
}

func TestMiddlewareCanAnswerWithoutTheHandler(t *testing.T) {
	app := newApp(tork.Use(func(http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTeapot)
		})
	}))
	app.GET("/", hello)

	if rec := do(t, app, "GET", "/", nil); rec.Code != http.StatusTeapot {
		t.Errorf("status = %d", rec.Code)
	}
}

func TestPanickingHandlerBecomesAnInternalError(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (greeting, error) { panic("handler exploded") })

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	e := decodeError(t, rec)
	if e.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %q", e.Code)
	}
	if strings.Contains(e.Message, "exploded") {
		t.Errorf("the panic value leaked into the response: %q", e.Message)
	}
}

// net/http asks handlers to re-panic this one so the server can abort the
// connection silently, so it must pass straight through.
func TestAbortHandlerPanicIsNotSwallowed(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (greeting, error) { panic(http.ErrAbortHandler) })

	defer func() {
		if recovered := recover(); !errors.Is(recovered.(error), http.ErrAbortHandler) {
			t.Errorf("recovered %v, want http.ErrAbortHandler", recovered)
		}
	}()
	do(t, app, "GET", "/", nil)
	t.Error("expected the panic to propagate")
}

// A result that cannot be marshalled must fail cleanly: the body is written
// only after it encodes, so the client gets an error rather than a truncated
// success.
func TestUnencodableResultBecomesAnInternalError(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (chan int, error) { return make(chan int), nil })

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %q", e.Code)
	}
}

// ServeMux refuses two patterns that overlap without either being more
// specific, and says so by panicking; the build has to turn that into an
// error like any other.
func TestConflictingPatternsAreReportedAsAnError(t *testing.T) {
	app := newApp()
	app.GET("/items/{item_id}/parts/{part}", hello)
	app.GET("/items/{a}/parts/{b}", farewell)

	if msg := buildError(t, app); !strings.Contains(msg, "cannot register") {
		t.Errorf("error = %q", msg)
	}
}

// Two paths differing only in the names of their wildcards do not conflict as
// long as their methods differ — but the framework also registers each path
// without a method, to answer 405, and those always conflict.
func TestPathsThatDifferOnlyByWildcardNameAreRefused(t *testing.T) {
	app := newApp()
	app.GET("/items/{item_id}", hello)
	app.POST("/items/{id}", farewell)

	if msg := buildError(t, app); !strings.Contains(msg, "cannot register") {
		t.Errorf("error = %q", msg)
	}
}

// The application-level metadata is accepted where it belongs, and a build
// with no logger of its own still builds.
func TestApplicationMetadataIsAccepted(t *testing.T) {
	app := tork.New(
		tork.Title("Example API"),
		tork.Description("Example backend built with Tork"),
		tork.Clock(func() time.Time { return fixedTime }),
	)
	app.GET("/", hello)

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
}
