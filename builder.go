package tork

import (
	"fmt"
	"reflect"
)

// Input is a declared input struct. It is returned by DefineInput so the
// declaration has something to be assigned to, in the shape a package level
// variable wants:
//
//	var listItemsInput = tork.DefineInput(...)
//
// Nothing has to be done with the value. Declaring it is what registers the
// struct, and handlers name the struct rather than this.
type Input[T any] struct {
	spec *inputSpec
}

// DefineInput declares how a struct is read from a request, in code rather
// than in struct tags.
//
//	type ListItemsInput struct {
//	    Page   int
//	    Limit  int
//	    Search string
//	}
//
//	var listItemsInput = tork.DefineInput(func(b *tork.InputBuilder, in *ListItemsInput) {
//	    b.Query.Int(&in.Page, "page").Default(1).Min(1)
//	    b.Query.Int(&in.Limit, "limit").Default(20).Range(1, 100)
//	    b.Query.String(&in.Search, "search").MaxLen(200)
//	})
//
// The struct stays plain data, and every name, default, and rule becomes a
// typed call the compiler checks. Passing &in.Page to String is a type error,
// MaxLen does not exist on an integer, and a default that is not the field's
// type will not compile — none of which a tag can promise.
//
// The struct given to the callback is a template that exists only to have its
// fields' addresses taken; it is never the value a handler receives. Which
// field each pointer names is worked out once, here, by comparing addresses.
//
// Handlers are unchanged: a handler takes ListItemsInput, and the framework
// finds this declaration by the struct's type.
func DefineInput[T any](build func(b *InputBuilder, in *T)) *Input[T] {
	template := new(T)
	spec := &inputSpec{typ: reflect.TypeFor[T]()}

	builder := &InputBuilder{spec: spec, template: reflect.ValueOf(template).Elem()}
	if spec.typ.Kind() != reflect.Struct {
		spec.errs = append(spec.errs, fmt.Errorf("tork.DefineInput: %s is not a struct", spec.typ))
	} else {
		builder.Path = &Source{kind: sourcePath, builder: builder}
		builder.Query = &Source{kind: sourceQuery, builder: builder}
		builder.Header = &Source{kind: sourceHeader, builder: builder}
		builder.Cookie = &Source{kind: sourceCookie, builder: builder}
		builder.Form = &Source{kind: sourceForm, builder: builder}
		build(builder, template)
		builder.checkComplete()
	}

	registerInput(spec)
	return &Input[T]{spec: spec}
}

// InputBuilder declares where each field of an input struct comes from.
//
// The five sources are fields rather than methods so that typing b. offers
// them, and typing b.Query. offers the field types it can read — which is the
// whole point of declaring an input this way.
type InputBuilder struct {
	// Path reads the wildcards of the route the handler serves.
	Path *Source
	// Query reads the URL's query string.
	Query *Source
	// Header reads request headers. A header name is always written out.
	Header *Source
	// Cookie reads request cookies.
	Cookie *Source
	// Form reads a urlencoded or multipart request body.
	Form *Source

	spec     *inputSpec
	template reflect.Value
	claimed  map[string]bool
}

// JSONBody declares that a field of the input struct is the request body.
//
//	b.JSONBody(&in.Body)
//
// Rules for the body's own fields are declared separately, with DefineBody,
// because they belong to the body type wherever it is used.
func (b *InputBuilder) JSONBody(field any) {
	index, ok := b.indexOf(field, "tork.InputBuilder.JSONBody")
	if !ok {
		return
	}
	if b.spec.body != nil {
		b.fail("tork.InputBuilder.JSONBody: a request has one body, and one is already declared")
		return
	}
	b.spec.body = &bodySpec{index: index, typ: fieldTypeAt(b.spec.typ, index)}
}

// add records one declared field and returns the spec so a param builder can
// keep adding to it.
func (b *InputBuilder) add(kind source, field any, name, method string) *fieldSpec {
	index, ok := b.indexOf(field, method)
	if !ok {
		// A spec nobody can write to still has to be returned, so that the
		// chained rule calls after this one have somewhere to go.
		return &fieldSpec{}
	}

	b.spec.fields = append(b.spec.fields, fieldSpec{
		index:     index,
		fieldName: b.spec.typ.FieldByIndex(index).Name,
		source:    kind,
		name:      name,
	})
	return &b.spec.fields[len(b.spec.fields)-1]
}

// indexOf works out which field a pointer names.
//
// The pointer came from &in.Page on the caller's template, so the field it
// names is the one whose address matches. That is what lets the declaration be
// a typed expression instead of a string: the compiler has already checked that
// in.Page exists and is the right type by the time this runs.
func (b *InputBuilder) indexOf(field any, method string) ([]int, bool) {
	pointer := reflect.ValueOf(field)
	if pointer.Kind() != reflect.Pointer || pointer.IsNil() {
		b.fail("%s: expected a pointer to a field of %s", method, b.spec.typ)
		return nil, false
	}

	index, found := findFieldByAddress(b.template, pointer.Pointer(), pointer.Type().Elem(), nil)
	if !found {
		b.fail("%s: the pointer does not name a field of %s; "+
			"it has to be written as &in.Field on the struct the builder was given", method, b.spec.typ)
		return nil, false
	}

	if b.claimed == nil {
		b.claimed = map[string]bool{}
	}
	key := fmt.Sprint(index)
	if b.claimed[key] {
		b.fail("%s: field %s is declared twice", method, b.spec.typ.FieldByIndex(index).Name)
		return nil, false
	}
	b.claimed[key] = true

	return index, true
}

// findFieldByAddress walks a struct looking for the field a pointer names.
//
// The address alone is not enough. A struct begins at the same address as its
// first field, so &in.Body and &in.Body.Name are the same number, and an
// embedded marker of no size shares an address with whatever follows it. The
// pointer's own type is what settles it: &in.Body is a *Body and &in.Body.Name
// is a *string, and only one field is both at that address and of that type.
func findFieldByAddress(value reflect.Value, target uintptr, want reflect.Type, prefix []int) ([]int, bool) {
	for i := range value.NumField() {
		field := value.Field(i)
		if !value.Type().Field(i).IsExported() {
			continue
		}
		// A zero-sized field shares its address with whatever follows it,
		// so an embedded marker like JSONBody would answer to the address
		// of the first real field. Nothing can be bound into one anyway.
		if field.Type().Size() == 0 {
			continue
		}

		index := append(append([]int{}, prefix...), i)
		if field.Addr().Pointer() == target && field.Type() == want {
			return index, true
		}
		if field.Kind() == reflect.Struct {
			if found, ok := findFieldByAddress(field, target, want, index); ok {
				return found, true
			}
		}
	}
	return nil, false
}

// checkComplete refuses a declaration that left a field unaccounted for.
//
// With struct tags an untagged field is a mistake the walk trips over. Here
// nothing would trip: a field nobody mentioned would simply stay zero forever,
// which is the one way this form could be quieter than tags. So it is checked.
func (b *InputBuilder) checkComplete() {
	declared := map[string]bool{}
	for _, field := range b.spec.fields {
		declared[b.spec.typ.FieldByIndex(field.index).Name] = true
	}
	if b.spec.body != nil && len(b.spec.body.index) > 0 {
		declared[b.spec.typ.FieldByIndex(b.spec.body.index).Name] = true
	}

	for i := range b.spec.typ.NumField() {
		field := b.spec.typ.Field(i)
		if !field.IsExported() || declared[field.Name] {
			continue
		}
		if field.Anonymous && field.Type.Kind() == reflect.Struct && anyDeclaredUnder(b.spec, i) {
			continue
		}
		b.fail("field %s of %s has no binding declared", field.Name, b.spec.typ)
	}
}

// anyDeclaredUnder reports whether an embedded struct had any of its own
// fields declared, so that embedding one is not itself reported as a gap.
func anyDeclaredUnder(spec *inputSpec, at int) bool {
	for _, field := range spec.fields {
		if len(field.index) > 1 && field.index[0] == at {
			return true
		}
	}
	return false
}

// fail records a mistake for the application build to report. A builder runs
// during package initialisation, where returning an error is not possible and
// panicking would take the process down over something a test should be able
// to assert on.
func (b *InputBuilder) fail(format string, args ...any) {
	b.spec.errs = append(b.spec.errs, fmt.Errorf(format, args...))
}

// Source is one place a value can be read from, and the door to the typed
// field builders.
//
// Every method takes a pointer to the field it fills, which is what makes the
// declaration type-safe: String will not accept a *int, and the builder it
// returns has only the rules that field's type can have.
type Source struct {
	kind    source
	builder *InputBuilder
}

func (s *Source) declare(field any, name, method string) *fieldSpec {
	return s.builder.add(s.kind, field, name, method)
}
