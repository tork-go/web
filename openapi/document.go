package openapi

import (
	"strconv"
	"strings"
)

// Version is the specification this package emits. It is a constant rather
// than a field because a document that claims a version it was not built for
// is worse than one that only speaks a single dialect; a 3.2 emitter joins as
// its own function, leaving this one alone.
const Version = "3.1.2"

// Document is a whole OpenAPI description.
//
// Everything here is a plain Go struct with no behaviour beyond turning itself
// into output, and nothing in this package knows what a route, a handler, or a
// dependency is. That separation is the point: this is "OpenAPI as Go types,
// plus a way to write them out", so the builder that reads an application and
// the emitter that writes a document can each change without the other.
//
// Ordered slices appear where the specification asks for an object — paths,
// responses, properties. The specification's objects are unordered, but a
// document that changes shape between two builds of the same application
// cannot be diffed, so order is decided once, by the builder, and preserved
// from there.
type Document struct {
	Info         Info
	Servers      []Server
	Paths        []PathItem
	Components   Components
	Security     []SecurityRequirement
	Tags         []Tag
	ExternalDocs *ExternalDocs
}

// Info is what the document says about the API as a whole.
type Info struct {
	Title          string
	Summary        string
	Description    string
	TermsOfService string
	Contact        *Contact
	License        *License
	Version        string
}

// Contact is who to ask about the API.
type Contact struct {
	Name  string
	URL   string
	Email string
}

// License is the terms the API is offered under. Identifier is an SPDX
// expression and is mutually exclusive with URL in the specification; both are
// carried here and the builder decides which it sets.
type License struct {
	Name       string
	Identifier string
	URL        string
}

// Server is one place the API is served from.
type Server struct {
	URL         string
	Description string
	Variables   []ServerVariable
}

// ServerVariable is a substitution in a server URL template.
type ServerVariable struct {
	Name        string
	Default     string
	Description string
	Enum        []string
}

// Tag groups operations, and is where a group's prose lives — an operation
// carries only the tag's name.
type Tag struct {
	Name         string
	Description  string
	ExternalDocs *ExternalDocs
}

// ExternalDocs points at prose that does not belong in the document.
type ExternalDocs struct {
	Description string
	URL         string
}

// PathItem is every operation on one path, plus whatever they share.
type PathItem struct {
	Path        string
	Summary     string
	Description string
	Parameters  []Parameter
	Operations  []Operation
}

// Operation is one method on one path.
type Operation struct {
	Method       string
	Tags         []string
	Summary      string
	Description  string
	ExternalDocs *ExternalDocs
	OperationID  string
	Parameters   []Parameter
	RequestBody  *RequestBody
	Responses    []Response
	Deprecated   bool
	Security     []SecurityRequirement
	Servers      []Server
}

// Parameter is one value read from the request line or its headers.
//
// A body is not a parameter — it is RequestBody — which is why In is only ever
// path, query, header, or cookie.
type Parameter struct {
	Name            string
	In              string
	Description     string
	Required        bool
	Deprecated      bool
	AllowEmptyValue bool
	Style           string
	Explode         *bool
	Schema          *Schema
	Example         any
	HasExample      bool
	Examples        []NamedExample
}

// The four places a parameter is read from.
const (
	InPath   = "path"
	InQuery  = "query"
	InHeader = "header"
	InCookie = "cookie"
)

// RequestBody is the body an operation accepts, in each media type it accepts
// it as.
type RequestBody struct {
	Description string
	Required    bool
	Content     []MediaType
}

// MediaType is one wire format a body or a response is available in.
type MediaType struct {
	Type       string
	Schema     *Schema
	Example    any
	HasExample bool
	Examples   []NamedExample
	Encoding   []Encoding
}

// Encoding says how one property of a multipart or form body is serialized —
// which is how an uploaded file declares its content type.
type Encoding struct {
	Property    string
	ContentType string
	Headers     []NamedHeader
	Style       string
	Explode     *bool
}

// Response is what an operation answers with for one status.
//
// Status is the HTTP status, or zero for the specification's "default"
// response — the one that covers whatever the others did not.
type Response struct {
	Status      int
	Description string
	Headers     []NamedHeader
	Content     []MediaType
	Links       []NamedLink
}

// Header is a response header. It is a parameter in all but name, minus the
// name itself, which the map it lives in supplies.
type Header struct {
	Description string
	Required    bool
	Deprecated  bool
	Schema      *Schema
}

// Link describes how one operation's result feeds another's parameters.
type Link struct {
	OperationID  string
	OperationRef string
	Description  string
	Parameters   []NamedValue
	RequestBody  any
	HasBody      bool
	Server       *Server
}

// Example is a value a reader can copy, with prose about when it applies.
type Example struct {
	Summary       string
	Description   string
	Value         any
	HasValue      bool
	ExternalValue string
}

// Components is everything the document refers to by name rather than
// repeating: the schemas a $ref points at, and the ways in which a request may
// authenticate.
type Components struct {
	Schemas         []NamedSchema
	Responses       []NamedResponse
	Parameters      []NamedParameter
	Examples        []NamedExample
	RequestBodies   []NamedRequestBody
	Headers         []NamedHeader
	SecuritySchemes []SecurityScheme
	Links           []NamedLink
}

// The Named* types are how this package spells an object whose keys are
// chosen by the application. A slice of these rather than a map is what lets
// the builder fix the order once and every emitter honour it.
type (
	NamedSchema struct {
		Name   string
		Schema *Schema
	}
	NamedResponse struct {
		Name     string
		Response Response
	}
	NamedParameter struct {
		Name      string
		Parameter Parameter
	}
	NamedExample struct {
		Name    string
		Example Example
	}
	NamedRequestBody struct {
		Name        string
		RequestBody RequestBody
	}
	NamedHeader struct {
		Name   string
		Header Header
	}
	NamedLink struct {
		Name string
		Link Link
	}
	NamedValue struct {
		Name  string
		Value any
	}
)

// SecurityScheme is one way a request may prove who it is.
//
// The four kinds share a struct because a document holds them in one object;
// which fields matter is decided by Type, and the emitter writes only the ones
// that kind uses, so an apiKey scheme never emits an empty "scheme".
type SecurityScheme struct {
	Name        string
	Type        string
	Description string

	// apiKey: which parameter carries it, and where.
	In        string
	ParamName string

	// http: the authorization scheme, and the shape of a bearer token.
	Scheme       string
	BearerFormat string

	// oauth2, and openIdConnect.
	Flows            *OAuthFlows
	OpenIDConnectURL string
}

// The security scheme types the specification defines.
const (
	SecurityAPIKey        = "apiKey"
	SecurityHTTP          = "http"
	SecurityOAuth2        = "oauth2"
	SecurityOpenIDConnect = "openIdConnect"
	SecurityMutualTLS     = "mutualTLS"
)

// OAuthFlows is the set of OAuth 2 flows a scheme offers.
type OAuthFlows struct {
	Implicit          *OAuthFlow
	Password          *OAuthFlow
	ClientCredentials *OAuthFlow
	AuthorizationCode *OAuthFlow
}

// OAuthFlow is one flow's endpoints and the scopes it can grant.
type OAuthFlow struct {
	AuthorizationURL string
	TokenURL         string
	RefreshURL       string
	Scopes           []Scope
}

// Scope is one permission an OAuth flow can grant.
type Scope struct {
	Name        string
	Description string
}

// SecurityRequirement is one scheme a request may satisfy. A document or an
// operation carrying several means any one of them is enough; a requirement
// naming no scheme at all means the operation may also be called
// unauthenticated.
type SecurityRequirement struct {
	Name   string
	Scopes []string
}

// JSON renders the document as indented JSON.
func (d *Document) JSON() []byte { return d.node().JSON() }

// YAML renders the document as YAML.
func (d *Document) YAML() []byte { return d.node().YAML() }

func (d *Document) node() *node {
	m := mapping()
	m.set("openapi", stringValue(Version))
	m.set("info", d.Info.node())
	if len(d.Servers) > 0 {
		m.set("servers", serversNode(d.Servers))
	}
	paths := mapping()
	for _, p := range d.Paths {
		paths.set(p.Path, p.node())
	}
	m.set("paths", paths)
	if components := d.Components.node(); !components.empty() {
		m.set("components", components)
	}
	if len(d.Security) > 0 {
		m.set("security", securityNode(d.Security))
	}
	if len(d.Tags) > 0 {
		tags := make([]*node, len(d.Tags))
		for i, t := range d.Tags {
			tags[i] = t.node()
		}
		m.set("tags", sequence(tags...))
	}
	if d.ExternalDocs != nil {
		m.set("externalDocs", d.ExternalDocs.node())
	}
	return m
}

func (i Info) node() *node {
	m := mapping()
	m.set("title", stringValue(i.Title))
	m.setString("summary", i.Summary)
	m.setString("description", i.Description)
	m.setString("termsOfService", i.TermsOfService)
	if i.Contact != nil {
		c := mapping()
		c.setString("name", i.Contact.Name)
		c.setString("url", i.Contact.URL)
		c.setString("email", i.Contact.Email)
		m.set("contact", c)
	}
	if i.License != nil {
		l := mapping()
		l.set("name", stringValue(i.License.Name))
		l.setString("identifier", i.License.Identifier)
		l.setString("url", i.License.URL)
		m.set("license", l)
	}
	m.set("version", stringValue(i.Version))
	return m
}

func serversNode(servers []Server) *node {
	items := make([]*node, len(servers))
	for i, s := range servers {
		m := mapping()
		m.set("url", stringValue(s.URL))
		m.setString("description", s.Description)
		if len(s.Variables) > 0 {
			vars := mapping()
			for _, v := range s.Variables {
				vm := mapping()
				vm.setStrings("enum", v.Enum)
				vm.set("default", stringValue(v.Default))
				vm.setString("description", v.Description)
				vars.set(v.Name, vm)
			}
			m.set("variables", vars)
		}
		items[i] = m
	}
	return sequence(items...)
}

func (t Tag) node() *node {
	m := mapping()
	m.set("name", stringValue(t.Name))
	m.setString("description", t.Description)
	if t.ExternalDocs != nil {
		m.set("externalDocs", t.ExternalDocs.node())
	}
	return m
}

func (e ExternalDocs) node() *node {
	m := mapping()
	m.setString("description", e.Description)
	m.set("url", stringValue(e.URL))
	return m
}

func (p PathItem) node() *node {
	m := mapping()
	m.setString("summary", p.Summary)
	m.setString("description", p.Description)
	for _, op := range p.Operations {
		m.set(strings.ToLower(op.Method), op.node())
	}
	if len(p.Parameters) > 0 {
		m.set("parameters", parametersNode(p.Parameters))
	}
	return m
}

func (o Operation) node() *node {
	m := mapping()
	m.setStrings("tags", o.Tags)
	m.setString("summary", o.Summary)
	m.setString("description", o.Description)
	if o.ExternalDocs != nil {
		m.set("externalDocs", o.ExternalDocs.node())
	}
	m.setString("operationId", o.OperationID)
	if len(o.Parameters) > 0 {
		m.set("parameters", parametersNode(o.Parameters))
	}
	if o.RequestBody != nil {
		m.set("requestBody", o.RequestBody.node())
	}
	if len(o.Responses) > 0 {
		responses := mapping()
		for _, r := range o.Responses {
			responses.set(r.key(), r.node())
		}
		m.set("responses", responses)
	}
	m.setBool("deprecated", o.Deprecated)
	if len(o.Security) > 0 {
		m.set("security", securityNode(o.Security))
	}
	if len(o.Servers) > 0 {
		m.set("servers", serversNode(o.Servers))
	}
	return m
}

func parametersNode(params []Parameter) *node {
	items := make([]*node, len(params))
	for i, p := range params {
		items[i] = p.node()
	}
	return sequence(items...)
}

func (p Parameter) node() *node {
	m := mapping()
	m.set("name", stringValue(p.Name))
	m.set("in", stringValue(p.In))
	m.setString("description", p.Description)
	m.setBool("required", p.Required)
	m.setBool("deprecated", p.Deprecated)
	m.setBool("allowEmptyValue", p.AllowEmptyValue)
	m.setString("style", p.Style)
	if p.Explode != nil {
		m.set("explode", boolValue(*p.Explode))
	}
	if p.Schema != nil {
		m.set("schema", p.Schema.node())
	}
	if p.HasExample {
		m.set("example", valueNode(p.Example))
	}
	setExamples(m, p.Examples)
	return m
}

func (r RequestBody) node() *node {
	m := mapping()
	m.setString("description", r.Description)
	m.set("content", contentNode(r.Content))
	m.setBool("required", r.Required)
	return m
}

func contentNode(content []MediaType) *node {
	m := mapping()
	for _, c := range content {
		m.set(c.Type, c.node())
	}
	return m
}

func (c MediaType) node() *node {
	m := mapping()
	if c.Schema != nil {
		m.set("schema", c.Schema.node())
	}
	if c.HasExample {
		m.set("example", valueNode(c.Example))
	}
	setExamples(m, c.Examples)
	if len(c.Encoding) > 0 {
		enc := mapping()
		for _, e := range c.Encoding {
			em := mapping()
			em.setString("contentType", e.ContentType)
			setHeaders(em, e.Headers)
			em.setString("style", e.Style)
			if e.Explode != nil {
				em.set("explode", boolValue(*e.Explode))
			}
			enc.set(e.Property, em)
		}
		m.set("encoding", enc)
	}
	return m
}

// key is the object key a response is filed under: the status, or "default"
// for the one that covers everything else.
func (r Response) key() string {
	if r.Status == 0 {
		return "default"
	}
	return strconv.Itoa(r.Status)
}

func (r Response) node() *node {
	m := mapping()
	m.set("description", stringValue(r.Description))
	setHeaders(m, r.Headers)
	if len(r.Content) > 0 {
		m.set("content", contentNode(r.Content))
	}
	if len(r.Links) > 0 {
		links := mapping()
		for _, l := range r.Links {
			links.set(l.Name, l.Link.node())
		}
		m.set("links", links)
	}
	return m
}

func setHeaders(m *node, headers []NamedHeader) {
	if len(headers) == 0 {
		return
	}
	hs := mapping()
	for _, h := range headers {
		hm := mapping()
		hm.setString("description", h.Header.Description)
		hm.setBool("required", h.Header.Required)
		hm.setBool("deprecated", h.Header.Deprecated)
		if h.Header.Schema != nil {
			hm.set("schema", h.Header.Schema.node())
		}
		hs.set(h.Name, hm)
	}
	m.set("headers", hs)
}

func setExamples(m *node, examples []NamedExample) {
	if len(examples) == 0 {
		return
	}
	es := mapping()
	for _, e := range examples {
		em := mapping()
		em.setString("summary", e.Example.Summary)
		em.setString("description", e.Example.Description)
		if e.Example.HasValue {
			em.set("value", valueNode(e.Example.Value))
		}
		em.setString("externalValue", e.Example.ExternalValue)
		es.set(e.Name, em)
	}
	m.set("examples", es)
}

func (l Link) node() *node {
	m := mapping()
	m.setString("operationRef", l.OperationRef)
	m.setString("operationId", l.OperationID)
	if len(l.Parameters) > 0 {
		params := mapping()
		for _, p := range l.Parameters {
			params.set(p.Name, valueNode(p.Value))
		}
		m.set("parameters", params)
	}
	if l.HasBody {
		m.set("requestBody", valueNode(l.RequestBody))
	}
	m.setString("description", l.Description)
	if l.Server != nil {
		m.set("server", serversNode([]Server{*l.Server}).items[0])
	}
	return m
}

func (c Components) node() *node {
	m := mapping()
	if len(c.Schemas) > 0 {
		schemas := mapping()
		for _, s := range c.Schemas {
			schemas.set(s.Name, s.Schema.node())
		}
		m.set("schemas", schemas)
	}
	if len(c.Responses) > 0 {
		responses := mapping()
		for _, r := range c.Responses {
			responses.set(r.Name, r.Response.node())
		}
		m.set("responses", responses)
	}
	if len(c.Parameters) > 0 {
		params := mapping()
		for _, p := range c.Parameters {
			params.set(p.Name, p.Parameter.node())
		}
		m.set("parameters", params)
	}
	setExamples(m, c.Examples)
	if len(c.RequestBodies) > 0 {
		bodies := mapping()
		for _, b := range c.RequestBodies {
			bodies.set(b.Name, b.RequestBody.node())
		}
		m.set("requestBodies", bodies)
	}
	setHeaders(m, c.Headers)
	if len(c.SecuritySchemes) > 0 {
		schemes := mapping()
		for _, s := range c.SecuritySchemes {
			schemes.set(s.Name, s.node())
		}
		m.set("securitySchemes", schemes)
	}
	if len(c.Links) > 0 {
		links := mapping()
		for _, l := range c.Links {
			links.set(l.Name, l.Link.node())
		}
		m.set("links", links)
	}
	return m
}

func (s SecurityScheme) node() *node {
	m := mapping()
	m.set("type", stringValue(s.Type))
	m.setString("description", s.Description)
	switch s.Type {
	case SecurityAPIKey:
		m.set("name", stringValue(s.ParamName))
		m.set("in", stringValue(s.In))
	case SecurityHTTP:
		m.set("scheme", stringValue(s.Scheme))
		m.setString("bearerFormat", s.BearerFormat)
	case SecurityOAuth2:
		if s.Flows != nil {
			m.set("flows", s.Flows.node())
		}
	case SecurityOpenIDConnect:
		m.set("openIdConnectUrl", stringValue(s.OpenIDConnectURL))
	}
	return m
}

func (f OAuthFlows) node() *node {
	m := mapping()
	for _, named := range []struct {
		name string
		flow *OAuthFlow
	}{
		{"implicit", f.Implicit},
		{"password", f.Password},
		{"clientCredentials", f.ClientCredentials},
		{"authorizationCode", f.AuthorizationCode},
	} {
		if named.flow != nil {
			m.set(named.name, named.flow.node())
		}
	}
	return m
}

func (f OAuthFlow) node() *node {
	m := mapping()
	m.setString("authorizationUrl", f.AuthorizationURL)
	m.setString("tokenUrl", f.TokenURL)
	m.setString("refreshUrl", f.RefreshURL)
	scopes := mapping()
	for _, s := range f.Scopes {
		scopes.set(s.Name, stringValue(s.Description))
	}
	m.set("scopes", scopes)
	return m
}

// securityNode writes each requirement as the one-key object the
// specification asks for. The scopes are always a list, empty included: a
// scheme that grants no scopes still has to say so, and a requirement naming
// no scheme at all is the empty object that means "this may also be called
// without authenticating".
func securityNode(requirements []SecurityRequirement) *node {
	items := make([]*node, len(requirements))
	for i, r := range requirements {
		m := mapping()
		if r.Name != "" {
			scopes := make([]*node, len(r.Scopes))
			for j, s := range r.Scopes {
				scopes[j] = stringValue(s)
			}
			m.set(r.Name, sequence(scopes...))
		}
		items[i] = m
	}
	return sequence(items...)
}
