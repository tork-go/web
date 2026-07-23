package tork

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
)

var (
	contextType = reflect.TypeFor[context.Context]()
	errorType   = reflect.TypeFor[error]()
)

// handlerPlan is a handler reduced to what serving it takes: the function, a
// binder per parameter, and the type it returns.
//
// Building one is the only time the framework looks at a handler's type. Once
// it exists, serving a request means calling the binders and calling the
// function, so no request pays for the signature being convenient.
type handlerPlan struct {
	fn     reflect.Value
	params []paramBinder

	// result is the type of the value the handler returns beside its error,
	// and nil for a handler that returns only an error. It is what the
	// encoder and the OpenAPI document are built from.
	result reflect.Type

	// deps are the request-scoped dependencies, in the order they run, and
	// slots is how many request-scoped values they produce. A request
	// allocates a slot array of that size and the steps fill it before the
	// handler is called, so a handler reading an injected request value reads
	// a slot already written.
	deps  []depStep
	slots int

	// bound and body are what the route reads from a request, recorded as it
	// was compiled so the OpenAPI document can describe it without deriving
	// any of it a second time. Nothing reads them while a request is served.
	bound []docParam
	body  *docBody
}

// paramBinder produces one argument for a handler call. Everything a
// parameter can be — the context, a decoded request struct, an injected
// service — is one of these, so invoke never learns the difference.
type paramBinder func(*exchange) (reflect.Value, error)

// compileHandler classifies a handler's signature and builds its plan.
//
// A handler is an ordinary function, so what the framework can do with one is
// decided entirely here: every parameter has to be something it knows how to
// produce, and the results have to end in an error. Anything else is a
// mistake in the caller's source, reported with the type that caused it.
func compileHandler(route *Route, inj *injector) (*handlerPlan, error) {
	if route.handler == nil {
		return nil, fmt.Errorf("handler is nil")
	}
	fn := reflect.ValueOf(route.handler)
	if fn.Kind() != reflect.Func {
		return nil, fmt.Errorf("handler is a %s, not a function", fn.Type())
	}

	fnType := fn.Type()
	if fnType.IsVariadic() {
		return nil, fmt.Errorf("handler is variadic, which leaves the framework nothing to pass")
	}

	rc := newRouteCompiler(route, inj)
	plan := &handlerPlan{fn: fn}

	// The dependencies run before the handler and in the order declared, so
	// they are compiled first: a later one, or the handler, can then read a
	// request-scoped value an earlier one produced.
	for i := range route.dependencies {
		step, err := compileDep(rc, route.dependencies[i])
		if err != nil {
			return nil, err
		}
		plan.deps = append(plan.deps, step)
	}

	compiler := newHandlerCompiler(rc)
	for i := range fnType.NumIn() {
		binder, err := compiler.param(fnType.In(i))
		if err != nil {
			return nil, fmt.Errorf("parameter %d: %w", i, err)
		}
		plan.params = append(plan.params, binder)
	}

	result, spec, err := compileResults(fnType)
	if err != nil {
		return nil, err
	}
	plan.result = result
	plan.slots = rc.slots
	plan.bound, plan.body = rc.params, rc.body
	route.ResponseSpec = spec

	return plan, nil
}

// param decides what one parameter is and how to produce it.
//
// The order is the order of certainty. The two framework types are recognised
// by identity, then a struct is asked whether it describes a request, and only
// what is left over is a dependency — which is the right way round, because a
// dependency is defined by what it is not.
func (c *handlerCompiler) param(t reflect.Type) (paramBinder, error) {
	switch t {
	case contextType:
		return bindContext, nil
	case requestType:
		return bindRequest, nil
	}

	spec, err := specFor(t)
	if err != nil {
		return nil, err
	}
	if spec != nil {
		plan, err := c.compileSpec(spec)
		if err != nil {
			return nil, err
		}
		return plan.bind, nil
	}

	if idx, ok := c.rc.inj.singletonIndex[t]; ok {
		return func(ex *exchange) (reflect.Value, error) {
			return ex.srv.singletons[idx], nil
		}, nil
	}
	if slot, ok := c.rc.scoped[t]; ok {
		return func(ex *exchange) (reflect.Value, error) {
			return ex.slots[slot], nil
		}, nil
	}
	if _, ok := c.rc.inj.transientByType[t]; ok {
		build := c.rc.inj.transientBuilder(t)
		return func(ex *exchange) (reflect.Value, error) {
			return build(ex.srv)
		}, nil
	}

	return nil, fmt.Errorf("nothing provides %s; take it from a tork.Provide or a tork.Depends", t)
}

// bindContext hands the handler the request's context.
func bindContext(ex *exchange) (reflect.Value, error) {
	return reflect.ValueOf(ex.request.Context()), nil
}

// bindRequest hands the handler the request itself.
func bindRequest(ex *exchange) (reflect.Value, error) {
	return reflect.ValueOf(&Request{request: ex.request, writer: ex.writer}), nil
}

// compileResults checks the return signature and reports the success type
// and, when that type answers for itself, the ResponseSpec it answers with.
//
// Handlers return (T, error) or error alone. The error is last because that
// is where Go puts it, and it is required because a handler that cannot fail
// is rare enough that letting it say so would cost every other handler a
// second shape to remember.
func compileResults(fnType reflect.Type) (reflect.Type, *ResponseSpec, error) {
	switch fnType.NumOut() {
	case 1:
		if fnType.Out(0) != errorType {
			return nil, nil, fmt.Errorf("returns %s alone; a handler returns (T, error) or error", fnType.Out(0))
		}
		return nil, nil, nil
	case 2:
		if fnType.Out(1) != errorType {
			return nil, nil, fmt.Errorf("returns (%s, %s); the second result must be error", fnType.Out(0), fnType.Out(1))
		}
		if fnType.Out(0) == errorType {
			return nil, nil, fmt.Errorf("returns (error, error); a handler returns (T, error) or error")
		}
		t := fnType.Out(0)
		spec, err := responderSpec(t)
		if err != nil {
			return nil, nil, err
		}
		return t, spec, nil
	default:
		return nil, nil, fmt.Errorf("returns %d values; a handler returns (T, error) or error", fnType.NumOut())
	}
}

// responderSpec reports what t promises about its response when it
// implements Responder, and nil when t is a plain success type that still
// serializes as JSON with status 200.
//
// The case where *t implements Responder but t does not is worth naming on
// its own: without it, a handler that returns items.Created by value when
// only *items.Created satisfies Responder would fail with nothing more than
// "cannot supply", which does not say what is wrong.
func responderSpec(t reflect.Type) (*ResponseSpec, error) {
	if t.Implements(responderType) {
		spec := reflect.New(t).Elem().Interface().(Responder).Spec()
		return &spec, nil
	}
	if reflect.PointerTo(t).Implements(responderType) {
		return nil, fmt.Errorf("result type %s implements tork.Responder on *%s, not %s; return a pointer", t, t, t)
	}
	return nil, nil
}

// serveRequest runs the request-scoped dependencies, then the handler.
//
// The dependencies run first, in order, and any one of them failing stops the
// request before the handler — a guard that refuses is meant to, and a value
// dependency that fails leaves the handler nothing to be called with. Only
// once they have all run, filling the slots the handler reads from, is the
// handler itself invoked.
func (p *handlerPlan) serveRequest(ex *exchange) (any, error) {
	for i := range p.deps {
		if err := p.deps[i].run(ex); err != nil {
			return nil, err
		}
	}
	return p.invoke(ex)
}

// invoke calls the handler for one request, returning the value it produced
// or the error it failed with.
//
// Field problems from every parameter are gathered before giving up, so a
// handler taking a path struct and a query struct reports what is wrong with
// both. Any other failure — a body of the wrong media type, a dependency that
// could not be built — ends the request where it happened, because nothing
// after it would be meaningful.
func (p *handlerPlan) invoke(ex *exchange) (any, error) {
	args := make([]reflect.Value, len(p.params))
	var invalid fieldErrors

	for i, bind := range p.params {
		arg, err := bind(ex)
		if err == nil {
			args[i] = arg
			continue
		}

		var rejected fieldErrors
		if !errors.As(err, &rejected) {
			return nil, err
		}
		invalid = append(invalid, rejected...)
		// The call will not happen, but the loop continues, so the
		// argument only has to be something Call would accept.
		args[i] = reflect.Zero(p.fn.Type().In(i))
	}

	if len(invalid) > 0 {
		return nil, invalid
	}

	out := p.fn.Call(args)
	if failure := out[len(out)-1]; !failure.IsNil() {
		return nil, failure.Interface().(error)
	}
	if p.result == nil {
		return nil, nil
	}
	return out[0].Interface(), nil
}

// derivedOperationID names an operation that did not name itself.
//
// The handler's own function name is the best default available: it is what
// the author already chose, it is stable as long as the function is, and it
// reads as "items.readItem" rather than as a path mangled into an identifier.
// A closure has no name worth using, and neither has a handler that turned
// out not to be a function at all — that is reported elsewhere — so both fall
// back to the route itself, which is always unique because a duplicate
// method and path is refused anyway.
func derivedOperationID(handler any, method, path string) string {
	if name := functionName(handler); name != "" {
		return name
	}
	slug := strings.NewReplacer("/", ".", "{", "", "}", "").Replace(strings.TrimPrefix(path, "/"))
	if slug == "" {
		return strings.ToLower(method)
	}
	return strings.ToLower(method) + "." + slug
}

// functionName is the handler's name as "package.Func", or "" when it has
// none worth showing.
func functionName(handler any) string {
	if handler == nil {
		return ""
	}
	fn := reflect.ValueOf(handler)
	if fn.Kind() != reflect.Func {
		return ""
	}

	full := runtime.FuncForPC(fn.Pointer()).Name()
	if i := strings.LastIndex(full, "/"); i >= 0 {
		full = full[i+1:]
	}
	// A method value is compiled to a wrapper named "...Read-fm"; the
	// suffix is an implementation detail nobody wants in their client.
	full = strings.TrimSuffix(full, "-fm")
	// A function literal is named after the function that contains it,
	// which says nothing about the operation.
	if full == "" || strings.Contains(full, ".func") {
		return ""
	}
	return full
}
