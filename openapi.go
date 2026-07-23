package tork

import (
	"net/http"
	"reflect"
	"slices"
	"sort"
	"strconv"

	"github.com/tork-go/web/openapi"
)

// docParam is one bound field, remembered for the document.
//
// It is recorded where the field is compiled, so the name here is the name the
// server actually reads — not a second derivation that could disagree with it.
type docParam struct {
	source    source
	name      string
	required  bool
	typ       reflect.Type
	rules     []rule
	fallback  *reflect.Value
	file      bool
	fileMulti bool
	csv       bool
}

// docBody is the request body a route decodes, remembered for the document.
type docBody struct{ typ reflect.Type }

// OpenAPI builds the document describing this application.
//
// It reads the same route model, field model, response types, and dependencies
// the server runs on, so nothing has to be written twice to be documented: a
// parameter appears because a field binds it, a schema because a handler
// returns it, a security requirement because a route declared one. The
// document covers the routes of one API version — the unversioned ones, or the
// first version declared when every route belongs to one — and OpenAPIFor asks
// for another by name.
func (a *App) OpenAPI() (*openapi.Document, error) {
	routes, err := a.Routes()
	if err != nil {
		return nil, err
	}
	return a.document(routes, defaultVersion(routes)), nil
}

// OpenAPIFor builds the document for one declared API version.
func (a *App) OpenAPIFor(version string) (*openapi.Document, error) {
	routes, err := a.Routes()
	if err != nil {
		return nil, err
	}
	return a.document(routes, version), nil
}

// defaultVersion is the version the bare endpoints describe: the routes that
// belong to no version, or — when every route was declared under one, which is
// what an application with a /api/v1 prefix looks like — the first version
// declared, so that the default document is never empty.
func defaultVersion(routes []*Route) string {
	for _, route := range routes {
		if route.Version == "" {
			return ""
		}
	}
	if len(routes) > 0 {
		return routes[0].Version
	}
	return ""
}

// versionsOf lists the declared versions, in the order their first route was
// declared. There is no register of versions anywhere else: a version exists
// because a route says it belongs to one.
func versionsOf(routes []*Route) []string {
	var versions []string
	for _, route := range routes {
		if route.Version != "" && !slices.Contains(versions, route.Version) {
			versions = append(versions, route.Version)
		}
	}
	return versions
}

// document builds one version's description.
func (a *App) document(routes []*Route, version string) *openapi.Document {
	cfg := a.docs.resolved()
	schemas := newSchemaBuilder()

	doc := &openapi.Document{
		Info: openapi.Info{
			Title:          a.info.title,
			Description:    a.info.description,
			Version:        cfg.Version,
			TermsOfService: cfg.TermsOfService,
			Contact:        cfg.Contact,
			License:        cfg.License,
		},
		Servers:      cfg.serverList(),
		Security:     cfg.Security,
		ExternalDocs: cfg.ExternalDocs,
	}
	if a.info.title == "" {
		doc.Info.Title = "API"
	}
	for _, scheme := range a.info.schemes {
		doc.Components.SecuritySchemes = append(doc.Components.SecuritySchemes, scheme)
	}

	// Routes come out in declaration order, and every operation on one path is
	// gathered under it, so the document follows the source rather than the
	// alphabet.
	byPath := map[string]int{}
	for _, route := range routes {
		if route.Version != version {
			continue
		}
		item, seen := byPath[route.Path]
		if !seen {
			byPath[route.Path] = len(doc.Paths)
			doc.Paths = append(doc.Paths, openapi.PathItem{Path: route.Path})
			item = len(doc.Paths) - 1
		}
		doc.Paths[item].Operations = append(doc.Paths[item].Operations, operationOf(route, schemas))
	}

	doc.Components.Schemas = schemas.components()
	doc.Tags = tagsOf(routes, version)
	return doc
}

// tagsOf collects the tags the operations use, so a reader sees the groups
// even before any of them is described.
func tagsOf(routes []*Route, version string) []openapi.Tag {
	var tags []openapi.Tag
	for _, route := range routes {
		if route.Version != version {
			continue
		}
		for _, name := range route.Tags {
			if !slices.ContainsFunc(tags, func(t openapi.Tag) bool { return t.Name == name }) {
				tags = append(tags, openapi.Tag{Name: name})
			}
		}
	}
	return tags
}

// operationOf describes one route.
func operationOf(route *Route, schemas *schemaBuilder) openapi.Operation {
	op := openapi.Operation{
		Method:      route.Method,
		Tags:        route.Tags,
		Summary:     route.Summary,
		Description: route.Description,
		OperationID: route.OperationID,
		Deprecated:  route.Deprecated,
		Security:    route.security,
		Responses:   responsesOf(route, schemas),
	}

	var form []docParam
	for _, p := range route.plan.bound {
		if p.source == sourceForm {
			form = append(form, p)
			continue
		}
		op.Parameters = append(op.Parameters, parameterOf(p, schemas))
	}

	switch {
	case route.plan.body != nil:
		op.RequestBody = jsonBodyOf(route.plan.body.typ, schemas)
	case len(form) > 0:
		op.RequestBody = formBodyOf(form, schemas)
	}
	return op
}

// parameterOf describes one bound field as a parameter.
func parameterOf(p docParam, schemas *schemaBuilder) openapi.Parameter {
	schema := schemas.schemaFor(p.typ)
	describeRules(p.rules, schema)
	if p.fallback != nil {
		schema.Default, schema.HasDefault = p.fallback.Interface(), true
	}

	param := openapi.Parameter{
		Name:   p.name,
		In:     inOf(p.source),
		Schema: schema,
		// A path parameter is part of the path, so it is required whatever the
		// declaration said; there is no request that reaches the route without
		// it.
		Required: p.required || p.source == sourcePath,
	}
	// A list written as one comma-separated value is a different serialization
	// from the same name repeated, and the document has to say which.
	if p.csv {
		param.Style, param.Explode = "form", new(bool)
	}
	return param
}

// inOf maps a binding source to the place the specification calls it. A form
// field is not here: it is part of the body, not of the request line.
func inOf(s source) string {
	switch s {
	case sourcePath:
		return openapi.InPath
	case sourceQuery:
		return openapi.InQuery
	case sourceHeader:
		return openapi.InHeader
	default:
		return openapi.InCookie
	}
}

// jsonBodyOf describes a JSON request body, overlaying whatever the body's own
// declaration said about its fields onto the structural schema.
func jsonBodyOf(t reflect.Type, schemas *schemaBuilder) *openapi.RequestBody {
	schema := schemas.schemaFor(t)
	describeBodyRules(t, schemas)
	return &openapi.RequestBody{
		Required: true,
		Content:  []openapi.MediaType{{Type: contentTypeJSON, Schema: schema}},
	}
}

// describeBodyRules writes a declared body's rules into the component schema
// the structural pass already built for it.
//
// The rules live in their own registry, keyed by type, because a body is
// declared with DefineBody rather than by binding fields — so the schema is
// built from the Go type and then told what the declaration added.
func describeBodyRules(t reflect.Type, schemas *schemaBuilder) {
	rules := lookupBody(t)
	if rules == nil {
		return
	}
	// The structural pass ran first and a body is always a named struct, so
	// the component it registered is there to be added to.
	schema := schemas.schemas[t]
	for _, field := range rules.fields {
		for _, property := range schema.Properties {
			if property.Name != field.name {
				continue
			}
			describeRules(field.rules, property.Schema)
			if field.required && !slices.Contains(schema.Required, field.name) {
				schema.Required = append(schema.Required, field.name)
			}
		}
	}
}

// formBodyOf describes the fields a form or a multipart body carries.
//
// A form is a body rather than a set of parameters, so the fields become the
// properties of one object; an upload among them makes it multipart, since
// that is the only encoding that can carry a file.
func formBodyOf(fields []docParam, schemas *schemaBuilder) *openapi.RequestBody {
	schema := &openapi.Schema{Type: []string{openapi.TypeObject}}
	multipart := false
	var encoding []openapi.Encoding

	for _, f := range fields {
		property := schemas.schemaFor(f.typ)
		describeRules(f.rules, property)
		if f.fallback != nil {
			property.Default, property.HasDefault = f.fallback.Interface(), true
		}
		schema.Properties = append(schema.Properties, openapi.NamedSchema{Name: f.name, Schema: property})
		if f.required {
			schema.Required = append(schema.Required, f.name)
		}
		if f.file {
			multipart = true
			encoding = append(encoding, openapi.Encoding{
				Property:    f.name,
				ContentType: "application/octet-stream",
			})
		}
	}

	media := openapi.MediaType{Type: "application/x-www-form-urlencoded", Schema: schema}
	if multipart {
		media.Type, media.Encoding = "multipart/form-data", encoding
	}
	return &openapi.RequestBody{Required: true, Content: []openapi.MediaType{media}}
}

// responsesOf describes what an operation answers with: what its result type
// says, and whatever Responds and Throws added.
func responsesOf(route *Route, schemas *schemaBuilder) []openapi.Response {
	responses := []openapi.Response{successOf(route, schemas)}

	// Declared responses come next, by status, so two builds order them the
	// same way.
	statuses := make([]int, 0, len(route.Responses))
	for status := range route.Responses {
		statuses = append(statuses, status)
	}
	sort.Ints(statuses)
	for _, status := range statuses {
		doc := route.Responses[status]
		if status == responses[0].Status {
			continue
		}
		responses = append(responses, declaredResponse(status, doc.Description, doc.Type, schemas))
	}

	// Thrown error shapes have no status of their own — the shape decides it
	// when it is returned — so they are described once, under default.
	if len(route.Throws) > 0 {
		responses = append(responses, thrownResponse(route, schemas))
	}
	return responses
}

// successOf is the response the handler's own signature promises.
func successOf(route *Route, schemas *schemaBuilder) openapi.Response {
	// A handler returning only an error has nothing to say when it succeeds.
	if route.plan.result == nil {
		return openapi.Response{Status: http.StatusNoContent, Description: "No content."}
	}

	status, contentType, body := http.StatusOK, contentTypeJSON, route.plan.result
	if spec := route.ResponseSpec; spec != nil {
		if spec.Status != 0 {
			status = spec.Status
		}
		if spec.ContentType != "" {
			contentType = spec.ContentType
		}
		// A response type that names the Go type of its body describes that;
		// one that does not — a file, a stream — is bytes, and says so.
		body = spec.BodyType
	}

	response := openapi.Response{Status: status, Description: statusText(status)}
	if body == nil {
		if spec := route.ResponseSpec; spec != nil && spec.ContentType == "" {
			return response
		}
		response.Content = []openapi.MediaType{{Type: contentType}}
		return response
	}
	response.Content = []openapi.MediaType{{Type: contentType, Schema: schemas.schemaFor(body)}}
	return response
}

func declaredResponse(status int, description string, t reflect.Type, schemas *schemaBuilder) openapi.Response {
	if description == "" {
		description = statusText(status)
	}
	response := openapi.Response{Status: status, Description: description}
	if t != nil {
		response.Content = []openapi.MediaType{{Type: contentTypeJSON, Schema: schemas.schemaFor(t)}}
	}
	return response
}

// thrownResponse describes the error shapes a route declared it may throw, as
// the one response that covers whatever the named statuses did not.
func thrownResponse(route *Route, schemas *schemaBuilder) openapi.Response {
	types := make([]reflect.Type, 0, len(route.Throws))
	for t := range route.Throws {
		types = append(types, t)
	}
	sort.Slice(types, func(i, j int) bool { return types[i].String() < types[j].String() })

	shapes := make([]*openapi.Schema, len(types))
	for i, t := range types {
		shapes[i] = schemas.schemaFor(t)
	}

	schema := shapes[0]
	if len(shapes) > 1 {
		schema = &openapi.Schema{OneOf: shapes}
	}
	return openapi.Response{
		Status:      0,
		Description: "An error.",
		Content:     []openapi.MediaType{{Type: contentTypeJSON, Schema: schema}},
	}
}

// statusText is http.StatusText with a fallback, so a status the standard
// library has never heard of still gets a description rather than an empty
// one, which the specification forbids.
func statusText(status int) string {
	if text := http.StatusText(status); text != "" {
		return text
	}
	return "Status " + strconv.Itoa(status)
}
