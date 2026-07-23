package tork

import (
	"context"
	"fmt"
	"reflect"
	"strings"
)

// injector is the provider graph after it has been checked and ordered: which
// provider builds each type, where each built value lives, and the order to
// build them in so a provider's dependencies exist before it does.
//
// It is produced once, when the application builds, and read from there on.
// At request time nothing consults it: a handler's injected parameter was
// already resolved to a slice index, so serving pays for none of this.
type injector struct {
	byType          map[reflect.Type]*provider // the provider for each type, deduplicated
	singletonIndex  map[reflect.Type]int       // singleton type → index in the server's singleton slice
	transientByType map[reflect.Type]*provider // transient type → its provider, built on demand
	order           []*provider                // singleton build order: dependencies first
}

// analyzeGraph turns a flat provider set into a checked, ordered graph, or
// reports every reason it cannot. Errors are collected rather than returned
// one at a time so that a build surfaces all of a graph's problems together,
// the same bargain the rest of the build makes.
func analyzeGraph(ps *providerSet) (*injector, []error) {
	inj := &injector{
		byType:          map[reflect.Type]*provider{},
		singletonIndex:  map[reflect.Type]int{},
		transientByType: map[reflect.Type]*provider{},
	}
	var errs []error

	// Shape and duplicate: a malformed provider names why it is malformed, a
	// second provider for a type names the first. The first declared wins the
	// type, so a later stage never mistakes the loser's absence for "nothing
	// provides this". A transient that registers a cleanup is reported here
	// but still recorded, so a handler that uses it is not also told nothing
	// provides it.
	for i := range ps.all {
		p := &ps.all[i]
		if p.badShape != nil {
			errs = append(errs, fmt.Errorf("%s: %w", p.site, p.badShape))
			continue
		}
		if p.transient && p.kind == providerCtorClean {
			errs = append(errs, fmt.Errorf("%s: a transient provider cannot register a tork.Cleanup; its lifetime is the injection, not the application", p.site))
		}
		if first, dup := inj.byType[p.out]; dup {
			errs = append(errs, fmt.Errorf("%s: a provider for %s is already declared at %s", p.site, p.out, first.site))
			continue
		}
		inj.byType[p.out] = p
	}

	// Missing and scope: every constructor parameter must be a singleton the
	// graph can build. A parameter that is the request itself is a scope
	// violation — a thing built once at boot cannot depend on a thing that
	// exists only while a request is being served — and everything else the
	// graph does not provide is simply missing.
	for i := range ps.all {
		p := &ps.all[i]
		if p.badShape != nil || inj.byType[p.out] != p {
			continue
		}
		for _, d := range p.deps {
			if _, ok := inj.byType[d]; ok {
				continue
			}
			if isRequestScoped(d) {
				errs = append(errs, fmt.Errorf("%s: the provider for %s takes %s, which exists only per request; a singleton cannot depend on the request", p.site, p.out, d))
			} else {
				errs = append(errs, fmt.Errorf("%s: the provider for %s needs %s, which nothing provides; declare it with tork.Provide", p.site, p.out, d))
			}
		}
	}

	// A cycle has no build order at all, so it is found before ordering is
	// attempted and ordering is skipped when one exists.
	cycleErrs := inj.detectCycles(ps)
	errs = append(errs, cycleErrs...)
	if len(cycleErrs) > 0 {
		return inj, errs
	}

	inj.topoSort(ps)
	return inj, errs
}

// isRequestScoped reports whether a type can only be produced while a request
// is in flight — the context, the raw request, or an input struct decoded from
// one — which is what makes it illegal for a singleton to depend on.
func isRequestScoped(t reflect.Type) bool {
	if t == contextType || t == requestType {
		return true
	}
	spec, _ := specFor(t)
	return spec != nil
}

// detectCycles walks the graph depth-first and reports each cycle as the path
// that closes it, so "*items.Service -> items.Repository -> *items.Service"
// names every type a reader has to break the loop at, not just that one
// exists.
func (inj *injector) detectCycles(ps *providerSet) []error {
	const (
		white = iota // not yet visited
		gray         // on the current path
		black        // finished
	)
	color := map[reflect.Type]int{}
	var stack []reflect.Type
	var errs []error

	var visit func(t reflect.Type)
	visit = func(t reflect.Type) {
		color[t] = gray
		stack = append(stack, t)
		for _, d := range inj.byType[t].deps {
			if _, ok := inj.byType[d]; !ok {
				continue
			}
			switch color[d] {
			case white:
				visit(d)
			case gray:
				errs = append(errs, cycleError(inj, stack, d))
			}
		}
		stack = stack[:len(stack)-1]
		color[t] = black
	}

	for i := range ps.all {
		p := &ps.all[i]
		if p.badShape != nil || inj.byType[p.out] != p {
			continue
		}
		if color[p.out] == white {
			visit(p.out)
		}
	}
	return errs
}

// cycleError renders the loop from where it re-enters the current path back to
// the edge that closed it.
func cycleError(inj *injector, stack []reflect.Type, head reflect.Type) error {
	start := 0
	for i, t := range stack {
		if t == head {
			start = i
			break
		}
	}
	loop := append(append([]reflect.Type{}, stack[start:]...), head)
	names := make([]string, len(loop))
	for i, t := range loop {
		names[i] = t.String()
	}
	return fmt.Errorf("%s: providers form a cycle: %s", inj.byType[head].site, strings.Join(names, " -> "))
}

// topoSort assigns every provider a build index in dependency order, so that
// construction can walk the order once and always find a provider's
// dependencies already built. It is only called for an acyclic graph, so the
// recursion terminates.
func (inj *injector) topoSort(ps *providerSet) {
	visited := map[reflect.Type]bool{}
	var visit func(p *provider)
	visit = func(p *provider) {
		if visited[p.out] {
			return
		}
		visited[p.out] = true
		for _, d := range p.deps {
			if dep, ok := inj.byType[d]; ok {
				visit(dep)
			}
		}
		// A transient is not built at boot and holds no fixed value, so it is
		// recorded to be built on demand rather than given a singleton slot.
		if p.transient {
			inj.transientByType[p.out] = p
			return
		}
		p.index = len(inj.order)
		inj.singletonIndex[p.out] = p.index
		inj.order = append(inj.order, p)
	}
	for i := range ps.all {
		p := &ps.all[i]
		if p.badShape != nil || inj.byType[p.out] != p {
			continue
		}
		visit(p)
	}
}

// transientBuilder precompiles how to build one transient value: each of its
// arguments resolved once, at build, to a read of a singleton or to a nested
// transient's builder, so constructing one at request time costs no graph
// lookup. The graph is acyclic by the time this runs, so the recursion through
// transient dependencies terminates.
func (inj *injector) transientBuilder(t reflect.Type) func(*server) (reflect.Value, error) {
	p := inj.transientByType[t]
	resolvers := make([]func(*server) (reflect.Value, error), len(p.deps))
	for i, d := range p.deps {
		if idx, ok := inj.singletonIndex[d]; ok {
			resolvers[i] = func(s *server) (reflect.Value, error) { return s.singletons[idx], nil }
		} else {
			resolvers[i] = inj.transientBuilder(d)
		}
	}
	return func(s *server) (reflect.Value, error) {
		args := make([]reflect.Value, len(resolvers))
		for i, resolve := range resolvers {
			v, err := resolve(s)
			if err != nil {
				return reflect.Value{}, err
			}
			args[i] = v
		}
		value, _, err := splitProviderReturn(p, p.fn.Call(args))
		return value, err
	}
}

// constructSingletons builds every singleton once, in dependency order, and
// returns the built values indexed for injection plus the cleanups they
// registered.
//
// It runs when the application builds rather than on first use, so a
// constructor that cannot do its job — a database that will not open — fails
// the boot rather than the first request that happens to need it. A
// constructor that fails unwinds what was already built, in reverse, so a
// half-constructed graph never leaks the resources it did acquire.
func constructSingletons(ctx context.Context, inj *injector) ([]reflect.Value, []Cleanup, error) {
	singletons := make([]reflect.Value, len(inj.order))
	var cleanups []Cleanup
	for _, p := range inj.order {
		if p.kind == providerValue {
			singletons[p.index] = p.value
			continue
		}
		args := make([]reflect.Value, len(p.deps))
		for i, d := range p.deps {
			args[i] = singletons[inj.singletonIndex[d]]
		}
		value, cleanup, err := splitProviderReturn(p, p.fn.Call(args))
		if err != nil {
			runCleanups(ctx, cleanups)
			return nil, nil, fmt.Errorf("%s: building %s: %w", p.site, p.out, err)
		}
		singletons[p.index] = value
		if cleanup != nil {
			cleanups = append(cleanups, cleanup)
		}
	}
	return singletons, cleanups, nil
}

// runCleanups runs cleanups in reverse of the order they were registered, so
// a thing is torn down before whatever it was built on top of, and returns
// every error rather than stopping at the first — a cleanup that fails should
// not strand the ones after it.
func runCleanups(ctx context.Context, cleanups []Cleanup) []error {
	var errs []error
	for i := len(cleanups) - 1; i >= 0; i-- {
		if err := cleanups[i](ctx); err != nil {
			errs = append(errs, err)
		}
	}
	return errs
}
