package tork

import (
	"context"
	"fmt"
	"reflect"
)

// Cleanup releases what a provider or a dependency acquired — a connection to
// close, a file to remove, a span to end.
//
// It is a defined type rather than a plain func so that a constructor's middle
// return value is recognised by identity: func(deps) (T, tork.Cleanup, error)
// is told apart from func(deps) (T, error) by the type of the second result,
// not by counting how the third one is spelled. It takes a context because
// the shutdown it runs under has a deadline, and a cleanup that ignores it can
// hang a graceful stop.
type Cleanup func(context.Context) error

var cleanupType = reflect.TypeFor[Cleanup]()

// providerKind is which of the four shapes a provider was written in. The
// three constructor kinds differ only in what they return beside the value,
// which is what construction reads to know where the value, the cleanup, and
// the error each are.
type providerKind uint8

const (
	providerValue     providerKind = iota // ProvideValue: a value already built
	providerCtor                          // func(deps...) T
	providerCtorErr                       // func(deps...) (T, error)
	providerCtorClean                     // func(deps...) (T, Cleanup, error)
)

// provider is one Provide or ProvideValue argument, reduced to what the graph
// needs: the type it produces, how to produce it, what it needs first, and
// where it was written so an error can point at the line.
//
// A malformed provider is not rejected here. reflectProvider records the
// complaint in badShape and analyzeGraph reports it beside every other build
// error, the same bargain applyOptions makes with a misplaced option, so one
// build surfaces every mistake rather than stopping at the first constructor
// that was not a function.
type provider struct {
	out       reflect.Type   // the type produced
	fn        reflect.Value  // the constructor, or the zero Value for a value provider
	value     reflect.Value  // ProvideValue's prebuilt value
	deps      []reflect.Type // the constructor's parameter types, in order
	kind      providerKind
	transient bool   // built fresh at each injection rather than once and shared
	site      string // the Provide/ProvideValue call line

	// index is where this provider's built value lives in the server's
	// singleton slice, assigned in topological order so a provider's
	// dependencies always have lower indices than it does.
	index int

	// badShape is the reason this thing cannot be a provider, or nil. It is
	// carried rather than returned so that a second mistake elsewhere is still
	// found in the same build.
	badShape error
}

// providerSet is every provider declared anywhere in the application, in the
// order declared.
//
// It is one flat list rather than a per-router collection because a provider
// is visible application-wide: a repository declared under /items can be
// injected into a handler under /users, since a service graph is a property of
// the program, not of a URL prefix. meta carries a pointer to one shared set
// (see meta.providers) precisely so a Provide on any router lands here, which
// inherited's downward-only copy could not achieve on its own.
type providerSet struct {
	all []provider
}

func (ps *providerSet) add(p provider) {
	ps.all = append(ps.all, p)
}

// Provide registers constructors, valid on an application and on a router. Each
// is a function returning the value it provides, in one of three shapes:
//
//	func(deps...) T
//	func(deps...) (T, error)
//	func(deps...) (T, tork.Cleanup, error)
//
// A constructor's own parameters are resolved from the graph, so constructors
// compose: NewService(NewRepository()) is written as two providers that each
// name only what they need. Everything Provide builds is a singleton —
// constructed once, when the application builds, and shared by every request —
// because a service is a thing the program has, not a thing a request makes.
// For a value already in hand rather than a recipe for one, see ProvideValue;
// for one rebuilt at each use, tork.Transient.
func Provide(constructors ...any) Option {
	site := callerSite(2)
	return newOption("Provide", scopeApp|scopeRouter, func(m *meta) error {
		for _, c := range constructors {
			if marker, ok := c.(transientMarker); ok {
				p := reflectProvider(marker.constructor, site)
				p.transient = true
				m.providers.add(p)
				continue
			}
			m.providers.add(reflectProvider(c, site))
		}
		return nil
	})
}

// transientMarker is what Transient wraps a constructor in so Provide can tell
// a transient one from a singleton without a second registration function.
type transientMarker struct{ constructor any }

// Transient marks a constructor, inside Provide, as one that builds a fresh
// value at each place it is injected instead of a single shared one.
//
//	tork.Provide(NewRepository, tork.Transient(NewRequestScratch))
//
// It is the rare case: most services are shared for the life of the
// application or for the life of a request, and only a value that must not be
// aliased — a builder two callers would corrupt, a buffer each use needs its
// own of — wants a new instance every time. A transient is built from the
// singletons in the graph, not from anything request-scoped, and cannot
// register a tork.Cleanup: its lifetime is the injection, not the application,
// so there is no shutdown for one to run at.
func Transient(constructor any) any {
	return transientMarker{constructor: constructor}
}

// ProvideValue registers a value that is already built — an opened database
// handle, a parsed configuration, a client constructed in main — as a
// singleton of its own type.
//
// It exists so that a value in hand does not have to be wrapped in a
// constructor that only returns it: ProvideValue(db) says what Provide(func()
// *sql.DB { return db }) would, without the ceremony. The type it provides is
// the dynamic type of v, so a value passed as an interface provides that
// interface's dynamic type, not the interface — declare a constructor
// returning the interface when the interface is what a handler asks for.
func ProvideValue(v any) Option {
	site := callerSite(2)
	return newOption("ProvideValue", scopeApp|scopeRouter, func(m *meta) error {
		m.providers.add(provider{
			out:   reflect.TypeOf(v),
			value: reflect.ValueOf(v),
			kind:  providerValue,
			site:  site,
		})
		return nil
	})
}

// reflectProvider classifies a constructor into a provider, recording why it is
// not one rather than returning an error, so the reason travels with it to
// where every build error is reported together.
func reflectProvider(c any, site string) provider {
	p := provider{site: site}
	if c == nil {
		p.badShape = fmt.Errorf("a provider is nil, not a function")
		return p
	}
	fn := reflect.ValueOf(c)
	t := fn.Type()
	if t.Kind() != reflect.Func {
		p.badShape = fmt.Errorf("a provider is a %s, not a function", t)
		return p
	}
	if t.IsVariadic() {
		p.badShape = fmt.Errorf("a provider is variadic, which leaves the graph nothing to resolve")
		return p
	}

	p.fn = fn
	for i := range t.NumIn() {
		p.deps = append(p.deps, t.In(i))
	}

	switch t.NumOut() {
	case 1:
		if t.Out(0) == errorType {
			p.badShape = fmt.Errorf("a function returning only an error is a tork.Depends guard, not a provider; a provider produces a value")
			return p
		}
		p.out, p.kind = t.Out(0), providerCtor
	case 2:
		if t.Out(1) != errorType {
			p.badShape = fmt.Errorf("the second result of a provider must be error, not %s", t.Out(1))
			return p
		}
		p.out, p.kind = t.Out(0), providerCtorErr
	case 3:
		if t.Out(1) != cleanupType {
			p.badShape = fmt.Errorf("the second result of a three-result provider must be tork.Cleanup, not %s", t.Out(1))
			return p
		}
		if t.Out(2) != errorType {
			p.badShape = fmt.Errorf("the third result of a provider must be error, not %s", t.Out(2))
			return p
		}
		p.out, p.kind = t.Out(0), providerCtorClean
	default:
		p.badShape = fmt.Errorf("a provider returns %d values; a provider returns T, (T, error), or (T, tork.Cleanup, error)", t.NumOut())
	}
	return p
}

// splitProviderReturn separates a constructor's results into the value, an
// optional cleanup, and an optional error, reading the positions its kind
// fixed at build. A value provider never reaches here: it has nothing to call.
func splitProviderReturn(p *provider, out []reflect.Value) (reflect.Value, Cleanup, error) {
	var cleanup Cleanup
	switch p.kind {
	case providerCtorErr:
		if e := out[1]; !e.IsNil() {
			return reflect.Value{}, nil, e.Interface().(error)
		}
	case providerCtorClean:
		if e := out[2]; !e.IsNil() {
			return reflect.Value{}, nil, e.Interface().(error)
		}
		if c := out[1]; !c.IsNil() {
			cleanup = c.Interface().(Cleanup)
		}
	}
	return out[0], cleanup, nil
}
