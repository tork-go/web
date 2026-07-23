package tork_test

import (
	"encoding/json"
	"mime/multipart"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/tork-go/web"
	"github.com/tork-go/web/openapi"
	"github.com/tork-go/web/tests/fixtures"
)

// schemaJSON describes T and returns the schema as a decoded map, which is
// what these tests assert against — the emitter has its own tests, so this one
// is about what was described rather than how it was written out.
func schemaJSON[T any](t *testing.T) map[string]any {
	t.Helper()
	schema, _ := tork.SchemaOf(reflect.TypeFor[T]())
	var decoded map[string]any
	if err := json.Unmarshal(schema.JSON(), &decoded); err != nil {
		t.Fatalf("schema does not parse: %v", err)
	}
	return decoded
}

func TestSchemaOfScalars(t *testing.T) {
	tests := []struct {
		name   string
		schema map[string]any
		typ    string
		format string
	}{
		{"bool", schemaJSON[bool](t), "boolean", ""},
		{"int", schemaJSON[int](t), "integer", ""},
		{"int64", schemaJSON[int64](t), "integer", ""},
		{"uint8", schemaJSON[uint8](t), "integer", ""},
		{"float64", schemaJSON[float64](t), "number", ""},
		{"string", schemaJSON[string](t), "string", ""},
		{"time", schemaJSON[time.Time](t), "string", "date-time"},
		{"duration", schemaJSON[time.Duration](t), "string", "duration"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.schema["type"] != tt.typ {
				t.Errorf("type = %v, want %s", tt.schema["type"], tt.typ)
			}
			if tt.format != "" && tt.schema["format"] != tt.format {
				t.Errorf("format = %v, want %s", tt.schema["format"], tt.format)
			}
		})
	}
}

// An interface has no shape of its own, and an empty schema is how JSON Schema
// says "anything".
func TestSchemaOfAnyIsEmpty(t *testing.T) {
	if got := schemaJSON[any](t); len(got) != 0 {
		t.Errorf("schema = %v, want an empty schema", got)
	}
}

func TestSchemaOfCollections(t *testing.T) {
	list := schemaJSON[[]string](t)
	if list["type"] != "array" {
		t.Fatalf("type = %v", list["type"])
	}
	if items, _ := list["items"].(map[string]any); items["type"] != "string" {
		t.Errorf("items = %v", list["items"])
	}

	fixed := schemaJSON[[3]int](t)
	if fixed["type"] != "array" {
		t.Errorf("a fixed-size array is still an array: %v", fixed)
	}

	// A []byte is a string that happens to be spelled as a slice.
	raw := schemaJSON[[]byte](t)
	if raw["type"] != "string" || raw["contentEncoding"] != "base64" {
		t.Errorf("[]byte = %v, want a base64 string", raw)
	}

	dict := schemaJSON[map[string]int](t)
	if dict["type"] != "object" {
		t.Fatalf("type = %v", dict["type"])
	}
	if extra, _ := dict["additionalProperties"].(map[string]any); extra["type"] != "integer" {
		t.Errorf("additionalProperties = %v", dict["additionalProperties"])
	}
}

// A pointer and an Optional both say absence is a state the field
// understands, which in 3.1 is a type array carrying "null".
func TestSchemaOfNullables(t *testing.T) {
	for name, got := range map[string]map[string]any{
		"pointer":  schemaJSON[*string](t),
		"optional": schemaJSON[tork.Optional[string]](t),
	} {
		types, ok := got["type"].([]any)
		if !ok || len(types) != 2 || types[0] != "string" || types[1] != "null" {
			t.Errorf("%s type = %v, want [string null]", name, got["type"])
		}
	}
}

// An uploaded file is a binary string, and several of them an array of one.
func TestSchemaOfUploads(t *testing.T) {
	one := schemaJSON[*multipart.FileHeader](t)
	if one["type"] != "string" || one["format"] != "binary" {
		t.Errorf("file = %v", one)
	}

	many := schemaJSON[[]*multipart.FileHeader](t)
	if many["type"] != "array" {
		t.Fatalf("files = %v", many)
	}
	if items, _ := many["items"].(map[string]any); items["format"] != "binary" {
		t.Errorf("items = %v", many["items"])
	}
}

// SchemaItem is a named struct, so it becomes a component and is referred to
// rather than repeated.
type SchemaItem struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Notes string `json:"notes,omitempty"`

	hidden   string
	Skipped  string `json:"-"`
	Untagged string
}

func TestSchemaOfNamedStructIsAReference(t *testing.T) {
	schema, components := tork.SchemaOf(reflect.TypeFor[SchemaItem]())

	if schema.Ref != "#/components/schemas/SchemaItem" {
		t.Fatalf("ref = %q", schema.Ref)
	}
	if len(components) != 1 || components[0].Name != "SchemaItem" {
		t.Fatalf("components = %+v", components)
	}

	var decoded map[string]any
	if err := json.Unmarshal(components[0].Schema.JSON(), &decoded); err != nil {
		t.Fatalf("component does not parse: %v", err)
	}
	props, _ := decoded["properties"].(map[string]any)
	for _, want := range []string{"id", "name", "notes", "Untagged"} {
		if _, ok := props[want]; !ok {
			t.Errorf("properties is missing %q: %v", want, props)
		}
	}
	if _, ok := props["hidden"]; ok {
		t.Error("an unexported field is not a property")
	}
	if _, ok := props["Skipped"]; ok {
		t.Error(`a field tagged json:"-" is not a property`)
	}

	// Everything the server always writes is required; what may be omitted is
	// not.
	required := map[string]bool{}
	for _, r := range decoded["required"].([]any) {
		required[r.(string)] = true
	}
	if !required["id"] || !required["name"] || !required["Untagged"] {
		t.Errorf("required = %v", decoded["required"])
	}
	if required["notes"] {
		t.Error("a field tagged omitempty must not be required")
	}
}

// An Optional field is nullable and never required, which is the whole reason
// a PATCH body uses one.
type SchemaPatch struct {
	Name tork.Optional[string] `json:"name,omitzero"`
	Kind string                `json:"kind"`
}

func TestOptionalFieldIsNotRequired(t *testing.T) {
	_, components := tork.SchemaOf(reflect.TypeFor[SchemaPatch]())

	var decoded map[string]any
	if err := json.Unmarshal(components[0].Schema.JSON(), &decoded); err != nil {
		t.Fatalf("component does not parse: %v", err)
	}
	required, _ := decoded["required"].([]any)
	if len(required) != 1 || required[0] != "kind" {
		t.Errorf("required = %v, want only kind", required)
	}
}

// An anonymous struct has no name to file it under, so it is described where
// it is used.
func TestAnonymousStructIsInline(t *testing.T) {
	schema, components := tork.SchemaOf(reflect.TypeFor[struct {
		Count int `json:"count"`
	}]())

	if schema.Ref != "" {
		t.Errorf("ref = %q, want an inline schema", schema.Ref)
	}
	if len(components) != 0 {
		t.Errorf("components = %+v, want none", components)
	}
	if len(schema.Properties) != 1 || schema.Properties[0].Name != "count" {
		t.Errorf("properties = %+v", schema.Properties)
	}
}

// A type that contains itself has to be describable, which is what the
// reference is for: the name is registered before the fields are walked.
type SchemaNode struct {
	Name     string        `json:"name"`
	Parent   *SchemaNode   `json:"parent,omitempty"`
	Children []*SchemaNode `json:"children,omitempty"`
}

func TestRecursiveTypeTerminates(t *testing.T) {
	schema, components := tork.SchemaOf(reflect.TypeFor[SchemaNode]())

	if schema.Ref != "#/components/schemas/SchemaNode" {
		t.Fatalf("ref = %q", schema.Ref)
	}
	if len(components) != 1 {
		t.Fatalf("components = %d, want 1", len(components))
	}
	body := string(components[0].Schema.JSON())
	if strings.Count(body, "#/components/schemas/SchemaNode") != 2 {
		t.Errorf("both self references should be refs:\n%s", body)
	}
}

// An embedded struct with no name of its own contributes its fields to the
// struct embedding it, the way encoding/json promotes them.
type SchemaPage struct {
	Limit int `json:"limit"`
}

type SchemaListing struct {
	SchemaPage
	Query string `json:"query"`
}

func TestEmbeddedFieldsArePromoted(t *testing.T) {
	_, components := tork.SchemaOf(reflect.TypeFor[SchemaListing]())

	var decoded map[string]any
	if err := json.Unmarshal(components[0].Schema.JSON(), &decoded); err != nil {
		t.Fatalf("component does not parse: %v", err)
	}
	props, _ := decoded["properties"].(map[string]any)
	if _, ok := props["limit"]; !ok {
		t.Errorf("the embedded struct's field was not promoted: %v", props)
	}
	if _, ok := props["query"]; !ok {
		t.Errorf("properties = %v", props)
	}
	if len(components) != 1 {
		t.Errorf("an embedded struct should not become a component of its own: %+v", components)
	}
}

// An embedded pointer promotes its fields too, and an embedded type that is
// not a struct has nothing to promote, so it is a property under its own name.
type SchemaMeta struct {
	Trace string `json:"trace"`
}

type SchemaLabel string

type SchemaMixed struct {
	*SchemaMeta
	SchemaLabel
	Name string `json:"name"`
}

func TestEmbeddedPointerAndNonStruct(t *testing.T) {
	_, components := tork.SchemaOf(reflect.TypeFor[SchemaMixed]())

	var decoded map[string]any
	for _, c := range components {
		if c.Name != "SchemaMixed" {
			continue
		}
		if err := json.Unmarshal(c.Schema.JSON(), &decoded); err != nil {
			t.Fatalf("component does not parse: %v", err)
		}
	}
	props, _ := decoded["properties"].(map[string]any)
	if _, ok := props["trace"]; !ok {
		t.Errorf("an embedded pointer's field was not promoted: %v", props)
	}
	if _, ok := props["SchemaLabel"]; !ok {
		t.Errorf("an embedded non-struct should be a property of its own: %v", props)
	}
	if _, ok := props["name"]; !ok {
		t.Errorf("properties = %v", props)
	}
}

// A body's marker embed says what the struct is, not what it carries.
type SchemaBody struct {
	tork.JSONBody
	Name string `json:"name"`
}

func TestJSONBodyMarkerIsNotAProperty(t *testing.T) {
	_, components := tork.SchemaOf(reflect.TypeFor[SchemaBody]())

	var decoded map[string]any
	if err := json.Unmarshal(components[0].Schema.JSON(), &decoded); err != nil {
		t.Fatalf("component does not parse: %v", err)
	}
	props, _ := decoded["properties"].(map[string]any)
	if len(props) != 1 {
		t.Errorf("properties = %v, want only name", props)
	}
}

// Item here shares its name with fixtures.Item, so the second one to be
// described has to be filed under a name of its own.
type Item struct {
	ID string `json:"id"`
}

type SchemaBoth struct {
	Mine  Item          `json:"mine"`
	Other fixtures.Item `json:"other"`
}

func TestTypesSharingANameGetOneComponentEach(t *testing.T) {
	_, components := tork.SchemaOf(reflect.TypeFor[SchemaBoth]())

	if len(components) != 3 {
		t.Fatalf("components = %d, want SchemaBoth and both Items", len(components))
	}
	names := map[string]bool{}
	for _, c := range components {
		if names[c.Name] {
			t.Errorf("two components share the name %q", c.Name)
		}
		names[c.Name] = true
	}
	if !names["Item"] {
		t.Errorf("the first Item should keep the plain name: %v", names)
	}
}

// A generic type's name carries its argument, and a package path inside it,
// none of which may reach a component key.
type SchemaBox[T any] struct {
	Value T `json:"value"`
}

func TestGenericTypeNameIsUsableAsAComponentKey(t *testing.T) {
	_, components := tork.SchemaOf(reflect.TypeFor[SchemaBox[string]]())

	if len(components) != 1 {
		t.Fatalf("components = %+v", components)
	}
	for _, r := range components[0].Name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '.', r == '-', r == '_':
		default:
			t.Fatalf("component name %q contains %q, which a key may not", components[0].Name, r)
		}
	}
}

// The same type described twice is one component, referred to twice.
func TestATypeUsedTwiceIsDescribedOnce(t *testing.T) {
	type pair struct {
		First  SchemaItem `json:"first"`
		Second SchemaItem `json:"second"`
	}

	schema, components := tork.SchemaOf(reflect.TypeFor[pair]())

	count := 0
	for _, c := range components {
		if c.Name == "SchemaItem" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("SchemaItem appears %d times in components", count)
	}
	for _, p := range schema.Properties {
		if p.Schema.Ref != "#/components/schemas/SchemaItem" {
			t.Errorf("%s = %+v, want a reference", p.Name, p.Schema)
		}
	}
}

// A schema is built the same way twice, so a document containing one can be
// compared as bytes.
func TestSchemaOfIsDeterministic(t *testing.T) {
	first, _ := tork.SchemaOf(reflect.TypeFor[SchemaBoth]())
	second, _ := tork.SchemaOf(reflect.TypeFor[SchemaBoth]())
	if string(first.JSON()) != string(second.JSON()) {
		t.Error("two descriptions of one type differ")
	}
}

// The builder is what the document uses, so a schema it produces has to be
// something the emitter can write.
func TestSchemaOfProducesAnEmittableSchema(t *testing.T) {
	schema, components := tork.SchemaOf(reflect.TypeFor[SchemaNode]())
	doc := &openapi.Document{
		Info:       openapi.Info{Title: "T", Version: "1"},
		Components: openapi.Components{Schemas: components},
		Paths: []openapi.PathItem{{
			Path: "/nodes",
			Operations: []openapi.Operation{{
				Method: "GET",
				Responses: []openapi.Response{{
					Status: 200, Description: "A node.",
					Content: []openapi.MediaType{{Type: "application/json", Schema: schema}},
				}},
			}},
		}},
	}

	var decoded map[string]any
	if err := json.Unmarshal(doc.JSON(), &decoded); err != nil {
		t.Fatalf("document does not parse: %v", err)
	}
}
