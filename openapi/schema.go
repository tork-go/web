package openapi

import "slices"

// Schema is a JSON Schema as OpenAPI 3.1 uses it.
//
// 3.1 dropped the dialect of its own that earlier versions carried, so this is
// ordinary JSON Schema: nullability is a type array rather than a "nullable"
// flag, and $ref sits beside other keywords rather than replacing them. The
// fields are pointers wherever zero is a meaningful value — a minimum of 0 and
// no minimum at all are different documents.
//
// Only the keywords something can actually produce are here. A validation rule
// that has no JSON Schema equivalent contributes nothing rather than being
// approximated, which is why there is no field for "checked by a function".
type Schema struct {
	// Ref points at an entry in components/schemas. When it is set the
	// remaining fields are left alone, since a reference describes nothing
	// itself.
	Ref string

	// Type is the set of types a value may be, which for a nullable value is
	// the type and "null". Format narrows it: a string that is a date-time,
	// an integer that is int64.
	Type   []string
	Format string

	Title       string
	Description string
	Deprecated  bool
	ReadOnly    bool
	WriteOnly   bool

	// Default, Const, and Example carry arbitrary values, so each has a
	// companion flag: a default of zero, false, or "" is a real default that
	// a missing one must not be confused with.
	Default    any
	HasDefault bool
	Const      any
	HasConst   bool
	Example    any
	HasExample bool
	Examples   []any
	Enum       []any

	// Objects.
	Properties           []NamedSchema
	Required             []string
	AdditionalProperties *Schema
	NoAdditional         bool
	MinProperties        *int
	MaxProperties        *int

	// Arrays.
	Items       *Schema
	MinItems    *int
	MaxItems    *int
	UniqueItems bool

	// Strings.
	MinLength        *int
	MaxLength        *int
	Pattern          string
	ContentEncoding  string
	ContentMediaType string

	// Numbers.
	Minimum          *float64
	Maximum          *float64
	ExclusiveMinimum *float64
	ExclusiveMaximum *float64
	MultipleOf       *float64

	// Composition.
	OneOf []*Schema
	AnyOf []*Schema
	AllOf []*Schema
	Not   *Schema

	// Defs holds schemas a standalone schema refers to. A document puts them
	// in components instead; this is for a schema emitted on its own.
	Defs []NamedSchema
}

// The type names JSON Schema uses.
const (
	TypeString  = "string"
	TypeNumber  = "number"
	TypeInteger = "integer"
	TypeBoolean = "boolean"
	TypeObject  = "object"
	TypeArray   = "array"
	TypeNull    = "null"
)

// Nullable adds "null" to the schema's types, which is how 3.1 spells a value
// that may be absent in the sense of explicitly null rather than missing.
func (s *Schema) Nullable() *Schema {
	if slices.Contains(s.Type, TypeNull) {
		return s
	}
	s.Type = append(s.Type, TypeNull)
	return s
}

// JSON renders a schema on its own, for a test or a tool that wants one
// without the document around it.
func (s *Schema) JSON() []byte { return s.node().JSON() }

func (s *Schema) node() *node {
	m := mapping()
	if s.Ref != "" {
		m.set("$ref", stringValue(s.Ref))
		return m
	}

	switch len(s.Type) {
	case 0:
	case 1:
		m.set("type", stringValue(s.Type[0]))
	default:
		m.setStrings("type", s.Type)
	}
	m.setString("format", s.Format)
	m.setString("title", s.Title)
	m.setString("description", s.Description)

	if s.HasDefault {
		m.set("default", valueNode(s.Default))
	}
	if s.HasConst {
		m.set("const", valueNode(s.Const))
	}
	if len(s.Enum) > 0 {
		m.set("enum", valuesNode(s.Enum))
	}
	if s.HasExample {
		m.set("example", valueNode(s.Example))
	}
	if len(s.Examples) > 0 {
		m.set("examples", valuesNode(s.Examples))
	}

	if len(s.Properties) > 0 {
		props := mapping()
		for _, p := range s.Properties {
			props.set(p.Name, p.Schema.node())
		}
		m.set("properties", props)
	}
	m.setStrings("required", s.Required)
	if s.NoAdditional {
		m.set("additionalProperties", boolValue(false))
	} else if s.AdditionalProperties != nil {
		m.set("additionalProperties", s.AdditionalProperties.node())
	}
	m.setInt("minProperties", s.MinProperties)
	m.setInt("maxProperties", s.MaxProperties)

	if s.Items != nil {
		m.set("items", s.Items.node())
	}
	m.setInt("minItems", s.MinItems)
	m.setInt("maxItems", s.MaxItems)
	m.setBool("uniqueItems", s.UniqueItems)

	m.setInt("minLength", s.MinLength)
	m.setInt("maxLength", s.MaxLength)
	m.setString("pattern", s.Pattern)
	m.setString("contentEncoding", s.ContentEncoding)
	m.setString("contentMediaType", s.ContentMediaType)

	m.setFloat("minimum", s.Minimum)
	m.setFloat("maximum", s.Maximum)
	m.setFloat("exclusiveMinimum", s.ExclusiveMinimum)
	m.setFloat("exclusiveMaximum", s.ExclusiveMaximum)
	m.setFloat("multipleOf", s.MultipleOf)

	setSchemaList(m, "oneOf", s.OneOf)
	setSchemaList(m, "anyOf", s.AnyOf)
	setSchemaList(m, "allOf", s.AllOf)
	if s.Not != nil {
		m.set("not", s.Not.node())
	}

	m.setBool("deprecated", s.Deprecated)
	m.setBool("readOnly", s.ReadOnly)
	m.setBool("writeOnly", s.WriteOnly)

	if len(s.Defs) > 0 {
		defs := mapping()
		for _, d := range s.Defs {
			defs.set(d.Name, d.Schema.node())
		}
		m.set("$defs", defs)
	}
	return m
}

func setSchemaList(m *node, key string, schemas []*Schema) {
	if len(schemas) == 0 {
		return
	}
	items := make([]*node, len(schemas))
	for i, s := range schemas {
		items[i] = s.node()
	}
	m.set(key, sequence(items...))
}

func valuesNode(values []any) *node {
	items := make([]*node, len(values))
	for i, v := range values {
		items[i] = valueNode(v)
	}
	return sequence(items...)
}
