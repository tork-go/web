package tork

import (
	"fmt"
	"net/http"
	"strings"
)

// Router is a group of routes that share metadata.
//
// It is not a multiplexer. Nothing is dispatched here and no path is matched;
// a router holds declarations, and the application folds them into finished
// routes when it builds its handler. That is what lets a feature package
// return a *Router that names a prefix, tags, dependencies, and error
// responses without knowing where it will be mounted, and what lets the
// application mount it somewhere else entirely.
//
// A router's own options are applied when it is mounted rather than when it
// is constructed, which is why NewRouter cannot fail: the prefix it
// contributes has to land after whatever prefix it is mounted under, and that
// is not known yet at the point the router is written.
type Router struct {
	opts  []Option
	decls []declaration
	site  string

	// version is set only by App.Version, and names the OpenAPI document
	// every route underneath belongs to.
	version string
}

// declaration is one thing written on a router: a route, or another router
// mounted under it.
//
// Both live in one list rather than two so that routes come out in the order
// they were written. Two lists would put every route of a router before every
// route of anything included into it, which is not the order anyone reading
// the file would expect, and that order is what the OpenAPI document and
// every error message are built in.
type declaration interface {
	resolve(parent meta) ([]*Route, []error)
}

// mount is one router included into another, with the options the including
// site added.
type mount struct {
	child *Router
	opts  []Option
	site  string
}

// resolve applies the including site's options between the parent's metadata
// and the child's own, which is what puts a prefix given to Include after the
// parent's and before the child's.
func (mt mount) resolve(parent meta) ([]*Route, []error) {
	if mt.child == nil {
		return nil, []error{fmt.Errorf("%s: tork.Include: router is nil", mt.site)}
	}
	m := parent.inherited()
	errs := applyOptions(&m, scopeRouter, "a router", mt.opts)
	routes, childErrs := mt.child.resolve(m)
	return routes, append(errs, childErrs...)
}

// NewRouter declares a group of routes.
//
// The options are the ones every route underneath inherits: Prefix, Tags,
// Use, Deprecated, and, once declared, the dependencies and providers of the
// package the router belongs to.
func NewRouter(opts ...Option) *Router {
	return &Router{opts: opts, site: callerSite(2)}
}

// Include mounts child underneath r, with opts applied between the two: a
// prefix given here comes after r's and before child's own.
func (r *Router) Include(child *Router, opts ...Option) {
	r.decls = append(r.decls, mount{child: child, opts: opts, site: callerSite(2)})
}

// Handle declares a route for an arbitrary method. The method is upper-cased,
// since that is how HTTP spells it and how net/http.ServeMux matches it.
//
// GET, POST, and the rest of the named methods below are the ones to reach
// for; this exists for the methods they do not cover.
func (r *Router) Handle(method, path string, handler any, opts ...Option) {
	r.handle(callerSite(2), method, path, handler, opts)
}

// GET declares a route. A GET route answers HEAD as well, which is
// net/http.ServeMux's own rule, not one this package adds.
func (r *Router) GET(path string, handler any, opts ...Option) {
	r.handle(callerSite(2), http.MethodGet, path, handler, opts)
}

// POST declares a route.
func (r *Router) POST(path string, handler any, opts ...Option) {
	r.handle(callerSite(2), http.MethodPost, path, handler, opts)
}

// PUT declares a route.
func (r *Router) PUT(path string, handler any, opts ...Option) {
	r.handle(callerSite(2), http.MethodPut, path, handler, opts)
}

// PATCH declares a route.
func (r *Router) PATCH(path string, handler any, opts ...Option) {
	r.handle(callerSite(2), http.MethodPatch, path, handler, opts)
}

// DELETE declares a route.
func (r *Router) DELETE(path string, handler any, opts ...Option) {
	r.handle(callerSite(2), http.MethodDelete, path, handler, opts)
}

// OPTIONS declares a route.
func (r *Router) OPTIONS(path string, handler any, opts ...Option) {
	r.handle(callerSite(2), http.MethodOptions, path, handler, opts)
}

// handle records a declaration. Nothing is validated here beyond the method's
// spelling: a bad path or an unusable handler signature is reported when the
// application builds, together with everything else wrong, rather than one
// panic at a time as the file is read.
func (r *Router) handle(site, method, path string, handler any, opts []Option) {
	r.decls = append(r.decls, &declaredRoute{
		method:  strings.ToUpper(method),
		path:    path,
		handler: handler,
		opts:    opts,
		site:    site,
	})
}

// resolve walks this router and everything mounted under it, folding
// inherited metadata into finished routes.
//
// Errors accumulate rather than stopping the walk, so one build reports every
// bad option, path, and signature in the tree.
func (r *Router) resolve(parent meta) ([]*Route, []error) {
	m := parent.inherited()
	if r.version != "" {
		m.version = r.version
	}
	errs := applyOptions(&m, scopeRouter, "a router", r.opts)

	var routes []*Route
	for _, decl := range r.decls {
		declared, declErrs := decl.resolve(m)
		routes = append(routes, declared...)
		errs = append(errs, declErrs...)
	}

	return routes, errs
}
