package tork_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/tork-go/web"
	"github.com/tork-go/web/openapi"
)

// An application publishes its description and a page to read it without
// being asked to, the way it answers OPTIONS without being asked to.
func TestDefaultEndpointsAreServed(t *testing.T) {
	app := newApp(tork.Title("Example API"))
	app.GET("/items", hello)

	spec := do(t, app, "GET", "/openapi.json", nil)
	if spec.Code != http.StatusOK {
		t.Fatalf("/openapi.json = %d", spec.Code)
	}
	if got := spec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("content type = %q", got)
	}
	var decoded map[string]any
	if err := json.Unmarshal(spec.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("served description does not parse: %v", err)
	}
	if paths, _ := decoded["paths"].(map[string]any); len(paths) != 1 {
		t.Errorf("paths = %v", paths)
	}

	yaml := do(t, app, "GET", "/openapi.yaml", nil)
	if yaml.Code != http.StatusOK {
		t.Fatalf("/openapi.yaml = %d", yaml.Code)
	}
	if got := yaml.Header().Get("Content-Type"); got != "application/yaml" {
		t.Errorf("content type = %q", got)
	}
	if !strings.Contains(yaml.Body.String(), "title: Example API") {
		t.Errorf("body = %s", yaml.Body)
	}

	page := do(t, app, "GET", "/docs", nil)
	if page.Code != http.StatusOK {
		t.Fatalf("/docs = %d", page.Code)
	}
	if got := page.Header().Get("Content-Type"); !strings.HasPrefix(got, "text/html") {
		t.Errorf("content type = %q", got)
	}
}

// The page loads a pinned build and tells the browser what it should hash to,
// so a CDN serving something else cannot run code in it.
func TestDocsPageIsPinnedAndChecked(t *testing.T) {
	app := newApp(tork.Title("Example API"))
	app.GET("/items", hello)

	body := do(t, app, "GET", "/docs", nil).Body.String()

	for _, want := range []string{
		tork.DefaultSwaggerCSS,
		tork.DefaultSwaggerJS,
		`integrity="sha384-`,
		`crossorigin="anonymous"`,
		`/openapi.json`,
		"Example API",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("the page is missing %q:\n%s", want, body)
		}
	}
	// The version is pinned rather than floating, so the hash keeps meaning
	// something.
	if strings.Contains(body, "swagger-ui-dist@latest") {
		t.Error("the assets must be pinned to a version")
	}
}

// An application chooses where its description lives.
func TestEndpointPathsAreConfigurable(t *testing.T) {
	app := newApp(tork.OpenAPI(tork.OpenAPIConfig{
		JSONPath: "/spec.json",
		DocsPath: "/reference",
	}))
	app.GET("/items", hello)

	if rec := do(t, app, "GET", "/spec.json", nil); rec.Code != http.StatusOK {
		t.Errorf("/spec.json = %d", rec.Code)
	}
	if rec := do(t, app, "GET", "/reference", nil); rec.Code != http.StatusOK {
		t.Errorf("/reference = %d", rec.Code)
	}
	// The page points at wherever the description actually is.
	if body := do(t, app, "GET", "/reference", nil).Body.String(); !strings.Contains(body, "/spec.json") {
		t.Errorf("the page should read the configured description:\n%s", body)
	}
	// What was moved is no longer where it was.
	if rec := do(t, app, "GET", "/openapi.json", nil); rec.Code != http.StatusNotFound {
		t.Errorf("/openapi.json = %d, want 404 once it was moved", rec.Code)
	}
	// The one that was not configured keeps its default.
	if rec := do(t, app, "GET", "/openapi.yaml", nil); rec.Code != http.StatusOK {
		t.Errorf("/openapi.yaml = %d, want the default to be kept", rec.Code)
	}
}

// One endpoint can be switched off without switching off the others.
func TestASingleEndpointCanBeSwitchedOff(t *testing.T) {
	app := newApp(tork.OpenAPI(tork.OpenAPIConfig{YAMLPath: "-"}))
	app.GET("/items", hello)

	if rec := do(t, app, "GET", "/openapi.yaml", nil); rec.Code != http.StatusNotFound {
		t.Errorf("/openapi.yaml = %d, want it switched off", rec.Code)
	}
	if rec := do(t, app, "GET", "/openapi.json", nil); rec.Code != http.StatusOK {
		t.Errorf("/openapi.json = %d", rec.Code)
	}
}

// A page with no description to read is not served at all.
func TestDocsPageFollowsTheDescription(t *testing.T) {
	// Only YAML is published, so the page reads that.
	yamlOnly := newApp(tork.OpenAPI(tork.OpenAPIConfig{JSONPath: "-"}))
	yamlOnly.GET("/items", hello)
	if body := do(t, yamlOnly, "GET", "/docs", nil).Body.String(); !strings.Contains(body, "/openapi.yaml") {
		t.Errorf("the page should fall back to the YAML description:\n%s", body)
	}

	// Neither is published, so there is nothing for a page to read.
	neither := newApp(tork.OpenAPI(tork.OpenAPIConfig{JSONPath: "-", YAMLPath: "-"}))
	neither.GET("/items", hello)
	if rec := do(t, neither, "GET", "/docs", nil); rec.Code != http.StatusNotFound {
		t.Errorf("/docs = %d, want nothing to read", rec.Code)
	}
}

// An application that publishes nothing serves none of it.
func TestNoOpenAPIServesNothing(t *testing.T) {
	app := newApp(tork.NoOpenAPI())
	app.GET("/items", hello)

	for _, path := range []string{"/openapi.json", "/openapi.yaml", "/docs"} {
		if rec := do(t, app, "GET", path, nil); rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d, want it switched off", path, rec.Code)
		}
	}
	// The application itself still works.
	if rec := do(t, app, "GET", "/items", nil); rec.Code != http.StatusOK {
		t.Errorf("/items = %d", rec.Code)
	}
}

// Each declared version is published beside the default, at the same paths
// with the version folded in.
func TestVersionedEndpoints(t *testing.T) {
	app := newApp()
	v1 := app.Version("v1", tork.Prefix("/api/v1"))
	v1.GET("/items", hello, tork.OperationID("items.list"))
	v2 := app.Version("v2", tork.Prefix("/api/v2"))
	v2.GET("/items", farewell, tork.OperationID("items.list"))

	for _, path := range []string{"/openapi/v1.json", "/openapi/v2.json", "/openapi/v1.yaml", "/docs/v1", "/docs/v2"} {
		if rec := do(t, app, "GET", path, nil); rec.Code != http.StatusOK {
			t.Errorf("%s = %d", path, rec.Code)
		}
	}

	// Each is its own description, carrying only its own routes.
	var first map[string]any
	if err := json.Unmarshal(do(t, app, "GET", "/openapi/v1.json", nil).Body.Bytes(), &first); err != nil {
		t.Fatalf("v1 does not parse: %v", err)
	}
	paths, _ := first["paths"].(map[string]any)
	if _, ok := paths["/api/v1/items"]; !ok || len(paths) != 1 {
		t.Errorf("v1 paths = %v", paths)
	}

	// Every route is versioned, so the default endpoints describe the first
	// version rather than nothing.
	var byDefault map[string]any
	if err := json.Unmarshal(do(t, app, "GET", "/openapi.json", nil).Body.Bytes(), &byDefault); err != nil {
		t.Fatalf("default does not parse: %v", err)
	}
	defaultPaths, _ := byDefault["paths"].(map[string]any)
	if _, ok := defaultPaths["/api/v1/items"]; !ok {
		t.Errorf("default paths = %v", defaultPaths)
	}

	// The versioned page reads the versioned description.
	if body := do(t, app, "GET", "/docs/v2", nil).Body.String(); !strings.Contains(body, "/openapi/v2.json") {
		t.Errorf("the v2 page should read the v2 description:\n%s", body)
	}
}

// Switching an endpoint off switches off its versioned counterparts too;
// there is no version of an endpoint that does not exist.
func TestSwitchingOffAppliesToEveryVersion(t *testing.T) {
	app := newApp(tork.OpenAPI(tork.OpenAPIConfig{YAMLPath: "-"}))
	v1 := app.Version("v1", tork.Prefix("/api/v1"))
	v1.GET("/items", hello)

	if rec := do(t, app, "GET", "/openapi/v1.yaml", nil); rec.Code != http.StatusNotFound {
		t.Errorf("/openapi/v1.yaml = %d, want it switched off with the rest", rec.Code)
	}
	if rec := do(t, app, "GET", "/openapi/v1.json", nil); rec.Code != http.StatusOK {
		t.Errorf("/openapi/v1.json = %d", rec.Code)
	}
}

// A route already at one of these paths wins, and the build says so rather
// than one of them quietly shadowing the other.
func TestARouteAtTheDescriptionPathIsABuildError(t *testing.T) {
	app := newApp()
	app.GET("/openapi.json", hello)

	msg := buildError(t, app)
	if !strings.Contains(msg, "/openapi.json") {
		t.Errorf("error = %q", msg)
	}
	if !strings.Contains(msg, "tork.OpenAPI") || !strings.Contains(msg, "tork.NoOpenAPI") {
		t.Errorf("the error should say how to fix it: %q", msg)
	}
}

// Moving the description out of the way is the fix the error suggests, and it
// works.
func TestMovingTheDescriptionResolvesTheCollision(t *testing.T) {
	app := newApp(tork.OpenAPI(tork.OpenAPIConfig{JSONPath: "/spec.json"}))
	app.GET("/openapi.json", hello)

	if rec := do(t, app, "GET", "/openapi.json", nil); rec.Code != http.StatusOK {
		t.Errorf("the application's own route = %d", rec.Code)
	}
	if rec := do(t, app, "GET", "/spec.json", nil); rec.Code != http.StatusOK {
		t.Errorf("/spec.json = %d", rec.Code)
	}
}

// An application pointing at its own copy of the assets gets no integrity
// attribute, since checking someone else's file against this one's hash would
// refuse it.
func TestCustomAssetsDropTheDefaultHash(t *testing.T) {
	app := newApp(tork.OpenAPI(tork.OpenAPIConfig{
		SwaggerCSS: "/static/swagger.css",
		SwaggerJS:  "/static/swagger.js",
	}))
	app.GET("/items", hello)

	body := do(t, app, "GET", "/docs", nil).Body.String()
	if !strings.Contains(body, "/static/swagger.js") {
		t.Errorf("the page should load the configured assets:\n%s", body)
	}
	if strings.Contains(body, "integrity=") {
		t.Errorf("a custom asset has no hash of ours to check against:\n%s", body)
	}
}

// An application supplying its own assets and their hashes gets both.
func TestCustomAssetsKeepASuppliedHash(t *testing.T) {
	app := newApp(tork.OpenAPI(tork.OpenAPIConfig{
		SwaggerCSS:          "/static/swagger.css",
		SwaggerJS:           "/static/swagger.js",
		SwaggerCSSIntegrity: "sha384-css",
		SwaggerJSIntegrity:  "sha384-js",
	}))
	app.GET("/items", hello)

	body := do(t, app, "GET", "/docs", nil).Body.String()
	if !strings.Contains(body, `integrity="sha384-js"`) {
		t.Errorf("a supplied hash should be used:\n%s", body)
	}
}

// The description is rendered when the application builds, so serving it
// twice serves the same bytes and costs nothing the second time.
func TestDescriptionIsRenderedOnce(t *testing.T) {
	app := newApp(tork.Title("Example"))
	app.GET("/items", hello)

	first := do(t, app, "GET", "/openapi.json", nil).Body.String()
	second := do(t, app, "GET", "/openapi.json", nil).Body.String()
	if first != second {
		t.Error("two requests for one description differ")
	}
}

// A description describes the application it is served from, dependencies and
// all — the round trip this phase exists for.
type EndpointAuthInput struct {
	Token string `header:"X-Auth"`
}

func TestServedDescriptionDescribesTheServedApplication(t *testing.T) {
	guard := func(_ context.Context, in EndpointAuthInput) error { return nil }

	items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(guard), tork.Secured("bearer"))
	items.POST("/", func(_ context.Context, body DocBody) (tork.Response[greeting], error) {
		return tork.Respond(201, greeting{}), nil
	}, tork.Summary("Create an item"), tork.Tags("items"))

	app := newApp(
		tork.Title("Example API"),
		tork.DeclareSecurityScheme(openapiBearer()),
	)
	app.Include(items)

	var doc map[string]any
	if err := json.Unmarshal(do(t, app, "GET", "/openapi.json", nil).Body.Bytes(), &doc); err != nil {
		t.Fatalf("served description does not parse: %v", err)
	}

	paths, _ := doc["paths"].(map[string]any)
	item, _ := paths["/items"].(map[string]any)
	post, _ := item["post"].(map[string]any)
	if post == nil {
		t.Fatalf("paths = %v", paths)
	}
	if post["summary"] != "Create an item" {
		t.Errorf("operation = %v", post)
	}
	if post["requestBody"] == nil {
		t.Error("the body the handler decodes should be described")
	}
	if params, _ := post["parameters"].([]any); len(params) != 1 {
		t.Errorf("the header the dependency reads should be described: %v", params)
	}
	if security, _ := post["security"].([]any); len(security) != 1 {
		t.Errorf("security = %v", post["security"])
	}
	components, _ := doc["components"].(map[string]any)
	if schemes, _ := components["securitySchemes"].(map[string]any); len(schemes) != 1 {
		t.Errorf("securitySchemes = %v", components["securitySchemes"])
	}
}

// openapiBearer is the scheme the round-trip test declares.
func openapiBearer() openapi.SecurityScheme {
	return openapi.SecurityScheme{
		Name: "bearer", Type: openapi.SecurityHTTP, Scheme: "bearer", BearerFormat: "JWT",
	}
}
