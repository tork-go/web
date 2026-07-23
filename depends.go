package tork

import (
	"errors"
	"fmt"
	"reflect"
)

// dependencyDecl is one Depends as written: the function, and where it was
// written so a mistake in it can point at the line.
type dependencyDecl struct {
	fn   any
	site string
}

// Depends registers a request-scoped dependency, valid on an application, a
// router, and a route.
//
// The function runs before the handler, once per request, and takes exactly
// what a handler takes — a context, the request, an input struct, an injected
// service, or a value an outer dependency produced — so it can read a header
// or a token without a second mechanism for doing so. What it returns decides
// what it is: a value becomes a request-scoped parameter any handler or later
// dependency under it can ask for by type, and a function that returns only an
// error is a guard, which runs for its effect and contributes nothing but the
// power to refuse the request.
//
//	tork.Depends(auth.RequireToken)   // (Principal, error): authenticates and provides
//	tork.Depends(audit.Record)        // error: runs for effect, refuses on failure
//
// Dependencies declared further out run first — the application's, then a
// router's, then a route's — because an inner one may rely on what an outer
// one established. A guard that returns an error stops the request there, and
// that error travels through the same envelope a handler's would.
func Depends(fn any) Option {
	site := callerSite(2)
	return newOption("Depends", scopeApp|scopeRouter|scopeRoute, func(m *meta) error {
		m.dependencies = append(m.dependencies, dependencyDecl{fn: fn, site: site})
		return nil
	})
}

// depStep is one dependency compiled: how to build its arguments, and where in
// its results the value, the cleanup, and the error each are — or -1 for a
// result it does not have.
type depStep struct {
	fn           reflect.Value
	params       []paramBinder
	slot         int // where its value lands on the exchange, or -1 for a guard
	valueIndex   int
	cleanupIndex int
	errIndex     int
}

// compileDep classifies one dependency: its parameters through the same
// machinery a handler's go through, and its results into the value, cleanup,
// and error a request will read. A value-producing dependency is assigned a
// slot and registered as request-scoped afterwards, so it is visible to later
// dependencies and the handler but not to itself.
func compileDep(rc *routeCompiler, decl dependencyDecl) (depStep, error) {
	if decl.fn == nil {
		return depStep{}, fmt.Errorf("%s: a dependency is nil, not a function", decl.site)
	}
	fn := reflect.ValueOf(decl.fn)
	t := fn.Type()
	if t.Kind() != reflect.Func {
		return depStep{}, fmt.Errorf("%s: a dependency is a %s, not a function", decl.site, t)
	}
	if t.IsVariadic() {
		return depStep{}, fmt.Errorf("%s: a dependency is variadic, which leaves the framework nothing to pass", decl.site)
	}

	c := newHandlerCompiler(rc)
	step := depStep{fn: fn, slot: -1, valueIndex: -1, cleanupIndex: -1, errIndex: -1}
	for i := range t.NumIn() {
		binder, err := c.param(t.In(i))
		if err != nil {
			return depStep{}, fmt.Errorf("%s: a dependency's parameter %d: %w", decl.site, i, err)
		}
		step.params = append(step.params, binder)
	}

	out, valueIndex, cleanupIndex, errIndex, err := classifyDepReturn(t)
	if err != nil {
		return depStep{}, fmt.Errorf("%s: %w", decl.site, err)
	}
	step.valueIndex, step.cleanupIndex, step.errIndex = valueIndex, cleanupIndex, errIndex

	if out != nil {
		step.slot = rc.slots
		rc.slots++
		rc.scoped[out] = step.slot
	}
	return step, nil
}

// classifyDepReturn works out where a dependency's value, cleanup, and error
// are among its results, or reports that its results are not a shape a
// dependency may have. A nil value type means a guard: it produces nothing.
func classifyDepReturn(t reflect.Type) (value reflect.Type, valueIndex, cleanupIndex, errIndex int, err error) {
	switch t.NumOut() {
	case 1:
		if t.Out(0) == errorType {
			return nil, -1, -1, 0, nil
		}
		return t.Out(0), 0, -1, -1, nil
	case 2:
		if t.Out(1) != errorType {
			return nil, 0, 0, 0, fmt.Errorf("the second result of a dependency must be error, not %s", t.Out(1))
		}
		return t.Out(0), 0, -1, 1, nil
	case 3:
		if t.Out(1) != cleanupType {
			return nil, 0, 0, 0, fmt.Errorf("the second result of a three-result dependency must be tork.Cleanup, not %s", t.Out(1))
		}
		if t.Out(2) != errorType {
			return nil, 0, 0, 0, fmt.Errorf("the third result of a dependency must be error, not %s", t.Out(2))
		}
		return t.Out(0), 0, 1, 2, nil
	default:
		return nil, 0, 0, 0, fmt.Errorf("a dependency returns %d values; a dependency returns error, T, (T, error), or (T, tork.Cleanup, error)", t.NumOut())
	}
}

// run builds the dependency's arguments, calls it, and records what it
// produced: its value into its slot for a later reader, its cleanup onto the
// request's stack, and its error back to the caller to stop the request.
//
// A parameter that rejects the request for a field problem is gathered like a
// handler's are, so a dependency reading three bad headers reports three; any
// other failure ends the dependency where it happened. A dependency that
// returns an error runs no further and the handler never runs.
func (d *depStep) run(ex *exchange) error {
	args := make([]reflect.Value, len(d.params))
	var invalid fieldErrors
	for i, bind := range d.params {
		arg, err := bind(ex)
		if err != nil {
			var rejected fieldErrors
			if !errors.As(err, &rejected) {
				return err
			}
			invalid = append(invalid, rejected...)
			continue
		}
		args[i] = arg
	}
	if len(invalid) > 0 {
		return invalid
	}

	out := d.fn.Call(args)
	if d.errIndex >= 0 {
		if e := out[d.errIndex]; !e.IsNil() {
			return e.Interface().(error)
		}
	}
	if d.slot >= 0 {
		ex.slots[d.slot] = out[d.valueIndex]
	}
	if d.cleanupIndex >= 0 {
		if c := out[d.cleanupIndex]; !c.IsNil() {
			ex.cleanups = append(ex.cleanups, c.Interface().(Cleanup))
		}
	}
	return nil
}
