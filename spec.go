package tork

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"sync"
)

// fieldSpec is one field's declaration, whichever way it was written.
//
// It is the single field model this package promises: struct tags and the
// typed builders are two front ends that both produce these, and everything
// downstream — the decoder, the validator, and in time the OpenAPI document —
// reads only this. Two front ends cannot disagree about what a field means,
// because neither of them is where the meaning lives.
type fieldSpec struct {
	index     []int
	fieldName string
	source    source

	// name is the wire name, or "" to derive it from fieldName.
	name string
	csv  bool

	required   bool
	transforms []transform
	rules      []rule

	// A default arrives as text from a tag, or as an already-typed value
	// from a builder. At most one of these is set.
	defaultText  string
	hasText      bool
	defaultValue reflect.Value
	hasValue     bool

	// file marks an uploaded file, which never passes through a string and
	// so has no decoder.
	file      bool
	fileMulti bool
}

// bodyFieldSpec is one rule-bearing field of a request body. The name comes
// from the field's json tag, because that is what encoding/json already reads
// and there is nothing to gain from saying it twice.
type bodyFieldSpec struct {
	index      []int
	name       string
	required   bool
	transforms []transform
	rules      []rule
}

// bodySpec is the request body of one input.
type bodySpec struct {
	// index is where the body lands in the input struct, and is empty when
	// the parameter is itself the body.
	index []int
	typ   reflect.Type
}

// inputSpec is a whole input struct as declared.
type inputSpec struct {
	typ    reflect.Type
	fields []fieldSpec
	body   *bodySpec

	// errs are mistakes found while the spec was being declared. A builder
	// runs at package initialisation, where there is nobody to return an
	// error to, so they are carried here and reported when the application
	// builds — beside every other mistake, as usual.
	errs []error
}

// registry holds the specs declared by DefineInput and DefineBody.
//
// Lookup is by type, exactly as orm's table registry resolves a relationship
// by its row type, and for the same reason: the handler names the struct, and
// the declaration is somewhere else entirely. Registering at package
// initialisation means every spec exists before an application is built.
var registry = struct {
	sync.RWMutex
	inputs map[reflect.Type]*inputSpec
	bodies map[reflect.Type]*bodyRules
}{
	inputs: map[reflect.Type]*inputSpec{},
	bodies: map[reflect.Type]*bodyRules{},
}

// bodyRules is everything declared about one body type: the rules for its own
// fields, and the checks that need to see the whole document at once.
type bodyRules struct {
	fields []bodyFieldSpec
	whole  []wholeCheck
}

// wholeCheck is a validator that judges a decoded body rather than one field
// of it, which is the only way to say something about two fields at once.
type wholeCheck func(reflect.Value) fieldErrors

func registerInput(spec *inputSpec) {
	registry.Lock()
	defer registry.Unlock()
	registry.inputs[spec.typ] = spec
}

func registerBody(t reflect.Type, rules *bodyRules) {
	registry.Lock()
	defer registry.Unlock()
	registry.bodies[t] = rules
}

func lookupInput(t reflect.Type) (*inputSpec, bool) {
	registry.RLock()
	defer registry.RUnlock()
	spec, ok := registry.inputs[t]
	return spec, ok
}

func lookupBody(t reflect.Type) *bodyRules {
	registry.RLock()
	defer registry.RUnlock()
	return registry.bodies[t]
}

// compileSpec turns a declaration into the plan that serves it.
//
// This is where the two front ends converge and where everything that needs a
// route to check against is finally checked: a path field against the
// wildcards the route actually has, a name against the ones already claimed,
// a default against the type it has to fit.
func (c *handlerCompiler) compileSpec(spec *inputSpec) (*inputPlan, error) {
	if len(spec.errs) > 0 {
		return nil, spec.errs[0]
	}

	plan := &inputPlan{typ: spec.typ}
	for i := range spec.fields {
		if err := c.compileField(plan, &spec.fields[i]); err != nil {
			return nil, err
		}
	}

	if spec.body != nil {
		if declErrs := lookupBodyErrors(spec.body.typ); len(declErrs) > 0 {
			return nil, declErrs[0]
		}
		if err := c.claimBody(spec.body.typ); err != nil {
			return nil, err
		}
		plan.body = &bodyBinder{
			index:  spec.body.index,
			typ:    spec.body.typ,
			checks: compileBodyChecks(spec.body.typ),
		}
	}

	return plan, nil
}

// compileField resolves one declared field into a binder.
func (c *handlerCompiler) compileField(plan *inputPlan, spec *fieldSpec) error {
	name := spec.name
	if name == "" {
		if spec.source == sourceHeader {
			return fmt.Errorf("field %s has no header name; a header name cannot be derived "+
				"from a Go identifier, so write it out", spec.fieldName)
		}
		name = lowerCamel(spec.fieldName)
	}

	if spec.source == sourcePath && !c.wildcards[name] {
		return fmt.Errorf("field %s reads the path parameter %q, but %s has no {%s} in it",
			spec.fieldName, name, c.rc.route, name)
	}
	if spec.source == sourceForm {
		if err := c.claimForm(spec.fieldName); err != nil {
			return err
		}
	}
	if err := c.claim(spec.source, name, spec.fieldName); err != nil {
		return err
	}

	errField := spec.source.String() + "." + name

	if spec.file {
		plan.files = append(plan.files, fileBinder{
			index: spec.index, name: name, errField: errField, multiple: spec.fileMulti,
		})
		return nil
	}

	fieldType := fieldTypeAt(plan.typ, spec.index)
	decode, err := decoderFor(fieldType)
	if err != nil {
		return fmt.Errorf("field %s: %w", spec.fieldName, err)
	}

	binder := fieldBinder{
		index: spec.index, source: spec.source, name: name, errField: errField,
		decode: decode, csv: spec.csv, required: spec.required,
		transforms: spec.transforms, rules: spec.rules,
	}
	if err := resolveDefault(&binder, spec, fieldType, decode); err != nil {
		return err
	}

	plan.fields = append(plan.fields, binder)
	return nil
}

// fieldTypeAt walks an index path to the type it names.
func fieldTypeAt(structType reflect.Type, index []int) reflect.Type {
	t := structType
	for _, i := range index {
		t = t.Field(i).Type
	}
	return t
}

// resolveDefault settles a field's fallback, whichever way it was declared.
//
// A default written as text is parsed here rather than when a request arrives,
// so one that does not fit its field is a mistake in the source and not a
// surprise in production. A default given to a builder is already the field's
// type, because the compiler saw to it.
func resolveDefault(binder *fieldBinder, spec *fieldSpec, fieldType reflect.Type, decode decodeFunc) error {
	switch {
	case spec.hasValue:
		binder.fallback = &spec.defaultValue
	case spec.hasText:
		value := reflect.New(fieldType).Elem()
		if failure := decode(value, []string{spec.defaultText}); failure != nil {
			return fmt.Errorf("field %s has an invalid default %q: it %s",
				spec.fieldName, spec.defaultText, failure)
		}
		binder.fallback = &value
	}
	return nil
}

// compileBodyChecks resolves the rules a body type is judged by.
//
// A body is a document, not a flat list of parameters, so this descends: a
// struct field whose own type has rules is checked through, and so is every
// entry of a list of them. The names build up as they go — billing.zipCode,
// items[0].sku — which is how a client is told which part of what it sent is
// the problem.
func compileBodyChecks(t reflect.Type) []bodyCheck {
	return bodyChecksFor(t, map[reflect.Type]bool{})
}

// bodyChecksFor collects one type's checks, refusing to follow a type back
// into itself: a document that can contain itself is fine, and walking it
// forever is not.
func bodyChecksFor(t reflect.Type, visiting map[reflect.Type]bool) []bodyCheck {
	if t.Kind() != reflect.Struct || visiting[t] {
		return nil
	}
	visiting[t] = true
	defer delete(visiting, t)

	var checks []bodyCheck
	declared := lookupBody(t)
	if declared != nil {
		for _, field := range declared.fields {
			checks = append(checks, bodyCheck{
				index: field.index, name: field.name, required: field.required,
				transforms: field.transforms, rules: field.rules,
			})
		}
	}
	checks = append(checks, nestedChecks(t, visiting)...)

	// The whole-document checks come last, so that a rule about two fields
	// is asked only once each field has been judged on its own.
	if declared != nil && len(declared.whole) > 0 {
		checks = append(checks, bodyCheck{whole: declared.whole})
	}
	return checks
}

// nestedChecks finds the fields that are themselves worth descending into.
func nestedChecks(t reflect.Type, visiting map[reflect.Type]bool) []bodyCheck {
	var checks []bodyCheck
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}

		name, serialized := jsonName(t, []int{i})
		if !serialized {
			continue
		}
		// encoding/json promotes an embedded struct's fields to the top
		// level unless it was renamed, so its children are named as though
		// they were written here.
		if _, renamed := field.Tag.Lookup("json"); field.Anonymous && !renamed {
			name = ""
		}

		inner, list := field.Type, false
		if inner.Kind() == reflect.Pointer {
			inner = inner.Elem()
		}
		if inner.Kind() == reflect.Slice {
			inner, list = inner.Elem(), true
			if inner.Kind() == reflect.Pointer {
				inner = inner.Elem()
			}
		}

		found := bodyChecksFor(inner, visiting)
		if len(found) == 0 {
			continue
		}
		if list {
			checks = append(checks, bodyCheck{index: []int{i}, name: name, element: found})
		} else {
			checks = append(checks, bodyCheck{index: []int{i}, name: name, nested: found})
		}
	}
	return checks
}

// sprintf and plural build rule predicates. They are here rather than inline
// so that every rule of a kind is worded identically.
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// plural writes "1 character" and "2 characters" from one format.
func plural(format string, n int) string {
	text := fmt.Sprintf(format, n)
	if n == 1 {
		return text
	}
	return text + "s"
}

// joinInts lists numbers for a message.
func joinInts(values []int) string {
	parts := make([]string, len(values))
	for i, value := range values {
		parts[i] = strconv.Itoa(value)
	}
	return strings.Join(parts, ", ")
}
