package tork_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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

func TestRawResponseWritesContentTypeAndBody(t *testing.T) {
	app := newApp()
	app.GET("/export.csv", func(context.Context) (tork.RawResponse, error) {
		return tork.Raw("text/csv", []byte("a,b\n1,2\n")), nil
	})

	rec := do(t, app, "GET", "/export.csv", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "text/csv" {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Body.String(); got != "a,b\n1,2\n" {
		t.Errorf("body = %q", got)
	}
}

// WithStatus and a chained WithHeader together prove RawResponse offers the
// same fluent path Response does, not a narrower one.
func TestRawResponseWithStatusAndHeader(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.RawResponse, error) {
		return tork.Raw("application/octet-stream", []byte{1, 2, 3}).
			WithStatus(http.StatusAccepted).
			WithHeader("X-Checksum", "abc").
			WithHeader("X-Trace", "def"), nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("X-Checksum"); got != "abc" {
		t.Errorf("X-Checksum = %q", got)
	}
	if got := rec.Header().Get("X-Trace"); got != "def" {
		t.Errorf("X-Trace = %q", got)
	}
	if rec.Body.Bytes()[1] != 2 {
		t.Errorf("body = %v", rec.Body.Bytes())
	}
}

// ResponseSpec is asked about a zero-valued RawResponse, not the one a
// handler actually returns, so it can say the status (RawResponse defaults
// to 200 the same way Response does) but not the content type, which has no
// default to fall back on and so is genuinely unknown until a request
// happens.
func TestRouteResponseSpecForRawResponseHasNoContentTypeOrBodyType(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.RawResponse, error) {
		return tork.Raw("text/csv", nil), nil
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
	if spec.ContentType != "" {
		t.Errorf("content type = %q, want empty since it is unknown before a request", spec.ContentType)
	}
	if spec.BodyType != nil {
		t.Errorf("body type = %v, want nil", spec.BodyType)
	}
}

func TestFileResponseGuessesContentTypeFromTheExtension(t *testing.T) {
	app := newApp()
	app.GET("/download", func(context.Context) (tork.FileResponse, error) {
		return tork.File("invoice.pdf", strings.NewReader("PDF-DATA")), nil
	})

	rec := do(t, app, "GET", "/download", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/pdf" {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename=invoice.pdf` {
		t.Errorf("content disposition = %q", got)
	}
	if rec.Body.String() != "PDF-DATA" {
		t.Errorf("body = %q", rec.Body)
	}
}

// A filename with a space needs quoting to stay one value in the header;
// unlike invoice.pdf above, this is where hand-rolling the header would be
// easy to get wrong.
func TestFileResponseQuotesAFilenameThatNeedsIt(t *testing.T) {
	app := newApp()
	app.GET("/download", func(context.Context) (tork.FileResponse, error) {
		return tork.File("year end report.pdf", strings.NewReader("x")), nil
	})

	rec := do(t, app, "GET", "/download", nil)
	if got := rec.Header().Get("Content-Disposition"); got != `attachment; filename="year end report.pdf"` {
		t.Errorf("content disposition = %q", got)
	}
}

func TestFileResponseWithUnrecognisedExtensionFallsBackToOctetStream(t *testing.T) {
	app := newApp()
	app.GET("/download", func(context.Context) (tork.FileResponse, error) {
		return tork.File("data.unrecognised-extension", strings.NewReader("bytes")), nil
	})

	rec := do(t, app, "GET", "/download", nil)
	if got := rec.Header().Get("Content-Type"); got != "application/octet-stream" {
		t.Errorf("content type = %q", got)
	}
}

// WithContentType overrides the guess, and chaining WithSize and WithHeader
// after it proves the fluent path is not limited to one addition at a time.
func TestFileResponseWithContentTypeSizeAndHeader(t *testing.T) {
	app := newApp()
	app.GET("/download", func(context.Context) (tork.FileResponse, error) {
		return tork.File("data.bin", strings.NewReader("12345")).
			WithContentType("application/x-custom").
			WithSize(5).
			WithHeader("X-Checksum", "abc").
			WithHeader("X-Trace", "def"), nil
	})

	rec := do(t, app, "GET", "/download", nil)
	if got := rec.Header().Get("Content-Type"); got != "application/x-custom" {
		t.Errorf("content type = %q", got)
	}
	if got := rec.Header().Get("Content-Length"); got != "5" {
		t.Errorf("content length = %q", got)
	}
	if got := rec.Header().Get("X-Checksum"); got != "abc" {
		t.Errorf("X-Checksum = %q", got)
	}
	if got := rec.Header().Get("X-Trace"); got != "def" {
		t.Errorf("X-Trace = %q", got)
	}
}

// failingReader answers some bytes successfully and then always fails,
// standing in for a file truncated on disk or a stream that drops midway.
type failingReader struct {
	remaining []byte
	err       error
}

func (r *failingReader) Read(p []byte) (int, error) {
	if len(r.remaining) == 0 {
		return 0, r.err
	}
	n := copy(p, r.remaining)
	r.remaining = r.remaining[n:]
	return n, nil
}

// A reader that fails partway has already sent headers and some of the body
// by the time it does, so the response is only ever what it managed to send
// — the same begun-response handling FileResponse shares with every other
// Responder.
func TestFileResponseReaderFailingPartwayIsOnlyLogged(t *testing.T) {
	app := newApp()
	app.GET("/download", func(context.Context) (tork.FileResponse, error) {
		reader := &failingReader{remaining: []byte("partial"), err: errors.New("disk error")}
		return tork.File("report.txt", reader), nil
	})

	rec := do(t, app, "GET", "/download", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the status already sent before the reader failed", rec.Code)
	}
	if rec.Body.String() != "partial" {
		t.Errorf("body = %q, want only what was read before the failure", rec.Body.String())
	}
}

func TestRouteResponseSpecForFileResponseHasNoContentTypeOrBodyType(t *testing.T) {
	app := newApp()
	app.GET("/download", func(context.Context) (tork.FileResponse, error) {
		return tork.File("report.pdf", strings.NewReader("")), nil
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
	if spec.ContentType != "" {
		t.Errorf("content type = %q, want empty since it is unknown before a request", spec.ContentType)
	}
	if spec.BodyType != nil {
		t.Errorf("body type = %v, want nil", spec.BodyType)
	}
}

// recordingFlusher is a minimal http.ResponseWriter and http.Flusher that
// records the order writes and flushes happen in — the one thing
// httptest.ResponseRecorder cannot show, since its own Flush only sets a
// bool rather than recording when it was called.
type recordingFlusher struct {
	header http.Header
	status int
	calls  []string
}

func newRecordingFlusher() *recordingFlusher {
	return &recordingFlusher{header: http.Header{}}
}

func (w *recordingFlusher) Header() http.Header { return w.header }

func (w *recordingFlusher) WriteHeader(status int) { w.status = status }

func (w *recordingFlusher) Write(p []byte) (int, error) {
	w.calls = append(w.calls, "write:"+string(p))
	return len(p), nil
}

func (w *recordingFlusher) Flush() {
	w.calls = append(w.calls, "flush")
}

func TestStreamFlushesAfterEveryWrite(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.Stream, error) {
		return tork.Streamed("text/plain", func(w io.Writer) error {
			if _, err := w.Write([]byte("a")); err != nil {
				return err
			}
			_, err := w.Write([]byte("b"))
			return err
		}), nil
	})

	rec := newRecordingFlusher()
	handlerOf(t, app).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	want := []string{"write:a", "flush", "write:b", "flush"}
	if !equalStrings(rec.calls, want) {
		t.Errorf("calls = %v, want %v", rec.calls, want)
	}
	if rec.status != http.StatusOK {
		t.Errorf("status = %d", rec.status)
	}
	if got := rec.header.Get("Content-Type"); got != "text/plain" {
		t.Errorf("content type = %q", got)
	}
}

// plainWriter is a minimal http.ResponseWriter with no Flush method, for
// proving Stream still writes correctly against a connection that cannot
// flush early.
type plainWriter struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newPlainWriter() *plainWriter { return &plainWriter{header: http.Header{}} }

func (w *plainWriter) Header() http.Header { return w.header }

func (w *plainWriter) WriteHeader(status int) { w.status = status }

func (w *plainWriter) Write(p []byte) (int, error) { return w.body.Write(p) }

func TestStreamWritesWithoutFlushingWhenTheConnectionCannot(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.Stream, error) {
		return tork.Streamed("text/plain", func(w io.Writer) error {
			_, err := w.Write([]byte("hello"))
			return err
		}), nil
	})

	rec := newPlainWriter()
	handlerOf(t, app).ServeHTTP(rec, httptest.NewRequest("GET", "/", nil))

	if rec.status != http.StatusOK {
		t.Errorf("status = %d", rec.status)
	}
	if rec.body.String() != "hello" {
		t.Errorf("body = %q", rec.body.String())
	}
}

func TestStreamWithStatusReplacesTheDefault(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.Stream, error) {
		return tork.Streamed("text/plain", func(w io.Writer) error {
			_, err := w.Write([]byte("queued"))
			return err
		}).WithStatus(http.StatusAccepted), nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusAccepted {
		t.Errorf("status = %d", rec.Code)
	}
	if rec.Body.String() != "queued" {
		t.Errorf("body = %q", rec.Body)
	}
}

// A callback that fails partway has already sent headers and some of the
// body, so — like FileResponse's reader — the response is only ever what it
// managed to send before the failure.
func TestStreamCallbackFailingPartwayIsOnlyLogged(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.Stream, error) {
		return tork.Streamed("text/plain", func(w io.Writer) error {
			if _, err := w.Write([]byte("partial")); err != nil {
				return err
			}
			return errors.New("stream failed")
		}), nil
	})

	rec := do(t, app, "GET", "/", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want the status already sent before the callback failed", rec.Code)
	}
	if rec.Body.String() != "partial" {
		t.Errorf("body = %q, want only what was written before the failure", rec.Body.String())
	}
}

func TestRouteResponseSpecForStream(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context) (tork.Stream, error) {
		return tork.Streamed("text/plain", func(io.Writer) error { return nil }), nil
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
	if spec.ContentType != "" {
		t.Errorf("content type = %q, want empty since it is unknown before a request", spec.ContentType)
	}
	if spec.BodyType != nil {
		t.Errorf("body type = %v, want nil", spec.BodyType)
	}
}
