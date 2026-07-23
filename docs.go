package tork

import (
	"github.com/tork-go/web/openapi"
)

// The endpoints an application serves its description from unless it says
// otherwise. They are on by default because an API that cannot be read is
// harder to use than one that can, and because the document is built from what
// was already declared, so there is nothing to switch on to get it.
const (
	DefaultJSONPath = "/openapi.json"
	DefaultYAMLPath = "/openapi.yaml"
	DefaultDocsPath = "/docs"
)

// The Swagger UI build the documentation page loads, pinned to an exact
// version and checked against a hash.
//
// It is loaded from a CDN rather than embedded because every build of every
// browser API console is upwards of a megabyte, and carrying that in the
// module — and in the binary of every application that never opens the page —
// costs more than it is worth. Pinning the version and giving the browser an
// integrity hash is what makes that safe: a CDN that served something else
// would be refused by the browser rather than trusted. An application that
// cannot reach a CDN at all, or will not, points these at its own copies.
const (
	swaggerVersion      = "5.32.11"
	DefaultSwaggerCSS   = "https://cdn.jsdelivr.net/npm/swagger-ui-dist@" + swaggerVersion + "/swagger-ui.css"
	DefaultSwaggerJS    = "https://cdn.jsdelivr.net/npm/swagger-ui-dist@" + swaggerVersion + "/swagger-ui-bundle.js"
	defaultDocumentName = "1.0.0"
)

// OpenAPIConfig is everything about the description that the routes do not
// already say.
//
// The zero value is the default: the three endpoints at their usual paths, and
// a Swagger UI page that loads its assets from the pinned build above. Setting
// a path moves that endpoint; setting it to "-" switches that one off, which
// is different from an empty string only because an empty string is what a
// caller who set nothing has.
type OpenAPIConfig struct {
	// JSONPath, YAMLPath, and DocsPath are where the description and the
	// documentation page are served. "-" switches one off.
	JSONPath string
	YAMLPath string
	DocsPath string

	// Version is what the document reports as the API's version, which is not
	// the same thing as an API version declared with App.Version: this is the
	// number in the document, those are separate documents.
	Version string

	// Servers, and the prose fields, are what a document says about the API
	// that no route could.
	Servers        []string
	TermsOfService string
	Contact        *openapi.Contact
	License        *openapi.License
	ExternalDocs   *openapi.ExternalDocs

	// Security is what a request must satisfy by default, for an API where
	// almost everything is authenticated. A route says otherwise with Secured.
	Security []openapi.SecurityRequirement

	// The Swagger UI assets, and the integrity hashes the browser checks them
	// against. Empty means the pinned defaults; point them at your own copies
	// to serve the page without reaching a CDN.
	SwaggerCSS          string
	SwaggerJS           string
	SwaggerCSSIntegrity string
	SwaggerJSIntegrity  string

	// disabled is set by NoOpenAPI rather than by a caller, so that switching
	// the description off is something an application says out loud rather
	// than a boolean it might set by accident.
	disabled bool
}

// OpenAPI configures the description and the endpoints it is served from.
//
//	app := tork.New(
//	    tork.Title("Example API"),
//	    tork.OpenAPI(tork.OpenAPIConfig{
//	        DocsPath: "/reference",
//	        Version:  "2.1.0",
//	    }),
//	)
//
// Anything left unset keeps its default, so moving one endpoint does not mean
// restating the others.
func OpenAPI(config OpenAPIConfig) Option {
	return newOption("OpenAPI", scopeApp, func(m *meta) error {
		m.docs = config
		return nil
	})
}

// NoOpenAPI switches off the description and every endpoint that serves it,
// for an application that publishes no documentation.
func NoOpenAPI() Option {
	return newOption("NoOpenAPI", scopeApp, func(m *meta) error {
		m.docs = OpenAPIConfig{disabled: true}
		return nil
	})
}

// DeclareSecurityScheme adds a way for a request to prove who it is, which
// operations then refer to by name with Secured.
//
//	tork.DeclareSecurityScheme(openapi.SecurityScheme{
//	    Name: "bearer", Type: openapi.SecurityHTTP, Scheme: "bearer", BearerFormat: "JWT",
//	})
//
// The scheme is declared rather than inferred from a dependency: what a
// dependency reads from a header is visible, but whether that header is a
// bearer token, an API key, or the first leg of an OAuth flow is not, and a
// document that guessed would sometimes be wrong.
func DeclareSecurityScheme(scheme openapi.SecurityScheme) Option {
	return newOption("DeclareSecurityScheme", scopeApp, func(m *meta) error {
		m.schemes = append(m.schemes, scheme)
		return nil
	})
}

// Secured says a request must satisfy a declared security scheme, optionally
// within named scopes. It is inherited, so a router that carries it secures
// every route underneath.
//
//	items := tork.NewRouter(tork.Prefix("/items"), tork.Secured("bearer", "items:read"))
func Secured(scheme string, scopes ...string) Option {
	return newOption("Secured", scopeApp|scopeRouter|scopeRoute, func(m *meta) error {
		m.security = append(m.security, openapi.SecurityRequirement{Name: scheme, Scopes: scopes})
		return nil
	})
}

// resolved fills in whatever the application did not say.
func (c OpenAPIConfig) resolved() OpenAPIConfig {
	if c.JSONPath == "" {
		c.JSONPath = DefaultJSONPath
	}
	if c.YAMLPath == "" {
		c.YAMLPath = DefaultYAMLPath
	}
	if c.DocsPath == "" {
		c.DocsPath = DefaultDocsPath
	}
	if c.Version == "" {
		c.Version = defaultDocumentName
	}
	if c.SwaggerCSS == "" {
		c.SwaggerCSS = DefaultSwaggerCSS
	}
	if c.SwaggerJS == "" {
		c.SwaggerJS = DefaultSwaggerJS
	}
	return c
}

// serverList turns the configured server URLs into what the document carries.
func (c OpenAPIConfig) serverList() []openapi.Server {
	if len(c.Servers) == 0 {
		return nil
	}
	servers := make([]openapi.Server, len(c.Servers))
	for i, url := range c.Servers {
		servers[i] = openapi.Server{URL: url}
	}
	return servers
}
