package tork

import (
	"reflect"
	"strings"

	"github.com/tork-go/web/openapi"
)

// SchemaOf describes a Go type as a JSON Schema, together with the named
// schemas its references point at.
//
// It is the same machinery the OpenAPI document is built with, exported
// because describing a type is useful on its own: a tool that generates
// clients, or the generator in a later milestone, should not have to
// reimplement what a Go type means on the wire.
func SchemaOf(t reflect.Type) (*openapi.Schema, []openapi.NamedSchema) {
	b := newSchemaBuilder()
	schema := b.schemaFor(t)
	return schema, b.components()
}

// schemaBuilder turns Go types into JSON Schemas, collecting the named struct
// types it meets so that a type used by three operations is described once and
// referred to three times.
//
// It exists in this package rather than in openapi because deciding what a
// type means is framework knowledge: an Optional is nullable, an uploaded file
// is a binary string, and a body struct's marker embed is not a property.
// The openapi package stays a model and two emitters, knowing none of that.
type schemaBuilder struct {
	names   map[reflect.Type]string // the component each named type was filed under
	byName  map[string]reflect.Type // what has claimed each name, to keep them unique
	schemas map[reflect.Type]*openapi.Schema
	order   []reflect.Type // registration order, so components come out the same way twice
}

func newSchemaBuilder() *schemaBuilder {
	return &schemaBuilder{
		names:   map[reflect.Type]string{},
		byName:  map[string]reflect.Type{},
		schemas: map[reflect.Type]*openapi.Schema{},
	}
}

// components is every named schema the builder collected, in the order it
// first met them.
func (b *schemaBuilder) components() []openapi.NamedSchema {
	named := make([]openapi.NamedSchema, 0, len(b.order))
	for _, t := range b.order {
		named = append(named, openapi.NamedSchema{Name: b.names[t], Schema: b.schemas[t]})
	}
	return named
}

// schemaFor describes t.
//
// The order of the cases is the order decoderFor unwraps a value in, and for
// the same reason: a type is whatever its outermost wrapper says it is, and
// only what is left after the wrappers is looked at as a kind. A named struct
// becomes a component and is referred to, which is what keeps a document that
// mentions one type twenty times readable, and what makes a recursive type
// describable at all.
func (b *schemaBuilder) schemaFor(t reflect.Type) *openapi.Schema {
	if inner, ok := optionalElem(t); ok {
		return b.schemaFor(inner).Nullable()
	}

	switch {
	case t == timeType:
		return &openapi.Schema{Type: []string{openapi.TypeString}, Format: "date-time"}
	case t == durationType:
		// A duration crosses the wire as the string Go writes it, "1h30m", not
		// as a number of anything.
		return &openapi.Schema{Type: []string{openapi.TypeString}, Format: "duration"}
	case t == fileType:
		return &openapi.Schema{Type: []string{openapi.TypeString}, Format: "binary"}
	case t == fileSliceType:
		return &openapi.Schema{
			Type:  []string{openapi.TypeArray},
			Items: &openapi.Schema{Type: []string{openapi.TypeString}, Format: "binary"},
		}
	}

	switch t.Kind() {
	case reflect.Pointer:
		// A pointer is how a field says absent is a state it cares about.
		return b.schemaFor(t.Elem()).Nullable()
	case reflect.Slice, reflect.Array:
		// A []byte is a string that happens to be spelled as a slice, the same
		// carve-out the decoder makes.
		if t.Elem().Kind() == reflect.Uint8 {
			return &openapi.Schema{Type: []string{openapi.TypeString}, ContentEncoding: "base64"}
		}
		return &openapi.Schema{Type: []string{openapi.TypeArray}, Items: b.schemaFor(t.Elem())}
	case reflect.Map:
		return &openapi.Schema{
			Type:                 []string{openapi.TypeObject},
			AdditionalProperties: b.schemaFor(t.Elem()),
		}
	case reflect.Struct:
		return b.structSchema(t)
	case reflect.Bool:
		return &openapi.Schema{Type: []string{openapi.TypeBoolean}}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return &openapi.Schema{Type: []string{openapi.TypeInteger}}
	case reflect.Float32, reflect.Float64:
		return &openapi.Schema{Type: []string{openapi.TypeNumber}}
	case reflect.String:
		return &openapi.Schema{Type: []string{openapi.TypeString}}
	default:
		// An interface, or anything else with no shape of its own, accepts
		// whatever it is given; an empty schema is how JSON Schema says that.
		return &openapi.Schema{}
	}
}

// structSchema describes a struct, as a reference when the type has a name and
// inline when it does not.
//
// The name is registered before the fields are walked, so a type that contains
// itself finds the reference already there instead of descending forever.
func (b *schemaBuilder) structSchema(t reflect.Type) *openapi.Schema {
	if t.Name() == "" {
		return b.objectSchema(t)
	}
	if name, built := b.names[t]; built {
		return &openapi.Schema{Ref: "#/components/schemas/" + name}
	}

	name := b.claim(t)
	b.names[t] = name
	b.byName[name] = t
	b.order = append(b.order, t)
	b.schemas[t] = &openapi.Schema{} // stand in until the fields are walked
	b.schemas[t] = b.objectSchema(t)

	return &openapi.Schema{Ref: "#/components/schemas/" + name}
}

// claim picks the component name for a type: its own, or its import path and
// its own when something else already answers to that.
//
// The fallback cannot itself collide, because a package path and a type name
// identify a type uniquely — which is why there is no third attempt and no
// counter to make one unique after the fact.
func (b *schemaBuilder) claim(t reflect.Type) string {
	name := componentName(t.Name())
	if taken, clash := b.byName[name]; !clash || taken == t {
		return name
	}
	return componentName(t.PkgPath()) + name
}

// componentName keeps only what a component name may contain. A generic type's
// name carries its arguments in brackets, and a package path in them, neither
// of which belongs in a key.
func componentName(name string) string {
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// objectSchema walks a struct's fields into properties.
//
// It reads the json tags rather than the binding tags, because this describes
// a value as it is serialized — a response body, or the shape a request body
// decodes into. What a field is called on the wire, whether it appears at all,
// and whether it may be left out are all the encoding/json rules, so following
// them is the only way the document can match what the server actually writes.
func (b *schemaBuilder) objectSchema(t reflect.Type) *openapi.Schema {
	schema := &openapi.Schema{Type: []string{openapi.TypeObject}}
	b.addFields(schema, t)
	return schema
}

func (b *schemaBuilder) addFields(schema *openapi.Schema, t reflect.Type) {
	for i := range t.NumField() {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		// The marker a body struct embeds says what the struct is, not what it
		// carries, so it is not a property of anything.
		if field.Anonymous && field.Type == jsonBodyType {
			continue
		}

		name, omitted, skip := jsonFieldName(field)
		if skip {
			continue
		}
		// An embedded struct with no name of its own contributes its fields to
		// the struct that embeds it, the way encoding/json promotes them.
		if field.Anonymous && name == "" {
			embedded := field.Type
			if embedded.Kind() == reflect.Pointer {
				embedded = embedded.Elem()
			}
			if embedded.Kind() == reflect.Struct {
				b.addFields(schema, embedded)
				continue
			}
		}
		if name == "" {
			name = field.Name
		}

		property := b.schemaFor(field.Type)
		schema.Properties = append(schema.Properties, openapi.NamedSchema{Name: name, Schema: property})
		// A field that can be left out is not required; everything else is
		// always written, so a reader can count on it being there.
		if !omitted && !isOptional(field.Type) {
			schema.Required = append(schema.Required, name)
		}
	}
}

// jsonFieldName reads a json tag into the name a field is written under,
// whether it may be omitted, and whether it is written at all.
func jsonFieldName(field reflect.StructField) (name string, omitted, skip bool) {
	tag, tagged := field.Tag.Lookup("json")
	if !tagged {
		return "", false, false
	}
	name, opts, _ := strings.Cut(tag, ",")
	if name == "-" && opts == "" {
		return "", false, true
	}
	for opt := range strings.SplitSeq(opts, ",") {
		if opt == "omitempty" || opt == "omitzero" {
			omitted = true
		}
	}
	return name, omitted, false
}

// optionalElem reports the type inside an Optional, and whether t was one.
//
// The test is against the pointer type because target has a pointer receiver,
// which is the same way the decoder recognises one.
func optionalElem(t reflect.Type) (reflect.Type, bool) {
	if !reflect.PointerTo(t).Implements(reflect.TypeFor[optionalTarget]()) {
		return nil, false
	}
	value, _, _ := reflect.New(t).Interface().(optionalTarget).target()
	return reflect.TypeOf(value).Elem(), true
}

// isOptional reports whether a field may be left out of a document because its
// type says absence is a state it understands.
func isOptional(t reflect.Type) bool {
	_, ok := optionalElem(t)
	return ok || t.Kind() == reflect.Pointer
}
