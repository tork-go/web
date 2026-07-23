package tork_test

import (
	"context"
	"errors"
	"net/http"
	"reflect"
	"strings"
	"testing"

	"github.com/tork-go/web"
)

func TestResponseWritesStatusHeadersAndBody(t *testing.T) {
	app := newApp()
	app.POST("/items", func(context.Context) (tork.Response[greeting], error) {
		// Chaining WithLocation into WithHeader exercises cloning an
		// already-populated header map, not just starting one from nil.
		return tork.Respond(http.StatusCreated, greeting{Message: "hi"}).
			WithLocation("/items/1").
			WithHeader("X-Trace", "abc"), nil
	})

	rec := do(t, app, "POST", "/items", nil)
	if rec.Code != http.StatusCreated {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Header().Get("Location"); got != "/items/1" {
		t.Errorf("location = %q", got)
	}
	if got := rec.Header().Get("X-Trace"); got != "abc" {
		t.Errorf("X-Trace = %q", got)
	}
	if got := rec.Body.String(); got != `{"message":"hi"}` {
		t.Errorf("body = %s", got)
	}
}

// Status left at its zero value answers exactly like a plain T would, so a
// handler that only wants headers never has to spell out the status too.
func TestResponseDefaultsToStatus200(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.Response[greeting], error) {
		return tork.Response[greeting]{Body: greeting{Message: "hi"}}, nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Body.String() != `{"message":"hi"}` {
		t.Errorf("body = %s", rec.Body)
	}
}

// The struct literal is the other of the two ways to build a Response: no
// constructor needed when there is nothing to infer.
func TestResponseStructLiteralSetsHeaders(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.Response[greeting], error) {
		return tork.Response[greeting]{
			Status:  http.StatusAccepted,
			Body:    greeting{Message: "hi"},
			Headers: http.Header{"X-Job": {"queued"}},
		}, nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Job"); got != "queued" {
		t.Errorf("X-Job = %q", got)
	}
}

// Two chains built from the same base must not share a header map: WithHeader
// clones rather than writing in place, or the second chain built would
// silently overwrite what the first one set.
func TestResponseWithHeaderDoesNotMutateASharedBase(t *testing.T) {
	base := tork.Respond(http.StatusCreated, greeting{Message: "hi"})
	a := base.WithHeader("X-Branch", "a")
	b := base.WithHeader("X-Branch", "b")

	if got := a.Headers.Get("X-Branch"); got != "a" {
		t.Errorf("a's header = %q", got)
	}
	if got := b.Headers.Get("X-Branch"); got != "b" {
		t.Errorf("b's header = %q", got)
	}
	if base.Headers != nil {
		t.Errorf("base gained a header map of its own: %v", base.Headers)
	}
}

// A body that cannot be marshalled fails before anything is written, exactly
// like the plain-T path, so the client gets a clean 500 rather than a 200
// with no body.
func TestUnencodableResponseBodyBecomesACleanInternalError(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.Response[chan int], error) {
		return tork.Respond(http.StatusCreated, make(chan int)), nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %q", e.Code)
	}
}

// Route.ResponseSpec is what the OpenAPI phase will read; it must exist for a
// Responder and say nothing for a plain T.
func TestRouteResponseSpecIsRecordedForAResponder(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.Response[greeting], error) {
		return tork.Respond(http.StatusCreated, greeting{}), nil
	})

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	spec := routes[0].ResponseSpec
	if spec == nil {
		t.Fatal("ResponseSpec is nil")
	}
	if spec.Status != http.StatusOK {
		t.Errorf("status = %d, want the documented default 200", spec.Status)
	}
	if spec.ContentType != "application/json" {
		t.Errorf("content type = %q", spec.ContentType)
	}
	if spec.BodyType != reflect.TypeFor[greeting]() {
		t.Errorf("body type = %v", spec.BodyType)
	}
}

func TestRouteResponseSpecIsNilForAPlainResult(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if routes[0].ResponseSpec != nil {
		t.Errorf("ResponseSpec = %+v, want nil", routes[0].ResponseSpec)
	}
}

// teapot is a Responder defined outside the package, proving the contract
// is usable by an application rather than only by tork's own types.
type teapot struct {
	message string
}

func (t teapot) Spec() tork.ResponseSpec {
	return tork.ResponseSpec{Status: http.StatusTeapot, ContentType: "text/plain"}
}

func (t teapot) WriteResponse(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusTeapot)
	_, err := w.Write([]byte(t.message))
	return err
}

func TestCustomResponderIsUsableFromOutsideThePackage(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (teapot, error) {
		return teapot{message: "short and stout"}, nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusTeapot {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Body.String() != "short and stout" {
		t.Errorf("body = %s", rec.Body)
	}
}

// A Responder whose methods are declared on the pointer, returned by value,
// implements nothing the framework can find — cannot supply would say so
// truthfully but not point at the fix, so this gets its own message instead.
type pointerOnlyResponder struct{}

func (*pointerOnlyResponder) Spec() tork.ResponseSpec {
	return tork.ResponseSpec{Status: http.StatusOK}
}

func (*pointerOnlyResponder) WriteResponse(http.ResponseWriter, *http.Request) error {
	return nil
}

func TestResponderOnlyOnThePointerIsABuildErrorNamingTheFix(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (pointerOnlyResponder, error) {
		return pointerOnlyResponder{}, nil
	})

	msg := buildError(t, app)
	want := "implements tork.Responder on *tork_test.pointerOnlyResponder, not tork_test.pointerOnlyResponder; return a pointer"
	if !strings.Contains(msg, want) {
		t.Errorf("error = %q, want it to contain %q", msg, want)
	}
}

// partialResponder writes a partial body and then fails, standing in for a
// Responder that has already sent something by the time it errors — the
// tracker must recognise this as begun and only log it, not attempt a
// second, conflicting response.
type partialResponder struct{}

func (partialResponder) Spec() tork.ResponseSpec {
	return tork.ResponseSpec{Status: http.StatusOK, ContentType: "text/plain"}
}

func (partialResponder) WriteResponse(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("partial")); err != nil {
		return err
	}
	return errors.New("failed after writing")
}

// A Responder that fails after it has already written something is only
// logged: the status line is already on the wire, so answering again would
// either be rejected by net/http or corrupt what was already sent.
func TestResponderFailingAfterItHasWrittenIsOnlyLogged(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (partialResponder, error) {
		return partialResponder{}, nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the status the Responder itself sent", rec.Code)
	}
	if rec.Body.String() != "partial" {
		t.Errorf("body = %q, want only what was written before the failure", rec.Body.String())
	}
}
