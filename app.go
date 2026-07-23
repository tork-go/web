package tork

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"
)

// App is the application: the root of the router tree and the metadata
// describing the API as a whole.
//
// Nothing is validated while an application is being described. Options,
// paths, and handler signatures are all checked when Handler builds, so one
// build reports every mistake in the tree at once instead of stopping at the
// first — the same bargain a compiler makes, and the reason none of the
// declaration functions return an error.
type App struct {
	opts []Option
	root *Router

	// info is what the build learned about the API itself, kept for the
	// OpenAPI document.
	info apiInfo

	// A built application constructs its singletons exactly once, however many
	// times Handler is asked for it, and remembers the result so Close can
	// reach the cleanups those singletons registered. buildOnce guards the
	// construction; built is the server it produced, or nil if the build
	// failed or has not happened.
	buildOnce    sync.Once
	builtHandler http.Handler
	built        *server
	buildErr     error
}

// apiInfo is the application-level metadata an OpenAPI document needs.
type apiInfo struct {
	title       string
	description string
}

// New begins an application.
//
// It takes both the options describing the API — Title, Description — and the
// ones every route inherits, since the application is also the root of the
// router tree: Tags and Use written here reach everything.
func New(opts ...Option) *App {
	return &App{opts: opts, root: &Router{site: callerSite(2)}}
}

// Version declares an API version: a router whose routes get their own
// OpenAPI document, served under their own documentation URL.
//
//	v1 := app.Version("v1", tork.Prefix("/api/v1"))
//
// The name is what the document is published as, and what an operation ID
// must be unique within.
func (a *App) Version(name string, opts ...Option) *Router {
	site := callerSite(2)
	child := &Router{opts: opts, site: site, version: name}
	a.root.decls = append(a.root.decls, mount{child: child, site: site})
	return child
}

// Include mounts a router at the root of the application.
func (a *App) Include(child *Router, opts ...Option) {
	a.root.decls = append(a.root.decls, mount{child: child, opts: opts, site: callerSite(2)})
}

// Handle declares a route at the root of the application.
func (a *App) Handle(method, path string, handler any, opts ...Option) {
	a.root.handle(callerSite(2), method, path, handler, opts)
}

// GET declares a route at the root of the application.
func (a *App) GET(path string, handler any, opts ...Option) {
	a.root.handle(callerSite(2), http.MethodGet, path, handler, opts)
}

// POST declares a route at the root of the application.
func (a *App) POST(path string, handler any, opts ...Option) {
	a.root.handle(callerSite(2), http.MethodPost, path, handler, opts)
}

// PUT declares a route at the root of the application.
func (a *App) PUT(path string, handler any, opts ...Option) {
	a.root.handle(callerSite(2), http.MethodPut, path, handler, opts)
}

// PATCH declares a route at the root of the application.
func (a *App) PATCH(path string, handler any, opts ...Option) {
	a.root.handle(callerSite(2), http.MethodPatch, path, handler, opts)
}

// DELETE declares a route at the root of the application.
func (a *App) DELETE(path string, handler any, opts ...Option) {
	a.root.handle(callerSite(2), http.MethodDelete, path, handler, opts)
}

// OPTIONS declares a route at the root of the application.
func (a *App) OPTIONS(path string, handler any, opts ...Option) {
	a.root.handle(callerSite(2), http.MethodOptions, path, handler, opts)
}

// Routes resolves the tree and returns the finished routes, in declaration
// order. It is what the OpenAPI builder and the generator read, and what a
// test asserts against when it wants the route table rather than a server.
func (a *App) Routes() ([]*Route, error) {
	routes, _, _, err := a.build(false)
	return routes, err
}

// Handler builds the http.Handler that serves the application.
//
// This is where every declaration is finally checked: options against the
// site they were written at, paths against their prefixes, handler signatures
// against what the framework knows how to call, and the whole route table
// against itself for duplicates. All of it is reported together.
//
// The build happens once, however many times Handler is called: the
// singletons an application constructs are constructed a single time, and Close
// tears down the same ones. Tests want this rather than Run: it needs no port,
// and it returns the same handler net/http would be given.
func (a *App) Handler() (http.Handler, error) {
	a.buildOnce.Do(func() {
		_, a.builtHandler, a.built, a.buildErr = a.build(true)
	})
	return a.builtHandler, a.buildErr
}

// Close runs the cleanups the application's singletons registered, in reverse
// of the order they were built. It is called for you when Serve or
// ServeListener returns, and is exported for an application built with Handler
// and served some other way. An application that never built, or failed to,
// has nothing to close.
func (a *App) Close(ctx context.Context) error {
	if a.built == nil {
		return nil
	}
	return a.built.close(ctx)
}

// build resolves the tree once and returns the things callers want from it, so
// asking for the routes and asking for the handler cannot disagree about what
// the application contains. It constructs the application's singletons only
// when construct is set, which is what lets Routes report a graph's mistakes
// without running its constructors.
func (a *App) build(construct bool) ([]*Route, http.Handler, *server, error) {
	base := meta{now: time.Now, providers: &providerSet{}}
	errs := applyOptions(&base, scopeApp|scopeRouter, "an application", a.opts)
	a.info = apiInfo{title: base.title, description: base.description}

	routes, resolveErrs := a.root.resolve(base)
	errs = append(errs, resolveErrs...)

	inj, graphErrs := analyzeGraph(base.providers)
	errs = append(errs, graphErrs...)

	for _, route := range routes {
		plan, err := compileHandler(route, inj)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s: %s: %w", route.site, route, err))
			continue
		}
		route.plan = plan
	}

	errs = append(errs, checkUnique(routes)...)

	if len(errs) > 0 {
		return nil, nil, nil, errors.Join(errs...)
	}

	s, err := newServer(routes, base, inj, construct)
	if err != nil {
		return nil, nil, nil, err
	}
	return routes, s.handler(), s, nil
}

// checkUnique reports the two collisions a route table can contain: the same
// method and path declared twice, which would leave one of them unreachable,
// and the same operation ID used twice within one API version, which would
// leave a generated client with two functions of one name.
func checkUnique(routes []*Route) []error {
	var errs []error

	type key struct{ method, path string }
	byPath := map[key]*Route{}
	byID := map[[2]string]*Route{}

	for _, route := range routes {
		k := key{route.Method, route.Path}
		if first, dup := byPath[k]; dup {
			errs = append(errs, fmt.Errorf("%s: %s is already declared at %s", route.site, route, first.site))
		} else {
			byPath[k] = route
		}

		id := [2]string{route.Version, route.OperationID}
		if first, dup := byID[id]; dup {
			errs = append(errs, fmt.Errorf("%s: operation ID %q is already used by %s at %s; set tork.OperationID on one of them",
				route.site, route.OperationID, first, first.site))
		} else {
			byID[id] = route
		}
	}

	return errs
}

// Run serves the application on addr until the process is asked to stop.
//
// SIGINT and SIGTERM begin a graceful shutdown: the listener closes, requests
// already in flight are given time to finish, and Run returns nil. Anything
// else — a port already in use, a mistake in the declarations — comes back as
// an error.
func (a *App) Run(addr string) error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return a.Serve(ctx, addr)
}

// shutdownGrace is how long a graceful shutdown waits for requests already in
// flight before giving up on them.
const shutdownGrace = 10 * time.Second

// Serve is Run with the stop signal supplied by the caller, which is what
// makes it testable: a test cancels the context instead of arranging for a
// signal.
func (a *App) Serve(ctx context.Context, addr string) error {
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return a.ServeListener(ctx, listener)
}

// ServeListener serves the application on a listener the caller opened.
//
// It is what to use when something other than an address decides where the
// server listens: a socket handed over by the supervisor, a TLS listener, or
// a test that wants the port assigned to it rather than chosen in advance.
// The listener is closed by the time this returns.
func (a *App) ServeListener(ctx context.Context, listener net.Listener) error {
	handler, err := a.Handler()
	if err != nil {
		// The caller opened the listener, so the caller's expectation that
		// something closes it holds even when there is nothing to serve.
		listener.Close()
		return err
	}

	srv := &http.Server{Handler: handler}
	serveErr := make(chan error, 1)
	go func() {
		serveErr <- srv.Serve(listener)
	}()

	select {
	case err := <-serveErr:
		// Serve only returns on its own when the listener failed; the
		// shutdown below never reaches this case, because it returns
		// without waiting for the goroutine. The singletons are closed even
		// so, since they were built and the process is done with them.
		grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		return errors.Join(err, a.Close(grace))
	case <-ctx.Done():
		grace, cancel := context.WithTimeout(context.Background(), shutdownGrace)
		defer cancel()
		return errors.Join(srv.Shutdown(grace), a.Close(grace))
	}
}
