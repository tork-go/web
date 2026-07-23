package tork

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"slices"
	"sort"
	"strings"
	"time"
)

// Middleware is the ecosystem's own middleware shape, so anything already
// written for net/http composes with a tork application unchanged.
type Middleware = func(http.Handler) http.Handler

// exchange is the per-request state a handler's parameters are produced from.
//
// It exists so that every kind of parameter — the context, a decoded request
// struct, an injected service — is produced by the same function type, which
// is what lets invoke call a handler without knowing what any of its
// arguments are.
//
// It is also where anything parsed out of the request is kept, because the
// query string is worth parsing once however many fields read it, and the body
// can only be read once at all.
type exchange struct {
	writer  http.ResponseWriter
	request *http.Request
	srv     *server

	query       url.Values
	queryParsed bool

	form       url.Values
	files      map[string][]*multipart.FileHeader
	formParsed bool
	formErr    error
}

// queryValues parses the query string once per request.
func (ex *exchange) queryValues() url.Values {
	if !ex.queryParsed {
		ex.query = ex.request.URL.Query()
		ex.queryParsed = true
	}
	return ex.query
}

// server is the built application: the routes compiled, registered, and ready
// to answer.
type server struct {
	mux *http.ServeMux

	now          func() time.Time
	logger       *slog.Logger
	errorMappers []ErrorMapper
	errorWriter  ErrorWriter
	maxBodyBytes int64
	strictBodies bool

	// singletons holds every provided value, built once when the application
	// built and read by index, so a handler needing one pays a slice access
	// and no lookup. singletonCleanups are what those providers registered, in
	// construction order, run in reverse when the application shuts down.
	singletons        []reflect.Value
	singletonCleanups []Cleanup
}

// close runs the singleton cleanups in reverse of construction, which is the
// order that tears a graph down safely: a thing built on top of another is
// released first.
func (s *server) close(ctx context.Context) error {
	return errors.Join(runCleanups(ctx, s.singletonCleanups)...)
}

// newServer registers every route and returns the handler that serves them.
//
// It is the last step of a build, and the only one that can still fail for a
// reason the declarations do not obviously contain: net/http.ServeMux refuses
// two patterns that overlap without one being more specific, and it says so
// by panicking, so the panic is turned back into the error the build was
// collecting.
func newServer(routes []*Route, base meta, inj *injector, construct bool) (*server, error) {
	s := &server{
		mux:          http.NewServeMux(),
		now:          base.now,
		logger:       base.logger,
		errorMappers: base.errorMappers,
		errorWriter:  base.errorWriter,
		maxBodyBytes: base.maxBodyBytes,
		strictBodies: base.strictBodies,
	}
	if s.logger == nil {
		s.logger = slog.Default()
	}
	if s.errorWriter == nil {
		s.errorWriter = defaultErrorWriter
	}
	if s.maxBodyBytes == 0 {
		s.maxBodyBytes = defaultMaxBodyBytes
	}

	// Singletons are built here, not while resolving the tree, because
	// construction is the one part of a build that runs the application's own
	// code and can fail for reasons no declaration reveals. Routes() asks for
	// the route table without it, so listing routes never opens a database;
	// Handler() asks with it, so serving fails at boot if a singleton cannot
	// be built rather than on the first request that needs one.
	if construct {
		singletons, cleanups, err := constructSingletons(context.Background(), inj)
		if err != nil {
			return nil, err
		}
		s.singletons = singletons
		s.singletonCleanups = cleanups
	}

	// Paths are collected in declaration order rather than read back out of
	// the map, so that two paths which turn out to conflict are always
	// reported the same way round.
	var paths []string
	allowed := map[string][]string{}
	for _, route := range routes {
		pattern := route.Method + " " + servePath(route.Path)
		if err := register(s.mux, pattern, wrap(s.serve(route), route.middleware)); err != nil {
			return nil, fmt.Errorf("%s: %s: %w", route.site, route, err)
		}
		if _, seen := allowed[route.Path]; !seen {
			paths = append(paths, route.Path)
		}
		allowed[route.Path] = appendUnique(allowed[route.Path], route.Method)
	}

	// A path that exists but not for this method answers 405 rather than
	// falling through to the catch-all 404. ServeMux prefers the pattern
	// with a method to the one without, so registering the bare path is
	// enough to catch everything the methods did not.
	//
	// This is also where two paths that differ only in the names of their
	// wildcards are caught. Their method patterns do not conflict as long as
	// the methods differ, but the bare paths always do.
	for _, path := range paths {
		if err := register(s.mux, servePath(path), s.methodNotAllowed(allowed[path])); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	// Every pattern above matches exactly one path, so "/" is left over for
	// the paths nothing matched. It cannot conflict with any of them — it is
	// the least specific pattern there is — and it is registered once, so
	// unlike the others it needs no guard.
	s.mux.Handle("/", http.HandlerFunc(s.notFound))

	return s, nil
}

// handler is the http.Handler a built server serves as. It is separate from
// the server so that shutdown, which the server owns, is not reachable through
// the value handed to net/http.
func (s *server) handler() http.Handler { return s.mux }

// servePath is a route's path as ServeMux needs it spelled.
//
// The root is the one path that has to be written differently: "/" alone is
// ServeMux's subtree pattern and would match everything, so it is anchored
// with the end-of-path wildcard. No other path needs it, because none of them
// ends in a slash.
func servePath(path string) string {
	if path == "/" {
		return "/{$}"
	}
	return path
}

// register adds one pattern, turning ServeMux's panic on conflicting patterns
// into an error the build can report beside everything else it found.
func register(mux *http.ServeMux, pattern string, handler http.Handler) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = fmt.Errorf("cannot register %q: %v", pattern, recovered)
		}
	}()
	mux.Handle(pattern, handler)
	return nil
}

// wrap puts a route inside its middleware, outermost first: the chain is
// applied in reverse so that the first middleware declared is the first one a
// request meets.
func wrap(handler http.Handler, middleware []Middleware) http.Handler {
	for i := len(middleware) - 1; i >= 0; i-- {
		handler = middleware[i](handler)
	}
	return handler
}

// serve is one route's handler: produce the arguments, call the function,
// write what it returned.
func (s *server) serve(route *Route) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer s.recoverPanic(w, r)

		result, err := route.plan.invoke(&exchange{writer: w, request: r, srv: s})
		if err != nil {
			s.fail(w, r, err)
			return
		}

		// A handler that returns only an error has nothing to say when it
		// succeeds, and 204 is how HTTP says exactly that.
		if route.plan.result == nil {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		if resp, ok := result.(Responder); ok {
			// tracked reports whether anything actually reached the client,
			// which is what decides how a failing Responder is answered: one
			// that fails before writing — the common case, since marshalling
			// comes first — is still a clean failure and gets the same
			// envelope writeJSON's own failures do; one that fails partway
			// through a body already begun can only be logged, because the
			// status line is already on the wire.
			tracked, tracker := trackResponse(w)
			if err := resp.WriteResponse(tracked, r); err != nil {
				if tracker.started {
					s.logger.Error("cannot write response", "method", r.Method, "path", r.URL.Path, "error", err)
				} else {
					s.fail(w, r, err)
				}
			}
			return
		}

		if err := writeJSON(w, http.StatusOK, result); err != nil {
			s.fail(w, r, err)
		}
	})
}

// responseTracker wraps a ResponseWriter to record whether a response has
// begun, which is the one thing serve needs to know about a Responder that
// failed and cannot know any other way: net/http's ResponseWriter has no
// method that answers "has anything been sent yet".
type responseTracker struct {
	http.ResponseWriter
	started bool
}

func (t *responseTracker) WriteHeader(status int) {
	t.started = true
	t.ResponseWriter.WriteHeader(status)
}

func (t *responseTracker) Write(b []byte) (int, error) {
	t.started = true
	return t.ResponseWriter.Write(b)
}

// flushingResponseTracker is a responseTracker for an underlying
// ResponseWriter that can flush.
//
// It exists so that tracking whether a response has begun never costs a
// Responder the ability to stream: without it, wrapping w in a plain
// responseTracker would hide http.Flusher from Stream even when the real
// ResponseWriter underneath supports it.
type flushingResponseTracker struct {
	*responseTracker
	flusher http.Flusher
}

func (t *flushingResponseTracker) Flush() {
	t.started = true
	t.flusher.Flush()
}

// trackResponse wraps w so the caller can learn, after the fact, whether
// anything reached the client. The returned ResponseWriter is what a
// Responder writes to; the returned tracker is what serve reads back.
func trackResponse(w http.ResponseWriter) (http.ResponseWriter, *responseTracker) {
	base := &responseTracker{ResponseWriter: w}
	if flusher, ok := w.(http.Flusher); ok {
		return &flushingResponseTracker{responseTracker: base, flusher: flusher}, base
	}
	return base, base
}

// recoverPanic turns a panicking handler into an internal error.
//
// A panic that escaped would take the connection with it and tell the client
// nothing, so it is logged with its stack and answered like any other failure
// the client cannot act on. It is not a substitute for handling errors: it is
// what keeps one bad request from being indistinguishable from a hung server.
func (s *server) recoverPanic(w http.ResponseWriter, r *http.Request) {
	recovered := recover()
	if recovered == nil {
		return
	}
	if errors.Is(asError(recovered), http.ErrAbortHandler) {
		// net/http's own signal for "stop, silently"; re-panicking is how
		// the server is told.
		panic(recovered)
	}
	s.logger.Error("handler panicked",
		"method", r.Method,
		"path", r.URL.Path,
		"panic", recovered,
	)
	s.write(w, r, *Internal())
}

// asError makes a recovered value comparable with errors.Is, which only
// matters for the one sentinel net/http asks handlers to re-panic.
func asError(recovered any) error {
	if err, ok := recovered.(error); ok {
		return err
	}
	return nil
}

// fail turns whatever a handler returned into the response it deserves.
func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	s.write(w, r, s.resolve(err))
}

// resolve decides which error body an error becomes.
//
// Mappers are consulted first, and see every error including ones that
// already know their own answer, so an application can override anything —
// its own domain errors, and the framework's. An error that implements
// HTTPError answers for itself. Anything left is a failure the client can do
// nothing about: it is logged in full and served as a bare internal error,
// because an unrecognised error's message is as likely to be a connection
// string as an explanation.
func (s *server) resolve(err error) Error {
	for _, mapper := range s.errorMappers {
		if mapped := mapper(err); mapped != nil {
			return *mapped
		}
	}

	var httpErr HTTPError
	if errors.As(err, &httpErr) {
		return httpErr.HTTPError()
	}

	s.logger.Error("unhandled error", "error", err)
	return *Internal()
}

// write completes an error body and serializes it.
//
// The timestamp and the path are stamped here rather than where the error was
// constructed: a handler does not know the path it was reached by, and should
// not be reading the clock. The status, code, and message are filled in when
// missing so that even a hand-built Error{} answers as something coherent.
func (s *server) write(w http.ResponseWriter, r *http.Request, e Error) {
	if e.Status == 0 {
		e.Status = http.StatusInternalServerError
	}
	if e.Code == "" {
		e.Code = codeForStatus(e.Status)
	}
	if e.Message == "" {
		e.Message = http.StatusText(e.Status)
	}
	e.Timestamp = Timestamp(s.now())
	e.Path = r.URL.Path

	if err := s.errorWriter(w, r, e); err != nil {
		// The response is already underway, so there is nowhere left to
		// report this except the log.
		s.logger.Error("cannot write error response",
			"method", r.Method,
			"path", r.URL.Path,
			"error", err,
		)
	}
}

// defaultErrorWriter serializes an error as JSON, which is what an
// application that has not said otherwise wants.
func defaultErrorWriter(w http.ResponseWriter, _ *http.Request, e Error) error {
	return writeJSON(w, e.Status, e)
}

// notFound answers a path no route matched.
//
// Its code is ROUTE_NOT_FOUND rather than RESOURCE_NOT_FOUND because the two
// are genuinely different failures: one means the client asked for something
// this API does not have, the other means it asked correctly for a thing that
// does not exist. A client retrying the second and giving up on the first is
// behaving sensibly, and it can only tell them apart if they say so.
func (s *server) notFound(w http.ResponseWriter, r *http.Request) {
	s.write(w, r, Error{
		Code:    "ROUTE_NOT_FOUND",
		Message: fmt.Sprintf("No route matches %s %s.", r.Method, r.URL.Path),
		Status:  http.StatusNotFound,
	})
}

// methodNotAllowed answers a path that exists for other methods, and answers
// an OPTIONS request outright, since the set of methods is exactly what
// OPTIONS asks for and the framework already knows it.
func (s *server) methodNotAllowed(methods []string) http.Handler {
	allow := allowHeader(methods)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Allow", allow)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		s.write(w, r, Error{
			Code:    "METHOD_NOT_ALLOWED",
			Message: fmt.Sprintf("%s is not allowed here; try %s.", r.Method, allow),
			Status:  http.StatusMethodNotAllowed,
			Details: map[string]any{"allowed": strings.Split(allow, ", ")},
		})
	})
}

// allowHeader is the Allow header for a path: the methods declared for it,
// plus the two the framework answers on their behalf. HEAD is included
// because ServeMux serves it from a GET route, and OPTIONS because the
// handler above answers it.
func allowHeader(methods []string) string {
	all := slices.Clone(methods)
	if slices.Contains(all, http.MethodGet) {
		all = appendUnique(all, http.MethodHead)
	}
	all = appendUnique(all, http.MethodOptions)
	sort.Strings(all)
	return strings.Join(all, ", ")
}
