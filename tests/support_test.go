package tork_test

import (
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/tork-go/web"
)

// fixedTime is the moment every error body in these tests is stamped with, so
// a response can be compared whole rather than field by field.
var fixedTime = time.Date(2026, 7, 23, 16, 37, 23, 0, time.UTC)

// testOptions are what every application in these tests is built with: a
// clock that does not move, and a logger that does not write to the test's
// output. Both exist so that a failing assertion shows the response and
// nothing else.
func testOptions() []tork.Option {
	return []tork.Option{
		tork.Clock(func() time.Time { return fixedTime }),
		tork.Logger(slog.New(slog.NewTextHandler(io.Discard, nil))),
	}
}

// newApp builds an application with the test defaults and the given options.
func newApp(opts ...tork.Option) *tork.App {
	return tork.New(append(testOptions(), opts...)...)
}

// handlerOf builds the application and fails the test if it will not build.
func handlerOf(t *testing.T, app *tork.App) http.Handler {
	t.Helper()
	handler, err := app.Handler()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	return handler
}

// do sends one request to the application and returns the recorded response.
func do(t *testing.T, app *tork.App, method, target string, body io.Reader) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, httptest.NewRequest(method, target, body))
	return rec
}

// decodeError reads a response body as an error envelope.
func decodeError(t *testing.T, rec *httptest.ResponseRecorder) tork.Error {
	t.Helper()
	var e tork.Error
	if err := json.Unmarshal(rec.Body.Bytes(), &e); err != nil {
		t.Fatalf("decode error body %q: %v", rec.Body.String(), err)
	}
	return e
}

// buildError builds the application expecting it to fail, and returns the
// message, which is what most declaration tests assert on.
func buildError(t *testing.T, app *tork.App) string {
	t.Helper()
	if _, err := app.Handler(); err != nil {
		return err.Error()
	}
	t.Fatal("expected the build to fail")
	return ""
}
