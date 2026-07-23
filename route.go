package tork

import (
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"
)

// meta is everything a declaration site can say, in one struct.
//
// One struct rather than three is what keeps options uniform: an option
// writes a field and declares which sites may write it, instead of every
// site owning a different shape. The fields fall into two groups, and which
// group a field is in is the whole of the inheritance rule — see inherited.
type meta struct {
	// Inherited by everything declared underneath.
	prefix     string
	tags       []string
	middleware []Middleware
	deprecated bool
	version    string
	responses  map[int]ResponseDoc
	throws     map[reflect.Type]ResponseDoc

	// providers is the application-wide provider set. Unlike everything else
	// in this group it is a pointer to one shared collection rather than a
	// value cloned per level, because a provider declared on any router is
	// visible everywhere: inherited() copies the pointer, so a Provide deep in
	// the tree still lands in the one set build() reads. It is seeded in
	// build() so an option's apply can always assume it is non-nil.
	providers *providerSet

	// Belonging to one operation, and never inherited. A summary that
	// applied to every route under a router would be wrong on all but the
	// one it was written for.
	operationID string
	summary     string
	description string

	// Set only at the application, and read only from the application's own
	// meta. They are not inherited because nothing below the root has any
	// use for them: a route does not serve its own errors, the server does.
	title        string
	now          func() time.Time
	logger       *slog.Logger
	errorMappers []ErrorMapper
	errorWriter  ErrorWriter
	maxBodyBytes int64
	strictBodies bool
}

// inherited returns what a child declaration starts from: the inheritable
// fields, and none of the per-operation ones.
//
// The slices and maps are cloned rather than shared. Two routers included
// into the same parent both append to the tags — and add to the responses
// and throws — they inherited, and writing into a shared backing array or
// map would let the first one's changes appear on the second.
func (m meta) inherited() meta {
	return meta{
		prefix:     m.prefix,
		tags:       slices.Clone(m.tags),
		middleware: slices.Clone(m.middleware),
		deprecated: m.deprecated,
		version:    m.version,
		responses:  maps.Clone(m.responses),
		throws:     maps.Clone(m.throws),
		providers:  m.providers,
	}
}

// Route is one method-and-path pair with everything it inherited already
// folded in. It is what the server registers, what the OpenAPI document is
// built from, and what the generator emits an adapter for, so by the time a
// Route exists no question about it needs the tree it came from.
type Route struct {
	// Method is the HTTP method, upper case.
	Method string
	// Path is the full path, prefixes concatenated, never with a trailing
	// slash.
	Path string
	// OperationID is unique within the API version; derived from the
	// handler's name when the declaration did not set one.
	OperationID string
	// Summary, Description, Tags, and Deprecated are documentation, and
	// change nothing about how the route behaves.
	Summary     string
	Description string
	Tags        []string
	Deprecated  bool
	// Version names the API version this route belongs to, and so which
	// OpenAPI document it appears in. It is "" for routes declared outside
	// any version.
	Version string

	// ResponseSpec is what the handler's result type promises about its
	// response, when that type implements Responder; nil for a plain T,
	// which is still 200 JSON, or for an error-only handler. It is set once
	// at build and read by nothing at request time — it exists for the
	// OpenAPI phase to build a document from without a request.
	ResponseSpec *ResponseSpec
	// Responses and Throws are what Responds and Throws declared on this
	// route and everything it inherited from, merged the same way every
	// other inherited field is: a route-level Responds for a status a
	// router already declared replaces the router's, and Throws simply
	// accumulates, since there is no status for two declarations of it to
	// collide over.
	Responses map[int]ResponseDoc
	Throws    map[reflect.Type]ResponseDoc

	// handler is the function as the user wrote it, kept for the generator
	// and for error messages; plan is what actually runs.
	handler any
	plan    *handlerPlan

	// middleware is the chain to wrap this route in, outermost first.
	middleware []Middleware

	// site is where the route was declared, so an error about it can point
	// at the line rather than only at the path.
	site string
}

// String identifies a route the way an error message should: the method and
// path a reader can find in their own source.
func (r *Route) String() string {
	return r.Method + " " + r.Path
}

// declaredRoute is a route as written, before its parents are known. It holds
// the path relative to the router that declared it, and the options that
// route alone carries.
type declaredRoute struct {
	method  string
	path    string
	handler any
	opts    []Option
	site    string
}

// resolve folds the inherited metadata and the route's own options into a
// finished Route.
//
// It returns a slice so that a route and a mounted router satisfy the same
// interface and can share one declaration list; a route resolves to one
// Route, or to none if it is malformed.
func (d *declaredRoute) resolve(parent meta) ([]*Route, []error) {
	m := parent.inherited()
	errs := applyOptions(&m, scopeRoute, "a route", d.opts)

	path, err := joinPath(m.prefix, d.path)
	if err != nil {
		errs = append(errs, fmt.Errorf("%s: %s %s: %w", d.site, d.method, d.path, err))
		return nil, errs
	}

	id := m.operationID
	if id == "" {
		id = derivedOperationID(d.handler, d.method, path)
	}

	return []*Route{{
		Method:      d.method,
		Path:        path,
		OperationID: id,
		Summary:     m.summary,
		Description: m.description,
		Tags:        m.tags,
		Deprecated:  m.deprecated,
		Version:     m.version,
		Responses:   m.responses,
		Throws:      m.throws,
		handler:     d.handler,
		middleware:  m.middleware,
		site:        d.site,
	}}, errs
}

// joinPath concatenates a router's prefix with a route's path.
//
// A route path of "/" means the prefix itself — the idiom for the collection
// a router is mounted at — so the result never ends in a slash. That matters
// more than tidiness: net/http.ServeMux reads a trailing slash as a subtree
// match, so "/items/" would match every path under /items instead of the one
// route declared for it.
func joinPath(prefix, path string) (string, error) {
	if !strings.HasPrefix(path, "/") {
		return "", fmt.Errorf("path %q must begin with a slash", path)
	}
	full := prefix + strings.TrimSuffix(path, "/")
	if full == "" {
		full = "/"
	}
	return full, nil
}
