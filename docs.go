package tork

import (
	"bytes"
	"html/template"
	"path"
	"strings"

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
	swaggerVersion    = "5.32.11"
	DefaultSwaggerCSS = "https://cdn.jsdelivr.net/npm/swagger-ui-dist@" + swaggerVersion + "/swagger-ui.css"
	DefaultSwaggerJS  = "https://cdn.jsdelivr.net/npm/swagger-ui-dist@" + swaggerVersion + "/swagger-ui-bundle.js"

	// The hashes of exactly those two files. A browser refuses a file whose
	// hash does not match, so a CDN that served something else — compromised,
	// or simply wrong — cannot run code in the documentation page.
	defaultSwaggerCSSIntegrity = "sha384-9Q2fpS+xeS4ffJy6CagnwoUl+4ldAYhOs9pgZuEKxypVModhmZFzeMlvVsAjf7uT"
	defaultSwaggerJSIntegrity  = "sha384-vfl/klfTFrIz5urj0HnhcXLAbzPdRHezizfy+XgFB6GqcKkhlk0lS3bIbyB39NLA"

	defaultDocumentName = "1.0.0"

	// disabledPath is what a path is set to in order to switch one endpoint
	// off, since an empty string is what a caller who set nothing has.
	disabledPath = "-"
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
	// The hash goes with the URL. An application pointing at its own copy gets
	// no integrity attribute unless it supplies the hash for that copy, since
	// checking someone else's file against this one's hash would refuse it.
	if c.SwaggerCSS == "" {
		c.SwaggerCSS, c.SwaggerCSSIntegrity = DefaultSwaggerCSS, defaultSwaggerCSSIntegrity
	}
	if c.SwaggerJS == "" {
		c.SwaggerJS, c.SwaggerJSIntegrity = DefaultSwaggerJS, defaultSwaggerJSIntegrity
	}
	return c
}

// docPage is the Swagger UI shell: a page that loads the assets and points
// them at this application's own description.
var docPage = template.Must(template.New("docs").Parse(`<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<link rel="stylesheet" href="{{.CSS}}"{{if .CSSIntegrity}} integrity="{{.CSSIntegrity}}" crossorigin="anonymous"{{end}}>
</head>
<body>
<div id="swagger-ui"></div>
<script src="{{.JS}}"{{if .JSIntegrity}} integrity="{{.JSIntegrity}}" crossorigin="anonymous"{{end}}></script>
<script>
window.onload = function () {
  window.ui = SwaggerUIBundle({
    url: {{.Spec}},
    dom_id: "#swagger-ui",
    deepLinking: true,
    presets: [SwaggerUIBundle.presets.apis],
    layout: "BaseLayout"
  });
};
</script>
</body>
</html>
`))

// renderDocPage writes the documentation page for one description.
func (c OpenAPIConfig) renderDocPage(title, spec string) []byte {
	var page bytes.Buffer
	// The template is a constant and every value in it is escaped by
	// html/template, so there is nothing here that can fail.
	_ = docPage.Execute(&page, struct {
		Title, CSS, JS, CSSIntegrity, JSIntegrity, Spec string
	}{
		Title:        title,
		CSS:          c.SwaggerCSS,
		JS:           c.SwaggerJS,
		CSSIntegrity: c.SwaggerCSSIntegrity,
		JSIntegrity:  c.SwaggerJSIntegrity,
		Spec:         spec,
	})
	return page.Bytes()
}

// docEndpoint is one thing the server answers with a fixed body: a
// description, or the page that reads one.
//
// The body is rendered when the application builds, not per request, because
// the document cannot change afterwards — every route it describes was
// declared before the server existed.
type docEndpoint struct {
	path        string
	contentType string
	body        []byte
}

// documentEndpoints renders every description this application publishes, and
// the page that reads each one.
//
// The unversioned endpoints describe the default version; each declared
// version also gets its own, at the same paths with the version folded in, so
// /openapi.json has /openapi/v1.json beside it and /docs has /docs/v1.
func (a *App) documentEndpoints(routes []*Route) []docEndpoint {
	cfg := a.docs.resolved()
	if a.docs.disabled {
		return nil
	}

	endpoints := a.describeVersion(cfg, routes, defaultVersion(routes), cfg.JSONPath, cfg.YAMLPath, cfg.DocsPath)
	for _, version := range versionsOf(routes) {
		endpoints = append(endpoints, a.describeVersion(cfg, routes, version,
			versionedPath(cfg.JSONPath, version),
			versionedPath(cfg.YAMLPath, version),
			versionedPath(cfg.DocsPath, version),
		)...)
	}
	return endpoints
}

// describeVersion renders one version's description in both formats, and the
// page that reads it. An endpoint whose path was switched off is left out.
func (a *App) describeVersion(cfg OpenAPIConfig, routes []*Route, version, jsonPath, yamlPath, docsPath string) []docEndpoint {
	doc := a.document(routes, version)

	var endpoints []docEndpoint
	if jsonPath != disabledPath {
		endpoints = append(endpoints, docEndpoint{jsonPath, "application/json", doc.JSON()})
	}
	if yamlPath != disabledPath {
		endpoints = append(endpoints, docEndpoint{yamlPath, "application/yaml", doc.YAML()})
	}
	if docsPath != disabledPath {
		// The page reads whichever description is actually being served. JSON
		// is what Swagger UI wants; if only YAML is published it reads that.
		spec := jsonPath
		if spec == disabledPath {
			spec = yamlPath
		}
		if spec != disabledPath {
			endpoints = append(endpoints, docEndpoint{docsPath, "text/html; charset=utf-8", cfg.renderDocPage(doc.Info.Title, spec)})
		}
	}
	return endpoints
}

// versionedPath folds a version into a path, keeping whatever extension it
// had: /openapi.json becomes /openapi/v1.json, and /docs becomes /docs/v1.
func versionedPath(p, version string) string {
	if p == disabledPath {
		return p
	}
	ext := path.Ext(p)
	return strings.TrimSuffix(p, ext) + "/" + version + ext
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
