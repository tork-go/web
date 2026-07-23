package tork_test

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/tork-go/web/openapi"
)

// ptr is the shorthand these tests need constantly: the schema model uses
// pointers wherever zero is a meaningful value.
func ptr[T any](v T) *T { return &v }

// kitchenSink is a document that sets every field the model has, so one test
// reaches every branch of both emitters. Anything added to the model and not
// added here shows up immediately as an uncovered line.
func kitchenSink() *openapi.Document {
	return &openapi.Document{
		Info: openapi.Info{
			Title:          "Example API",
			Summary:        "A short one",
			Description:    "Prose about the API.",
			TermsOfService: "https://example.com/terms",
			Contact:        &openapi.Contact{Name: "Support", URL: "https://example.com", Email: "help@example.com"},
			License:        &openapi.License{Name: "Apache-2.0", Identifier: "Apache-2.0", URL: "https://example.com/licence"},
			Version:        "2.1.0",
		},
		Servers: []openapi.Server{{
			URL:         "https://{region}.example.com",
			Description: "Production",
			Variables: []openapi.ServerVariable{{
				Name: "region", Default: "eu", Description: "Which region", Enum: []string{"eu", "us"},
			}},
		}},
		Paths: []openapi.PathItem{{
			Path:        "/items/{item_id}",
			Summary:     "One item",
			Description: "Everything about a single item.",
			Parameters: []openapi.Parameter{{
				Name: "trace", In: openapi.InHeader, Schema: &openapi.Schema{Type: []string{openapi.TypeString}},
			}},
			Operations: []openapi.Operation{{
				Method:       "GET",
				Tags:         []string{"items"},
				Summary:      "Read an item",
				Description:  "Reads one item by its identifier.",
				ExternalDocs: &openapi.ExternalDocs{Description: "More", URL: "https://example.com/docs"},
				OperationID:  "items.read",
				Deprecated:   true,
				Parameters: []openapi.Parameter{{
					Name:            "item_id",
					In:              openapi.InPath,
					Description:     "The item's identifier.",
					Required:        true,
					Deprecated:      true,
					AllowEmptyValue: true,
					Style:           "simple",
					Explode:         ptr(false),
					Schema:          &openapi.Schema{Type: []string{openapi.TypeString}},
					Example:         "itm_1",
					HasExample:      true,
					Examples: []openapi.NamedExample{{
						Name: "first",
						Example: openapi.Example{
							Summary: "The first", Description: "An identifier", Value: "itm_1", HasValue: true,
							ExternalValue: "https://example.com/example.json",
						},
					}},
				}},
				RequestBody: &openapi.RequestBody{
					Description: "The item to store.",
					Required:    true,
					Content: []openapi.MediaType{{
						Type:       "application/json",
						Schema:     &openapi.Schema{Ref: "#/components/schemas/Item"},
						Example:    map[string]any{"name": "Boots"},
						HasExample: true,
						Examples:   []openapi.NamedExample{{Name: "boots", Example: openapi.Example{Summary: "Boots"}}},
						Encoding: []openapi.Encoding{{
							Property:    "avatar",
							ContentType: "image/png",
							Style:       "form",
							Explode:     ptr(true),
							Headers: []openapi.NamedHeader{{
								Name:   "X-Rate",
								Header: openapi.Header{Description: "How many left", Required: true, Deprecated: true, Schema: &openapi.Schema{Type: []string{openapi.TypeInteger}}},
							}},
						}},
					}},
				},
				Responses: []openapi.Response{
					{
						Status:      200,
						Description: "The item.",
						Content:     []openapi.MediaType{{Type: "application/json", Schema: &openapi.Schema{Ref: "#/components/schemas/Item"}}},
						Headers: []openapi.NamedHeader{{
							Name: "X-Request-Id", Header: openapi.Header{Description: "Correlation", Schema: &openapi.Schema{Type: []string{openapi.TypeString}}},
						}},
						Links: []openapi.NamedLink{{
							Name: "next",
							Link: openapi.Link{
								OperationID: "items.list", OperationRef: "#/paths/~1items/get", Description: "The list",
								Parameters:  []openapi.NamedValue{{Name: "after", Value: "$response.body#/id"}},
								RequestBody: map[string]any{"copy": true}, HasBody: true,
								Server: &openapi.Server{URL: "https://example.com"},
							},
						}},
					},
					{Description: "Anything else."},
				},
				Security: []openapi.SecurityRequirement{{Name: "bearer", Scopes: []string{"items:read"}}, {}},
				Servers:  []openapi.Server{{URL: "https://items.example.com"}},
			}},
		}},
		Components: openapi.Components{
			Schemas:       []openapi.NamedSchema{{Name: "Item", Schema: &openapi.Schema{Type: []string{openapi.TypeObject}}}},
			Responses:     []openapi.NamedResponse{{Name: "NotFound", Response: openapi.Response{Status: 404, Description: "Missing."}}},
			Parameters:    []openapi.NamedParameter{{Name: "Trace", Parameter: openapi.Parameter{Name: "trace", In: openapi.InHeader}}},
			Examples:      []openapi.NamedExample{{Name: "shared", Example: openapi.Example{Summary: "Shared"}}},
			RequestBodies: []openapi.NamedRequestBody{{Name: "ItemBody", RequestBody: openapi.RequestBody{Content: []openapi.MediaType{{Type: "application/json"}}}}},
			Headers:       []openapi.NamedHeader{{Name: "X-Shared", Header: openapi.Header{Description: "Shared"}}},
			Links:         []openapi.NamedLink{{Name: "shared", Link: openapi.Link{OperationID: "items.list"}}},
			SecuritySchemes: []openapi.SecurityScheme{
				{Name: "apiKey", Type: openapi.SecurityAPIKey, Description: "A key", In: openapi.InHeader, ParamName: "X-API-Key"},
				{Name: "bearer", Type: openapi.SecurityHTTP, Scheme: "bearer", BearerFormat: "JWT"},
				{Name: "oidc", Type: openapi.SecurityOpenIDConnect, OpenIDConnectURL: "https://example.com/.well-known/openid-configuration"},
				{Name: "mtls", Type: openapi.SecurityMutualTLS},
				{Name: "oauth", Type: openapi.SecurityOAuth2, Flows: &openapi.OAuthFlows{
					Implicit:          &openapi.OAuthFlow{AuthorizationURL: "https://example.com/authorize", Scopes: []openapi.Scope{{Name: "items:read", Description: "Read items"}}},
					Password:          &openapi.OAuthFlow{TokenURL: "https://example.com/token"},
					ClientCredentials: &openapi.OAuthFlow{TokenURL: "https://example.com/token", RefreshURL: "https://example.com/refresh"},
					AuthorizationCode: &openapi.OAuthFlow{AuthorizationURL: "https://example.com/authorize", TokenURL: "https://example.com/token"},
				}},
			},
		},
		Security:     []openapi.SecurityRequirement{{Name: "apiKey"}},
		Tags:         []openapi.Tag{{Name: "items", Description: "Everything about items", ExternalDocs: &openapi.ExternalDocs{URL: "https://example.com/items"}}},
		ExternalDocs: &openapi.ExternalDocs{Description: "The manual", URL: "https://example.com/manual"},
	}
}

// The JSON a document emits must be JSON — checked by the standard library
// rather than by reading it, so a quoting mistake cannot pass unnoticed.
func TestDocumentJSONIsValidJSON(t *testing.T) {
	var decoded map[string]any
	if err := json.Unmarshal(kitchenSink().JSON(), &decoded); err != nil {
		t.Fatalf("emitted JSON does not parse: %v", err)
	}
	if decoded["openapi"] != openapi.Version {
		t.Errorf("openapi = %v, want %s", decoded["openapi"], openapi.Version)
	}
	info, _ := decoded["info"].(map[string]any)
	if info["title"] != "Example API" || info["version"] != "2.1.0" {
		t.Errorf("info = %v", info)
	}
	if _, ok := decoded["paths"].(map[string]any)["/items/{item_id}"]; !ok {
		t.Errorf("paths = %v", decoded["paths"])
	}
}

// Both emitters must describe the same document, so anything that appears in
// one appears in the other.
func TestDocumentYAMLCarriesTheSameContent(t *testing.T) {
	yaml := string(kitchenSink().YAML())
	for _, want := range []string{
		"openapi: " + openapi.Version,
		"title: Example API",
		"/items/{item_id}:",
		"get:",
		"operationId: items.read",
		"securitySchemes:",
		"bearerFormat: JWT",
		"openIdConnectUrl:",
		"authorizationCode:",
		"items:read",
	} {
		if !strings.Contains(yaml, want) {
			t.Errorf("YAML is missing %q\n%s", want, yaml)
		}
	}
}

// A document emitted twice is byte-identical, which is what lets a change to
// it be reviewed as a diff.
func TestEmittersAreDeterministic(t *testing.T) {
	if a, b := kitchenSink().JSON(), kitchenSink().JSON(); !bytes.Equal(a, b) {
		t.Error("JSON differs between two emissions of the same document")
	}
	if a, b := kitchenSink().YAML(), kitchenSink().YAML(); !bytes.Equal(a, b) {
		t.Error("YAML differs between two emissions of the same document")
	}
}

// The smallest document still has to be well formed: an API with no paths has
// an empty paths object, not a missing one.
func TestMinimalDocument(t *testing.T) {
	doc := &openapi.Document{Info: openapi.Info{Title: "Tiny", Version: "1.0.0"}}

	var decoded map[string]any
	if err := json.Unmarshal(doc.JSON(), &decoded); err != nil {
		t.Fatalf("emitted JSON does not parse: %v", err)
	}
	paths, ok := decoded["paths"].(map[string]any)
	if !ok || len(paths) != 0 {
		t.Errorf("paths = %v, want an empty object", decoded["paths"])
	}
	if _, present := decoded["components"]; present {
		t.Error("an empty components object should be left out entirely")
	}

	yaml := string(doc.YAML())
	if !strings.Contains(yaml, "paths: {}") {
		t.Errorf("YAML should spell an empty object rather than a bare key:\n%s", yaml)
	}
}

// A value YAML would read back as something other than the string it was
// written as has to be quoted; one it would read back unchanged should not be,
// because quoting everything makes a document tiring to read.
func TestYAMLQuotesOnlyWhatItMustEmit(t *testing.T) {
	tests := []struct {
		name   string
		value  string
		quoted bool
	}{
		{"plain", "an ordinary description", false},
		{"path-like", "/items/{item_id}", false},
		{"empty", "", true},
		{"true", "true", true},
		{"yes", "yes", true},
		{"null", "null", true},
		{"tilde", "~", true},
		{"integer", "42", true},
		{"float", "1.5", true},
		{"hex", "0x1f", true},
		{"leading dash", "-dash", true},
		{"leading brace", "{brace", true},
		{"leading hash", "#hash", true},
		{"leading quote", `"quoted"`, true},
		{"trailing space", "trails ", true},
		{"colon space", "key: value", true},
		{"space hash", "text # comment", true},
		{"trailing colon", "ends:", true},
		{"newline", "two\nlines", true},
		{"carriage return", "two\rlines", true},
		{"tab", "a\tb", true},
		{"control", "a\x01b", true},
		{"unprintable", "ab", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			doc := &openapi.Document{Info: openapi.Info{Title: "T", Version: "1", Description: tt.value}}
			yaml := string(doc.YAML())
			if tt.value == "" {
				// An empty description is left out entirely, so the quoting
				// rule is exercised through a title instead.
				doc = &openapi.Document{Info: openapi.Info{Title: tt.value, Version: "1"}}
				yaml = string(doc.YAML())
				if !strings.Contains(yaml, `title: ""`) {
					t.Errorf("an empty string must be quoted:\n%s", yaml)
				}
				return
			}
			quoted := strings.Contains(yaml, `description: "`)
			if quoted != tt.quoted {
				t.Errorf("quoted = %v, want %v, for %q:\n%s", quoted, tt.quoted, tt.value, yaml)
			}
		})
	}
}

// A schema carries every keyword a validation rule can produce, so the emitter
// is exercised over all of them at once.
func TestSchemaEmitsEveryKeyword(t *testing.T) {
	s := &openapi.Schema{
		Type:        []string{openapi.TypeObject},
		Format:      "custom",
		Title:       "Item",
		Description: "One item.",
		Deprecated:  true,
		ReadOnly:    true,
		WriteOnly:   true,
		Default:     map[string]any{"name": "Boots"},
		HasDefault:  true,
		Const:       "fixed",
		HasConst:    true,
		Example:     "an example",
		HasExample:  true,
		Examples:    []any{"one", 2},
		Enum:        []any{"a", "b"},
		Properties: []openapi.NamedSchema{
			{Name: "name", Schema: &openapi.Schema{
				Type: []string{openapi.TypeString}, MinLength: ptr(1), MaxLength: ptr(200),
				Pattern: `^\p{L}+$`, ContentEncoding: "base64", ContentMediaType: "application/json",
			}},
			{Name: "count", Schema: &openapi.Schema{
				Type: []string{openapi.TypeInteger}, Minimum: ptr(1.0), Maximum: ptr(10.0),
				ExclusiveMinimum: ptr(0.0), ExclusiveMaximum: ptr(11.0), MultipleOf: ptr(2.0),
			}},
			{Name: "tags", Schema: &openapi.Schema{
				Type: []string{openapi.TypeArray}, Items: &openapi.Schema{Type: []string{openapi.TypeString}},
				MinItems: ptr(1), MaxItems: ptr(5), UniqueItems: true,
			}},
			{Name: "ref", Schema: &openapi.Schema{Ref: "#/components/schemas/Other"}},
		},
		Required:      []string{"name"},
		MinProperties: ptr(1),
		MaxProperties: ptr(9),
		OneOf:         []*openapi.Schema{{Type: []string{openapi.TypeString}}},
		AnyOf:         []*openapi.Schema{{Type: []string{openapi.TypeNumber}}},
		AllOf:         []*openapi.Schema{{Type: []string{openapi.TypeObject}}},
		Not:           &openapi.Schema{Type: []string{openapi.TypeNull}},
		Defs:          []openapi.NamedSchema{{Name: "Other", Schema: &openapi.Schema{Type: []string{openapi.TypeString}}}},
	}

	var decoded map[string]any
	if err := json.Unmarshal(s.JSON(), &decoded); err != nil {
		t.Fatalf("emitted schema does not parse: %v", err)
	}
	for _, key := range []string{
		"type", "format", "title", "description", "default", "const", "enum", "example", "examples",
		"properties", "required", "minProperties", "maxProperties",
		"oneOf", "anyOf", "allOf", "not", "deprecated", "readOnly", "writeOnly", "$defs",
	} {
		if _, ok := decoded[key]; !ok {
			t.Errorf("schema is missing %q", key)
		}
	}
	if _, ok := decoded["additionalProperties"]; ok {
		t.Error("additionalProperties should be absent when nothing said anything about it")
	}
}

// A nullable value is a type array in 3.1, and saying it twice does not make
// it doubly null.
func TestNullableIsATypeArray(t *testing.T) {
	s := (&openapi.Schema{Type: []string{openapi.TypeString}}).Nullable().Nullable()

	var decoded map[string]any
	if err := json.Unmarshal(s.JSON(), &decoded); err != nil {
		t.Fatalf("emitted schema does not parse: %v", err)
	}
	types, ok := decoded["type"].([]any)
	if !ok || len(types) != 2 || types[0] != "string" || types[1] != "null" {
		t.Errorf("type = %v, want [string null]", decoded["type"])
	}
}

// A schema that forbids extra properties says so as a boolean; one that
// describes them says so as a schema.
func TestAdditionalProperties(t *testing.T) {
	closed := &openapi.Schema{Type: []string{openapi.TypeObject}, NoAdditional: true}
	if got := string(closed.JSON()); !strings.Contains(got, `"additionalProperties": false`) {
		t.Errorf("closed schema = %s", got)
	}

	open := &openapi.Schema{Type: []string{openapi.TypeObject}, AdditionalProperties: &openapi.Schema{Type: []string{openapi.TypeString}}}
	if got := string(open.JSON()); !strings.Contains(got, `"additionalProperties": {`) {
		t.Errorf("open schema = %s", got)
	}
}

// Defaults and examples arrive as whatever type the field was, so every kind
// the field model can hand over has to render.
func TestValuesOfEveryKindRender(t *testing.T) {
	type point struct{ X int }

	tests := []struct {
		name  string
		value any
		want  string
	}{
		{"nil", nil, "null"},
		{"bool", true, "true"},
		{"int", 42, "42"},
		{"int64", int64(-7), "-7"},
		{"uint", uint(9), "9"},
		{"float", 1.5, "1.5"},
		{"string", "text", `"text"`},
		{"nil pointer", (*int)(nil), "null"},
		{"pointer", ptr(3), "3"},
		{"slice", []any{1, "two"}, "[\n"},
		{"map", map[string]any{"b": 2, "a": 1}, `"a": 1`},
		{"struct", point{X: 1}, `"{1}"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := &openapi.Schema{Default: tt.value, HasDefault: true}
			if got := string(s.JSON()); !strings.Contains(got, tt.want) {
				t.Errorf("schema = %s, want it to contain %s", got, tt.want)
			}
		})
	}
}

// A map renders its keys in a fixed order, since a map is the one input whose
// own order is not stable.
func TestMapValuesSortTheirKeys(t *testing.T) {
	s := &openapi.Schema{Default: map[string]any{"c": 3, "a": 1, "b": 2}, HasDefault: true}
	got := string(s.JSON())
	a, b, c := strings.Index(got, `"a"`), strings.Index(got, `"b"`), strings.Index(got, `"c"`)
	if !(a < b && b < c) {
		t.Errorf("map keys are not sorted:\n%s", got)
	}
}

// An empty collection has to be spelled, in both formats: a bare key reads
// back as null, which is not the same as having none.
func TestEmptyCollectionsAreSpelled(t *testing.T) {
	doc := &openapi.Document{
		Info: openapi.Info{Title: "T", Version: "1"},
		Paths: []openapi.PathItem{{
			Path: "/x",
			Operations: []openapi.Operation{{
				Method:    "GET",
				Responses: []openapi.Response{{Status: 204, Description: "Nothing."}},
				Security:  []openapi.SecurityRequirement{{Name: "bearer"}},
			}},
		}},
	}

	yaml := string(doc.YAML())
	if !strings.Contains(yaml, "bearer: []") {
		t.Errorf("a scheme granting no scopes must still say so:\n%s", yaml)
	}
	if got := string(doc.JSON()); !strings.Contains(got, `"bearer": []`) {
		t.Errorf("JSON = %s", got)
	}
}
