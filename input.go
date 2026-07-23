package tork

import (
	"fmt"
	"mime/multipart"
	"reflect"
	"strings"
)

// source is where a field's value comes from. It is also how a field error
// names its location, which is why the string form is part of the wire
// contract rather than a debugging convenience.
type source uint8

const (
	sourcePath source = iota
	sourceQuery
	sourceHeader
	sourceCookie
	sourceForm
)

// sourceTags maps each source to the struct tag that selects it. The order is
// the order they are looked for, and the order they are listed in when a field
// carries none of them.
var sourceTags = []struct {
	tag    string
	source source
}{
	{"path", sourcePath},
	{"query", sourceQuery},
	{"header", sourceHeader},
	{"cookie", sourceCookie},
	{"form", sourceForm},
}

// bodyTag is the sixth tag, kept apart from the five above because a body is
// not a parameter: it has no name, there is at most one, and it consumes the
// request rather than reading a value out of it.
const bodyTag = "body"

func (s source) String() string {
	return sourceTags[s].tag
}

var (
	requestType   = reflect.TypeFor[*Request]()
	jsonBodyType  = reflect.TypeFor[JSONBody]()
	fileType      = reflect.TypeFor[*multipart.FileHeader]()
	fileSliceType = reflect.TypeFor[[]*multipart.FileHeader]()
)

// inputPlan is one input struct compiled: what to read, from where, and how to
// turn it into the field's type. Building one is the only time this package
// looks at a declaration.
type inputPlan struct {
	typ    reflect.Type
	fields []fieldBinder
	files  []fileBinder
	body   *bodyBinder
}

// fieldBinder is one parameter, resolved down to indices and closures.
type fieldBinder struct {
	index  []int
	source source
	name   string

	// errField is what a FieldError calls this field, worked out once
	// because it is a string concatenation that would otherwise happen on
	// every rejected request.
	errField string

	decode     decodeFunc
	fallback   *reflect.Value
	csv        bool
	required   bool
	transforms []transform
	rules      []rule
}

// fileBinder is an uploaded file, which is not a decodeFunc because it never
// passes through a string.
type fileBinder struct {
	index    []int
	name     string
	errField string
	multiple bool
}

// bodyBinder is the request body. Index is where in the input struct it lands,
// and is empty when the parameter is itself the body.
type bodyBinder struct {
	index  []int
	typ    reflect.Type
	checks []bodyCheck
}

// bodyCheck is one checked field of a body, after decoding.
//
// A check is either a leaf — rules against a value — or a way through to more
// of them: nested for a struct, element for every entry of a list of structs.
type bodyCheck struct {
	index      []int
	name       string
	required   bool
	transforms []transform
	rules      []rule

	nested  []bodyCheck
	element []bodyCheck

	// whole is a check against the document this one belongs to rather than
	// against any one field of it.
	whole []wholeCheck
}

// routeCompiler is the state one route's compilation shares across its
// dependencies and its handler: the graph to resolve services from, the
// request-scoped values produced so far and the slot each lives in, and the
// one body a request has. A dependency and the handler are separate units with
// their own parameters, but they draw from and add to this together.
type routeCompiler struct {
	route  *Route
	inj    *injector
	scoped map[reflect.Type]int // request-scoped type → its slot on the exchange
	slots  int                  // number of request-scoped slots assigned so far

	// bodyFrom and formFrom name whatever already consumed the request body,
	// across every unit of the route, because the body is read once however
	// the dependencies and the handler divide up what reads it.
	bodyFrom string
	formFrom string
}

func newRouteCompiler(route *Route, inj *injector) *routeCompiler {
	return &routeCompiler{
		route:  route,
		inj:    inj,
		scoped: map[reflect.Type]int{},
	}
}

// handlerCompiler classifies the parameters of one unit — a dependency or the
// handler — carrying what is private to that unit: the wildcards its path
// offers, and the names it has already claimed, since one function cannot read
// the same parameter into two places. Everything shared across the route
// reaches it through rc.
type handlerCompiler struct {
	rc        *routeCompiler
	wildcards map[string]bool
	claimed   map[string]string
}

func newHandlerCompiler(rc *routeCompiler) *handlerCompiler {
	return &handlerCompiler{
		rc:        rc,
		wildcards: wildcardsOf(rc.route.Path),
		claimed:   map[string]string{},
	}
}

// wildcardsOf collects the names a route pattern binds, so a path field can be
// checked against the route it will actually serve.
func wildcardsOf(path string) map[string]bool {
	names := map[string]bool{}
	for _, segment := range strings.Split(path, "/") {
		if !strings.HasPrefix(segment, "{") || !strings.HasSuffix(segment, "}") {
			continue
		}
		// ServeMux spells a multi-segment wildcard {name...}; the name is
		// the same either way.
		names[strings.TrimSuffix(segment[1:len(segment)-1], "...")] = true
	}
	return names
}

// specFor finds how a struct was declared, and returns nil when it was not
// declared as an input at all.
//
// A registered spec wins, because writing one is a deliberate act; tags are
// only read for a type nobody built a spec for. A type declared both ways has
// two answers and is refused rather than being given the arbitrary one.
func specFor(t reflect.Type) (*inputSpec, error) {
	if t.Kind() != reflect.Struct {
		return nil, nil
	}

	registered, found := lookupInput(t)
	marked, tagged := scanStruct(t)

	switch {
	case found && tagged:
		return nil, fmt.Errorf("%s is declared by tork.DefineInput and also carries binding tags; "+
			"a struct is declared one way or the other", t)
	case found:
		return registered, nil
	case marked && tagged:
		return nil, fmt.Errorf("%s embeds tork.JSONBody and also carries parameter tags; "+
			"a body struct holds only the body, so put it in a field tagged `body:\"json\"` of a wrapper struct instead", t)
	case marked:
		return &inputSpec{typ: t, body: &bodySpec{typ: t}}, nil
	case tagged:
		return specFromTags(t)
	default:
		return nil, nil
	}
}

// scanStruct reports whether a struct says it is a body, and whether any field
// says where it comes from.
func scanStruct(t reflect.Type) (marked, tagged bool) {
	for i := range t.NumField() {
		field := t.Field(i)
		if field.Anonymous && field.Type == jsonBodyType {
			marked = true
			continue
		}
		if !field.IsExported() {
			continue
		}
		if _, ok := field.Tag.Lookup(bodyTag); ok {
			tagged = true
			continue
		}
		for _, candidate := range sourceTags {
			if _, ok := field.Tag.Lookup(candidate.tag); ok {
				tagged = true
			}
		}
	}
	return marked, tagged
}

// specFromTags reads a struct's tags into the same field model the builders
// produce.
func specFromTags(t reflect.Type) (*inputSpec, error) {
	spec := &inputSpec{typ: t}
	if err := walkTags(spec, t, nil); err != nil {
		return nil, err
	}
	return spec, nil
}

// walkTags adds every field of t to the spec, descending into embedded structs
// so that a shared set of parameters — a Pagination struct, say — can be
// embedded rather than repeated.
func walkTags(spec *inputSpec, t reflect.Type, prefix []int) error {
	for i := range t.NumField() {
		field := t.Field(i)
		index := append(append([]int{}, prefix...), i)

		if !field.IsExported() {
			// An unexported field cannot be written to, and no tag on one
			// could be honoured, so it is left alone rather than refused.
			continue
		}

		if raw, ok := field.Tag.Lookup(bodyTag); ok {
			if err := addTaggedBody(spec, field, index, raw); err != nil {
				return err
			}
			continue
		}

		tagged, err := addTaggedField(spec, field, index)
		if err != nil {
			return err
		}
		if tagged {
			continue
		}

		if field.Anonymous && field.Type.Kind() == reflect.Struct {
			if err := walkTags(spec, field.Type, index); err != nil {
				return err
			}
			continue
		}

		return fmt.Errorf("field %s of %s has no path, query, header, cookie, form, or body tag; "+
			"every field of an input struct has to say where its value comes from", field.Name, t)
	}
	return nil
}

// addTaggedField adds one tagged parameter, reporting whether the field
// carried a tag at all.
func addTaggedField(spec *inputSpec, field reflect.StructField, index []int) (bool, error) {
	src, raw, found := lookupSourceTag(field)
	if !found {
		return false, nil
	}
	if err := rejectSecondTag(field, src); err != nil {
		return true, err
	}

	name, csv, err := parseParamTag(field, src, raw)
	if err != nil {
		return true, err
	}

	declared := fieldSpec{
		index: index, fieldName: field.Name, source: src, name: name, csv: csv,
	}
	if fallback, ok := field.Tag.Lookup("default"); ok {
		declared.defaultText, declared.hasText = fallback, true
	}

	if field.Type == fileType || field.Type == fileSliceType {
		if src != sourceForm {
			return true, fmt.Errorf("field %s is an uploaded file, which only a form can carry; tag it `form:%q`",
				field.Name, name)
		}
		declared.file = true
		declared.fileMulti = field.Type == fileSliceType
	}

	spec.fields = append(spec.fields, declared)
	return true, nil
}

// lookupSourceTag finds which of the five parameter tags a field carries.
func lookupSourceTag(field reflect.StructField) (source, string, bool) {
	for _, candidate := range sourceTags {
		if raw, ok := field.Tag.Lookup(candidate.tag); ok {
			return candidate.source, raw, true
		}
	}
	return 0, "", false
}

// rejectSecondTag refuses a field that names two places to read from, which
// has no sensible reading and is nearly always a half-finished edit.
func rejectSecondTag(field reflect.StructField, chosen source) error {
	for _, candidate := range sourceTags {
		if candidate.source == chosen {
			continue
		}
		if _, ok := field.Tag.Lookup(candidate.tag); ok {
			return fmt.Errorf("field %s carries both %s and %s tags; a field reads from one place",
				field.Name, chosen, candidate.tag)
		}
	}
	return nil
}

// parseParamTag reads a tag into a wire name and its modifiers. An empty name
// is left empty here and derived later, so that both front ends derive it the
// same way.
func parseParamTag(field reflect.StructField, src source, raw string) (name string, csv bool, err error) {
	name, modifiers, _ := strings.Cut(raw, ",")

	for _, modifier := range strings.Split(modifiers, ",") {
		switch modifier {
		case "":
		case "csv":
			csv = true
		default:
			return "", false, fmt.Errorf("field %s has an unknown %s modifier %q; the only one is csv",
				field.Name, src, modifier)
		}
	}

	if csv && field.Type.Kind() != reflect.Slice {
		return "", false, fmt.Errorf("field %s is marked csv but is not a slice; csv splits one value into many",
			field.Name)
	}

	return name, csv, nil
}

// addTaggedBody records the field a body decodes into.
func addTaggedBody(spec *inputSpec, field reflect.StructField, index []int, raw string) error {
	switch raw {
	case "", "json":
	default:
		return fmt.Errorf("field %s has body tag %q; the only body format is json", field.Name, raw)
	}
	if spec.body != nil {
		return fmt.Errorf("field %s is a second request body; a request has one body", field.Name)
	}
	spec.body = &bodySpec{index: index, typ: field.Type}
	return nil
}

// claimBody records that this route has its one body. The claim is on the
// route rather than the unit, so a dependency and the handler cannot each read
// the body: the request drains once.
func (c *handlerCompiler) claimBody(t reflect.Type) error {
	if c.rc.bodyFrom != "" {
		return fmt.Errorf("%s is a second request body; %s already is one, and a request has one body", t, c.rc.bodyFrom)
	}
	if c.rc.formFrom != "" {
		return fmt.Errorf("%s is a request body, but field %s already reads the body as a form; "+
			"a request body is read once", t, c.rc.formFrom)
	}
	c.rc.bodyFrom = t.String()
	return nil
}

// claimForm records that this route reads the body as a form.
func (c *handlerCompiler) claimForm(field string) error {
	if c.rc.bodyFrom != "" {
		return fmt.Errorf("field %s cannot read a form when %s is already the request body; "+
			"a request body is read once", field, c.rc.bodyFrom)
	}
	c.rc.formFrom = field
	return nil
}

// claim records a wire name so that two fields cannot quietly read the same
// parameter into different places.
func (c *handlerCompiler) claim(src source, name, field string) error {
	key := src.String() + "." + name
	if first, taken := c.claimed[key]; taken {
		return fmt.Errorf("field %s reads %s, which field %s already reads", field, key, first)
	}
	c.claimed[key] = field
	return nil
}
