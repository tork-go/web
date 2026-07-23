package tork

import (
	"fmt"
	"path/filepath"
	"reflect"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Option is one piece of metadata attached to an application, a router, or a
// single route.
//
// It is a struct rather than an interface so the set stays closed: every
// option this package accepts is a function in this package, which is what
// lets an option know both where it is allowed to appear and where it was
// written. Constructing one records the caller's file and line, so an option
// used in the wrong place is reported the way a compiler would report it
// rather than as a value that quietly did nothing.
type Option struct {
	name  string
	scope scope
	site  string
	apply func(*meta) error
}

// scope is the set of declaration sites an option may appear at. An option
// carries the sites it accepts, and each site checks the bit it stands for,
// so adding a site later means adding a bit rather than a case to every
// option.
type scope uint8

const (
	// scopeApp is New: metadata describing the API as a whole.
	scopeApp scope = 1 << iota
	// scopeRouter is NewRouter, Version, and Include: metadata every route
	// underneath inherits.
	scopeRouter
	// scopeRoute is GET, POST, and the rest: metadata for one operation.
	scopeRoute
)

// newOption builds an option, recording the line the caller's caller wrote it
// on. Every exported constructor in this package is a one-line wrapper around
// it, which is what makes the skip count a constant.
func newOption(name string, sc scope, apply func(*meta) error) Option {
	return Option{name: name, scope: sc, site: callerSite(3), apply: apply}
}

// callerSite names the source line skip frames up, as "package/file.go:line".
// The full path is trimmed to its last two elements because that is the part
// a reader recognises, and the leading directories differ on every machine.
//
// A stack too short to have the frame — which needs a caller this package has
// no way to be given — yields ":0" rather than a branch that can never run.
func callerSite(skip int) string {
	_, file, line, _ := runtime.Caller(skip)
	return shortPath(file) + ":" + strconv.Itoa(line)
}

// shortPath keeps the last two elements of a path, so a site reads
// "items/router.go" rather than the machine's whole module root.
func shortPath(file string) string {
	parts := strings.Split(filepath.ToSlash(file), "/")
	return strings.Join(parts[max(0, len(parts)-2):], "/")
}

// applyOptions folds opts into m, rejecting any option the site does not
// accept. The label is the site as an error message names it, article and
// all, and is passed in rather than derived from accept because the
// application accepts two scopes at once. Every option is tried even after
// one fails, because a caller who misplaced two of them should learn about
// both at once.
func applyOptions(m *meta, accept scope, label string, opts []Option) []error {
	var errs []error
	for _, opt := range opts {
		if opt.scope&accept == 0 {
			errs = append(errs, fmt.Errorf("%s: tork.%s is not valid on %s", opt.site, opt.name, label))
			continue
		}
		if err := opt.apply(m); err != nil {
			errs = append(errs, fmt.Errorf("%s: tork.%s: %w", opt.site, opt.name, err))
		}
	}
	return errs
}

// Title names the API in the OpenAPI document.
func Title(title string) Option {
	return newOption("Title", scopeApp, func(m *meta) error {
		m.title = title
		return nil
	})
}

// Description is prose about the API or, on a route, about the operation.
//
// It is not inherited. A description that applied to every route underneath a
// router would be wrong on all but the one it was written for, so a router
// takes its documentation from the doc comments of the handlers it holds.
func Description(text string) Option {
	return newOption("Description", scopeApp|scopeRoute, func(m *meta) error {
		m.description = text
		return nil
	})
}

// Prefix is the path every route underneath is mounted under.
//
// It must begin with a slash and must not end with one, because a trailing
// slash is how net/http.ServeMux spells a subtree match: "/items/" would
// match every path beginning with /items rather than the routes declared
// under it.
func Prefix(prefix string) Option {
	return newOption("Prefix", scopeRouter, func(m *meta) error {
		if prefix == "" {
			return nil
		}
		if !strings.HasPrefix(prefix, "/") {
			return fmt.Errorf("prefix %q must begin with a slash", prefix)
		}
		if strings.HasSuffix(prefix, "/") {
			return fmt.Errorf("prefix %q must not end with a slash", prefix)
		}
		m.prefix += prefix
		return nil
	})
}

// Tags group operations in the OpenAPI document and in the docs UI.
//
// Tags accumulate down the tree and are deduplicated, so a route under a
// router tagged "items" that adds "custom" carries both.
func Tags(tags ...string) Option {
	return newOption("Tags", scopeApp|scopeRouter|scopeRoute, func(m *meta) error {
		m.tags = appendUnique(m.tags, tags...)
		return nil
	})
}

// Summary is the one-line description of an operation shown in the docs UI.
func Summary(text string) Option {
	return newOption("Summary", scopeRoute, func(m *meta) error {
		m.summary = text
		return nil
	})
}

// OperationID is the stable name generated clients give an operation. It must
// be unique within an API version; two routes claiming the same one is an
// error at startup rather than a document that silently loses an operation.
//
// Left unset, it is derived from the handler's own name, which is stable as
// long as the function is.
func OperationID(id string) Option {
	return newOption("OperationID", scopeRoute, func(m *meta) error {
		if id == "" {
			return fmt.Errorf("operation ID must not be empty")
		}
		m.operationID = id
		return nil
	})
}

// Deprecated marks an operation, or every operation under a router, as
// deprecated in the OpenAPI document. It changes nothing about how the route
// behaves.
func Deprecated() Option {
	return newOption("Deprecated", scopeRouter|scopeRoute, func(m *meta) error {
		m.deprecated = true
		return nil
	})
}

// Responds documents a response an operation may answer with beyond what
// its result type already says on its own — a status a handler or a
// dependency chooses at runtime, or an error response whose shape is worth
// naming even though tork.Throws would name it too. It changes nothing at
// runtime; the OpenAPI phase reads it.
//
// Declared on a router, it documents every route underneath; declared again
// on a route for the same status, the route's own description replaces the
// router's, the same way every inherited field a route redeclares does.
func Responds[T any](status int, description string) Option {
	return newOption("Responds", scopeRouter|scopeRoute, func(m *meta) error {
		if m.responses == nil {
			m.responses = map[int]ResponseDoc{}
		}
		m.responses[status] = ResponseDoc{Status: status, Type: reflect.TypeFor[T](), Description: description}
		return nil
	})
}

// Throws documents an error shape an operation may answer with, for a
// domain error a handler or a dependency returns rather than one the
// framework already knows about. It changes nothing at runtime; the
// OpenAPI phase reads it.
//
// Declaring the same type twice, on a router and on a route beneath it or
// twice at the same site, is not a mistake: an operation legitimately fails
// in more than one way, and Throws has no status for two declarations to
// collide over the way Responds does — it simply accumulates.
func Throws[T any]() Option {
	return newOption("Throws", scopeRouter|scopeRoute, func(m *meta) error {
		if m.throws == nil {
			m.throws = map[reflect.Type]ResponseDoc{}
		}
		t := reflect.TypeFor[T]()
		m.throws[t] = ResponseDoc{Type: t}
		return nil
	})
}

// Use adds middleware, which runs outermost-first in the order declared, with
// an application's middleware outside a router's and a router's outside a
// route's.
//
// The type is the ecosystem's own func(http.Handler) http.Handler, so
// anything already written for net/http composes without adaptation.
func Use(mw ...Middleware) Option {
	return newOption("Use", scopeApp|scopeRouter|scopeRoute, func(m *meta) error {
		for i, f := range mw {
			if f == nil {
				return fmt.Errorf("middleware %d is nil", i)
			}
		}
		m.middleware = append(m.middleware, mw...)
		return nil
	})
}

// Clock replaces the source of the timestamps stamped onto error responses.
//
// Its reason to exist is testing: an error body carries the moment it was
// written, which a golden file cannot match unless the test can hold time
// still.
func Clock(now func() time.Time) Option {
	return newOption("Clock", scopeApp, func(m *meta) error {
		if now == nil {
			return fmt.Errorf("clock must not be nil")
		}
		m.now = now
		return nil
	})
}

// appendUnique adds each value that is not already present, preserving the
// order things were declared in. Tags read best in the order the author wrote
// them, which is why this is not a sorted set.
func appendUnique(dst []string, values ...string) []string {
	for _, v := range values {
		if !slices.Contains(dst, v) {
			dst = append(dst, v)
		}
	}
	return dst
}
