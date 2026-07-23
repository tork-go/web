package tork_test

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"testing"

	"github.com/tork-go/web"
	"github.com/tork-go/web/openapi"
)

// decodeDoc builds the document and decodes it, which is how every test here
// asserts: against the document a client would actually be served.
func decodeDoc(t *testing.T, doc *openapi.Document) map[string]any {
	t.Helper()
	var decoded map[string]any
	if err := json.Unmarshal(doc.JSON(), &decoded); err != nil {
		t.Fatalf("document does not parse: %v", err)
	}
	return decoded
}

func documentOf(t *testing.T, app *tork.App) map[string]any {
	t.Helper()
	doc, err := app.OpenAPI()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	return decodeDoc(t, doc)
}

// operationAt digs out one operation, which is where most of these assertions
// live.
func operationAt(t *testing.T, doc map[string]any, path, method string) map[string]any {
	t.Helper()
	paths, _ := doc["paths"].(map[string]any)
	item, ok := paths[path].(map[string]any)
	if !ok {
		t.Fatalf("no path %q in %v", path, paths)
	}
	op, ok := item[method].(map[string]any)
	if !ok {
		t.Fatalf("no %s on %q in %v", method, path, item)
	}
	return op
}

// An application says everything about itself that no route could, and the
// document carries all of it.
func TestDocumentInfoAndServers(t *testing.T) {
	app := newApp(
		tork.Title("Example API"),
		tork.Description("Prose about the API."),
		tork.OpenAPI(tork.OpenAPIConfig{
			Version:        "2.1.0",
			Servers:        []string{"https://api.example.com", "https://staging.example.com"},
			TermsOfService: "https://example.com/terms",
			Contact:        &openapi.Contact{Name: "Support", Email: "help@example.com"},
			License:        &openapi.License{Name: "Apache-2.0", Identifier: "Apache-2.0"},
			ExternalDocs:   &openapi.ExternalDocs{Description: "The manual", URL: "https://example.com/manual"},
		}),
	)
	app.GET("/", hello)

	doc := documentOf(t, app)
	if doc["openapi"] != openapi.Version {
		t.Errorf("openapi = %v", doc["openapi"])
	}
	info, _ := doc["info"].(map[string]any)
	if info["title"] != "Example API" || info["version"] != "2.1.0" {
		t.Errorf("info = %v", info)
	}
	if info["description"] != "Prose about the API." || info["termsOfService"] != "https://example.com/terms" {
		t.Errorf("info = %v", info)
	}
	if contact, _ := info["contact"].(map[string]any); contact["email"] != "help@example.com" {
		t.Errorf("contact = %v", info["contact"])
	}
	if licence, _ := info["license"].(map[string]any); licence["name"] != "Apache-2.0" {
		t.Errorf("license = %v", info["license"])
	}
	if servers, _ := doc["servers"].([]any); len(servers) != 2 {
		t.Errorf("servers = %v", doc["servers"])
	}
	if external, _ := doc["externalDocs"].(map[string]any); external["url"] != "https://example.com/manual" {
		t.Errorf("externalDocs = %v", doc["externalDocs"])
	}
}

// An application that says nothing about itself still gets a usable document.
func TestDocumentDefaults(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	info, _ := documentOf(t, app)["info"].(map[string]any)
	if info["title"] != "API" || info["version"] != "1.0.0" {
		t.Errorf("info = %v, want the defaults", info)
	}
}

// DocParams reads one of each parameter source, so the document has to place
// each of them correctly.
type DocParams struct {
	ItemID string   `path:"item_id"`
	Limit  int      `query:"limit"`
	Token  string   `header:"X-Token"`
	Sess   string   `cookie:"session"`
	Tags   []string `query:"tags,csv"`
}

func TestDocumentParameters(t *testing.T) {
	app := newApp()
	app.GET("/items/{item_id}", func(_ context.Context, in DocParams) (greeting, error) {
		return greeting{}, nil
	}, tork.Summary("Read an item"), tork.Description("Reads one."),
		tork.Tags("items"), tork.Deprecated(), tork.OperationID("items.read"))

	doc := documentOf(t, app)
	op := operationAt(t, doc, "/items/{item_id}", "get")

	if op["summary"] != "Read an item" || op["description"] != "Reads one." {
		t.Errorf("operation = %v", op)
	}
	if op["operationId"] != "items.read" || op["deprecated"] != true {
		t.Errorf("operation = %v", op)
	}
	if tags, _ := op["tags"].([]any); len(tags) != 1 || tags[0] != "items" {
		t.Errorf("tags = %v", op["tags"])
	}
	// The tag is also listed on the document, so a reader sees the group.
	if tags, _ := doc["tags"].([]any); len(tags) != 1 {
		t.Errorf("document tags = %v", doc["tags"])
	}

	places := map[string]string{}
	required := map[string]bool{}
	styles := map[string]any{}
	for _, p := range op["parameters"].([]any) {
		param, _ := p.(map[string]any)
		name, _ := param["name"].(string)
		places[name], _ = param["in"].(string)
		required[name], _ = param["required"].(bool)
		styles[name] = param["style"]
	}

	for name, want := range map[string]string{
		"item_id": "path", "limit": "query", "X-Token": "header", "session": "cookie", "tags": "query",
	} {
		if places[name] != want {
			t.Errorf("%s is in %q, want %q", name, places[name], want)
		}
	}
	// A path parameter is part of the path, so it is required whatever was
	// declared.
	if !required["item_id"] {
		t.Error("a path parameter must be required")
	}
	if required["limit"] {
		t.Error("a query parameter nobody required must not be")
	}
	// A comma-separated list is a different serialization from a repeated one.
	if styles["tags"] != "form" {
		t.Errorf("a csv list must say how it is serialized: %v", styles["tags"])
	}
}

// DocBody is a declared body, so the document carries both the shape the Go
// type gives it and the rules the declaration added.
type DocBody struct {
	tork.JSONBody
	Name  string `json:"name"`
	Price int    `json:"price"`
}

var docBodyRules = tork.DefineBody(func(b *tork.BodyRules, in *DocBody) {
	b.String(&in.Name).Required().MinLen(2)
	b.Int(&in.Price).Min(1)
})

func TestDocumentJSONBody(t *testing.T) {
	_ = docBodyRules
	app := newApp()
	app.POST("/items", func(_ context.Context, body DocBody) (greeting, error) {
		return greeting{}, nil
	})

	doc := documentOf(t, app)
	op := operationAt(t, doc, "/items", "post")

	body, _ := op["requestBody"].(map[string]any)
	if body["required"] != true {
		t.Errorf("requestBody = %v", body)
	}
	content, _ := body["content"].(map[string]any)
	media, ok := content["application/json"].(map[string]any)
	if !ok {
		t.Fatalf("content = %v", content)
	}
	schema, _ := media["schema"].(map[string]any)
	if ref, _ := schema["$ref"].(string); ref != "#/components/schemas/DocBody" {
		t.Fatalf("schema = %v, want a reference", schema)
	}

	// The component carries the rules the declaration added.
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	described, _ := schemas["DocBody"].(map[string]any)
	props, _ := described["properties"].(map[string]any)

	name, _ := props["name"].(map[string]any)
	if !sameJSON(name["minLength"], 2) {
		t.Errorf("name = %v, want the declared length rule", name)
	}
	price, _ := props["price"].(map[string]any)
	if !sameJSON(price["minimum"], 1) {
		t.Errorf("price = %v, want the declared bound", price)
	}
	var required []any
	required, _ = described["required"].([]any)
	if !containsAny(required, "name") {
		t.Errorf("required = %v, want the declared requirement", required)
	}
}

// A body with no declaration of its own is still described from its type.
type PlainBody struct {
	tork.JSONBody
	Note string `json:"note"`
}

func TestDocumentUndeclaredBody(t *testing.T) {
	app := newApp()
	app.POST("/notes", func(_ context.Context, body PlainBody) (greeting, error) {
		return greeting{}, nil
	})

	op := operationAt(t, documentOf(t, app), "/notes", "post")
	if op["requestBody"] == nil {
		t.Error("a body with no rules is still a body")
	}
}

// A form is a body rather than a set of parameters, and an upload among the
// fields makes it multipart.
type DocForm struct {
	Note   string                  `form:"note"`
	Avatar *multipart.FileHeader   `form:"avatar"`
	Extras []*multipart.FileHeader `form:"extras"`
}

func TestDocumentMultipartForm(t *testing.T) {
	app := newApp()
	app.POST("/upload", func(_ context.Context, in DocForm) (greeting, error) {
		return greeting{}, nil
	})

	op := operationAt(t, documentOf(t, app), "/upload", "post")
	if _, hasParams := op["parameters"]; hasParams {
		t.Error("a form field is part of the body, not a parameter")
	}

	body, _ := op["requestBody"].(map[string]any)
	content, _ := body["content"].(map[string]any)
	media, ok := content["multipart/form-data"].(map[string]any)
	if !ok {
		t.Fatalf("an upload makes the body multipart: %v", content)
	}
	schema, _ := media["schema"].(map[string]any)
	props, _ := schema["properties"].(map[string]any)
	avatar, _ := props["avatar"].(map[string]any)
	if avatar["format"] != "binary" {
		t.Errorf("avatar = %v, want a binary string", avatar)
	}
	extras, _ := props["extras"].(map[string]any)
	if extras["type"] != "array" {
		t.Errorf("extras = %v, want an array", extras)
	}
	if _, hasEncoding := media["encoding"]; !hasEncoding {
		t.Errorf("an upload declares its encoding: %v", media)
	}
}

// A form with no upload is urlencoded rather than multipart.
type DocPlainForm struct {
	Note string `form:"note"`
}

func TestDocumentURLEncodedForm(t *testing.T) {
	app := newApp()
	app.POST("/notes", func(_ context.Context, in DocPlainForm) (greeting, error) {
		return greeting{}, nil
	})

	op := operationAt(t, documentOf(t, app), "/notes", "post")
	body, _ := op["requestBody"].(map[string]any)
	content, _ := body["content"].(map[string]any)
	if _, ok := content["application/x-www-form-urlencoded"]; !ok {
		t.Errorf("content = %v", content)
	}
}

// What a handler returns is what the document says it returns.
func TestDocumentResponses(t *testing.T) {
	app := newApp()
	app.GET("/plain", hello)
	app.DELETE("/gone", func(context.Context) error { return nil })
	app.POST("/made", func(context.Context) (tork.Response[greeting], error) {
		return tork.Respond(201, greeting{}), nil
	})
	app.GET("/file", func(context.Context) (tork.FileResponse, error) {
		return tork.File("x.txt", bytes.NewReader(nil)), nil
	})

	doc := documentOf(t, app)

	// A plain T is 200 with its schema.
	plain := operationAt(t, doc, "/plain", "get")
	responses, _ := plain["responses"].(map[string]any)
	ok200, _ := responses["200"].(map[string]any)
	if ok200["description"] == "" {
		t.Errorf("200 = %v", ok200)
	}
	if _, hasContent := ok200["content"]; !hasContent {
		t.Errorf("a plain result has a body: %v", ok200)
	}

	// A handler returning only an error answers 204 with nothing.
	gone := operationAt(t, doc, "/gone", "delete")
	goneResponses, _ := gone["responses"].(map[string]any)
	if _, has204 := goneResponses["204"]; !has204 {
		t.Errorf("responses = %v, want a 204", goneResponses)
	}

	// A response type's own status is the one described.
	made := operationAt(t, doc, "/made", "post")
	madeResponses, _ := made["responses"].(map[string]any)
	if _, has200 := madeResponses["200"]; !has200 {
		t.Errorf("responses = %v", madeResponses)
	}

	// A file has no Go type for its body, so it is described without a schema.
	file := operationAt(t, doc, "/file", "get")
	fileResponses, _ := file["responses"].(map[string]any)
	if len(fileResponses) == 0 {
		t.Errorf("a file response is still a response: %v", file)
	}
}

// Responds and Throws are how an operation describes what its own signature
// cannot.
type OutOfStock struct {
	SKU string `json:"sku"`
}

type Unavailable struct {
	Until string `json:"until"`
}

func TestDocumentDeclaredResponsesAndThrows(t *testing.T) {
	app := newApp()
	app.GET("/items", hello,
		tork.Responds[tork.Error](404, "No such item."),
		tork.Responds[tork.Error](599, ""),
		tork.Throws[OutOfStock](),
		tork.Throws[Unavailable](),
	)

	op := operationAt(t, documentOf(t, app), "/items", "get")
	responses, _ := op["responses"].(map[string]any)

	notFound, _ := responses["404"].(map[string]any)
	if notFound["description"] != "No such item." {
		t.Errorf("404 = %v", notFound)
	}
	if _, hasContent := notFound["content"]; !hasContent {
		t.Errorf("a declared response carries its shape: %v", notFound)
	}

	// A status the standard library has never heard of still gets a
	// description, since the specification requires one.
	odd, _ := responses["599"].(map[string]any)
	if odd["description"] == "" || odd["description"] == nil {
		t.Errorf("599 = %v, want a description", odd)
	}

	// Thrown shapes have no status of their own, so they are the default
	// response; two of them are a choice between them.
	fallback, _ := responses["default"].(map[string]any)
	if fallback == nil {
		t.Fatalf("responses = %v, want a default", responses)
	}
	content, _ := fallback["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	if _, hasOneOf := schema["oneOf"]; !hasOneOf {
		t.Errorf("two thrown shapes are a choice: %v", schema)
	}
}

// One thrown shape needs no choice between alternatives.
func TestDocumentSingleThrow(t *testing.T) {
	app := newApp()
	app.GET("/items", hello, tork.Throws[OutOfStock]())

	op := operationAt(t, documentOf(t, app), "/items", "get")
	responses, _ := op["responses"].(map[string]any)
	fallback, _ := responses["default"].(map[string]any)
	content, _ := fallback["content"].(map[string]any)
	media, _ := content["application/json"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)
	if _, hasOneOf := schema["oneOf"]; hasOneOf {
		t.Errorf("one shape is not a choice: %v", schema)
	}
}

// Authentication is declared, and the operations that require it say so.
func TestDocumentSecurity(t *testing.T) {
	app := newApp(
		tork.DeclareSecurityScheme(openapi.SecurityScheme{
			Name: "bearer", Type: openapi.SecurityHTTP, Scheme: "bearer", BearerFormat: "JWT",
		}),
		tork.DeclareSecurityScheme(openapi.SecurityScheme{
			Name: "apiKey", Type: openapi.SecurityAPIKey, In: openapi.InHeader, ParamName: "X-API-Key",
		}),
	)
	secured := tork.NewRouter(tork.Prefix("/admin"), tork.Secured("bearer", "admin:read"))
	secured.GET("/", hello)
	app.Include(secured)
	app.GET("/open", farewell)

	doc := documentOf(t, app)
	components, _ := doc["components"].(map[string]any)
	schemes, _ := components["securitySchemes"].(map[string]any)
	if len(schemes) != 2 {
		t.Fatalf("securitySchemes = %v", schemes)
	}
	bearer, _ := schemes["bearer"].(map[string]any)
	if bearer["type"] != "http" || bearer["bearerFormat"] != "JWT" {
		t.Errorf("bearer = %v", bearer)
	}
	key, _ := schemes["apiKey"].(map[string]any)
	if key["in"] != "header" || key["name"] != "X-API-Key" {
		t.Errorf("apiKey = %v", key)
	}

	// A router that is secured secures everything under it.
	admin := operationAt(t, doc, "/admin", "get")
	security, _ := admin["security"].([]any)
	if len(security) != 1 {
		t.Fatalf("security = %v", admin["security"])
	}
	requirement, _ := security[0].(map[string]any)
	scopes, _ := requirement["bearer"].([]any)
	if len(scopes) != 1 || scopes[0] != "admin:read" {
		t.Errorf("scopes = %v", requirement)
	}

	// A route outside it is not secured.
	open := operationAt(t, doc, "/open", "get")
	if _, hasSecurity := open["security"]; hasSecurity {
		t.Errorf("an unsecured route should say nothing: %v", open)
	}
}

// A document-wide default is what an API where almost everything is
// authenticated says once.
func TestDocumentWideSecurity(t *testing.T) {
	app := newApp(tork.OpenAPI(tork.OpenAPIConfig{
		Security: []openapi.SecurityRequirement{{Name: "bearer"}},
	}))
	app.GET("/", hello)

	if _, hasSecurity := documentOf(t, app)["security"]; !hasSecurity {
		t.Error("a document-wide requirement belongs on the document")
	}
}

// A dependency reads a header, and the document says so — the parameter is
// part of the operation even though the handler never mentions it.
type DepAuthInput struct {
	Token string `header:"X-Auth"`
}

func authFromHeader(_ context.Context, in DepAuthInput) (Principal, error) {
	return Principal{name: in.Token}, nil
}

func TestDocumentIncludesDependencyParameters(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(authFromHeader))
	items.GET("/", func(_ context.Context, p Principal) (greeting, error) {
		return greeting{Message: p.name}, nil
	})
	app := newApp()
	app.Include(items)

	op := operationAt(t, documentOf(t, app), "/items", "get")
	params, _ := op["parameters"].([]any)
	if len(params) != 1 {
		t.Fatalf("parameters = %v, want the header the dependency reads", params)
	}
	param, _ := params[0].(map[string]any)
	if param["name"] != "X-Auth" || param["in"] != "header" {
		t.Errorf("parameter = %v", param)
	}
}

// A dependency and the handler may legitimately read the same parameter, and
// the document describes it once.
type SharedPage struct {
	Page int `query:"page"`
}

func pageDep(_ context.Context, in SharedPage) (Principal, error) {
	return Principal{}, nil
}

func TestDocumentDeduplicatesSharedParameters(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(pageDep))
	items.GET("/", func(_ context.Context, in SharedPage, p Principal) (greeting, error) {
		return greeting{}, nil
	})
	app := newApp()
	app.Include(items)

	op := operationAt(t, documentOf(t, app), "/items", "get")
	params, _ := op["parameters"].([]any)
	if len(params) != 1 {
		t.Errorf("parameters = %v, want one description of page", params)
	}
}

// Each declared version is its own document, and an operation ID may repeat
// across them.
func TestDocumentPerVersion(t *testing.T) {
	app := newApp()
	v1 := app.Version("v1", tork.Prefix("/api/v1"))
	v1.GET("/items", hello, tork.OperationID("items.list"))
	v2 := app.Version("v2", tork.Prefix("/api/v2"))
	v2.GET("/items", farewell, tork.OperationID("items.list"))

	first, err := app.OpenAPIFor("v1")
	if err != nil {
		t.Fatalf("v1: %v", err)
	}
	second, err := app.OpenAPIFor("v2")
	if err != nil {
		t.Fatalf("v2: %v", err)
	}

	firstPaths, _ := decodeDoc(t, first)["paths"].(map[string]any)
	if _, ok := firstPaths["/api/v1/items"]; !ok || len(firstPaths) != 1 {
		t.Errorf("v1 paths = %v", firstPaths)
	}
	secondPaths, _ := decodeDoc(t, second)["paths"].(map[string]any)
	if _, ok := secondPaths["/api/v2/items"]; !ok || len(secondPaths) != 1 {
		t.Errorf("v2 paths = %v", secondPaths)
	}

	// Everything is versioned, so the default document is the first version
	// rather than an empty one.
	defaultPaths, _ := documentOf(t, app)["paths"].(map[string]any)
	if _, ok := defaultPaths["/api/v1/items"]; !ok {
		t.Errorf("default paths = %v, want the first version", defaultPaths)
	}
}

// An application with no routes at all still describes itself.
func TestDocumentWithNoRoutes(t *testing.T) {
	paths, _ := documentOf(t, newApp())["paths"].(map[string]any)
	if len(paths) != 0 {
		t.Errorf("paths = %v", paths)
	}
}

// Two builds of one application produce the same bytes, which is what lets a
// change to the document be reviewed as a diff.
func TestDocumentIsDeterministic(t *testing.T) {
	build := func() []byte {
		app := newApp(tork.Title("Example"))
		items := tork.NewRouter(tork.Prefix("/items"), tork.Depends(authFromHeader))
		items.GET("/{item_id}", func(_ context.Context, in DocParams, p Principal) (DocBody, error) {
			return DocBody{}, nil
		}, tork.Tags("items"), tork.Throws[OutOfStock](), tork.Throws[Unavailable]())
		items.POST("/", func(_ context.Context, body DocBody) (greeting, error) { return greeting{}, nil })
		app.Include(items)

		doc, err := app.OpenAPI()
		if err != nil {
			t.Fatalf("document: %v", err)
		}
		return doc.JSON()
	}

	if !bytes.Equal(build(), build()) {
		t.Error("two builds of one application produced different documents")
	}
}

// A document built for a version nothing declared is empty rather than an
// error: asking about a version that does not exist is a question with an
// answer.
func TestDocumentForUnknownVersion(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	doc, err := app.OpenAPIFor("nope")
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	if paths, _ := decodeDoc(t, doc)["paths"].(map[string]any); len(paths) != 0 {
		t.Errorf("paths = %v", paths)
	}
}

// A document cannot be built from an application that does not build.
func TestDocumentReportsABuildFailure(t *testing.T) {
	app := newApp()
	app.GET("no-slash", hello)

	if _, err := app.OpenAPI(); err == nil {
		t.Error("expected the build error to surface")
	}
	if _, err := app.OpenAPIFor("v1"); err == nil {
		t.Error("expected the build error to surface")
	}
}

// An application that publishes nothing has no document to publish.
func TestNoOpenAPIProducesAnEmptyDescription(t *testing.T) {
	app := newApp(tork.NoOpenAPI())
	app.GET("/", hello)

	// The document can still be asked for directly — what NoOpenAPI switches
	// off is serving it, which the endpoints test covers.
	if _, err := app.OpenAPI(); err != nil {
		t.Fatalf("document: %v", err)
	}
}

// DefaultedParams carries a default and a required flag, both of which the
// document has to carry too.
type DefaultedParams struct {
	Limit int
	Sort  string
}

var defaultedParams = tork.DefineInput(func(b *tork.InputBuilder, in *DefaultedParams) {
	b.Query.Int(&in.Limit, "limit").Default(25)
	b.Query.String(&in.Sort, "sort").Required()
})

func TestDocumentCarriesDefaultsAndRequirements(t *testing.T) {
	_ = defaultedParams
	app := newApp()
	app.GET("/search", func(_ context.Context, in DefaultedParams) (greeting, error) {
		return greeting{}, nil
	})

	op := operationAt(t, documentOf(t, app), "/search", "get")
	for _, p := range op["parameters"].([]any) {
		param, _ := p.(map[string]any)
		schema, _ := param["schema"].(map[string]any)
		switch param["name"] {
		case "limit":
			if !sameJSON(schema["default"], 25) {
				t.Errorf("limit = %v, want its default", schema)
			}
		case "sort":
			if param["required"] != true {
				t.Errorf("sort = %v, want it required", param)
			}
		}
	}
}

// A body field that may be omitted structurally but was declared required is
// required, because the declaration is what the server enforces.
type OptionalShapedBody struct {
	tork.JSONBody
	Nickname string `json:"nickname,omitempty"`
}

var optionalShapedRules = tork.DefineBody(func(b *tork.BodyRules, in *OptionalShapedBody) {
	b.String(&in.Nickname).Required()
})

func TestDeclaredRequirementBeatsOmitempty(t *testing.T) {
	_ = optionalShapedRules
	app := newApp()
	app.POST("/people", func(_ context.Context, body OptionalShapedBody) (greeting, error) {
		return greeting{}, nil
	})

	doc := documentOf(t, app)
	components, _ := doc["components"].(map[string]any)
	schemas, _ := components["schemas"].(map[string]any)
	described, _ := schemas["OptionalShapedBody"].(map[string]any)
	required, _ := described["required"].([]any)
	if !containsAny(required, "nickname") {
		t.Errorf("required = %v, want the declared requirement", required)
	}
}

// RequiredForm has a required field and one with a default, so the form body
// carries both.
type RequiredForm struct {
	Note  string
	Pages int
}

var requiredForm = tork.DefineInput(func(b *tork.InputBuilder, in *RequiredForm) {
	b.Form.String(&in.Note, "note").Required()
	b.Form.Int(&in.Pages, "pages").Default(3)
})

func TestFormBodyCarriesRequirementsAndDefaults(t *testing.T) {
	_ = requiredForm
	app := newApp()
	app.POST("/forms", func(_ context.Context, in RequiredForm) (greeting, error) {
		return greeting{}, nil
	})

	op := operationAt(t, documentOf(t, app), "/forms", "post")
	body, _ := op["requestBody"].(map[string]any)
	content, _ := body["content"].(map[string]any)
	media, _ := content["application/x-www-form-urlencoded"].(map[string]any)
	schema, _ := media["schema"].(map[string]any)

	required, _ := schema["required"].([]any)
	if !containsAny(required, "note") {
		t.Errorf("required = %v", required)
	}
	props, _ := schema["properties"].(map[string]any)
	pages, _ := props["pages"].(map[string]any)
	if !sameJSON(pages["default"], 3) {
		t.Errorf("pages = %v, want its default", pages)
	}
}

// A tag two operations share is listed once.
func TestSharedTagIsListedOnce(t *testing.T) {
	app := newApp()
	app.GET("/a", hello, tork.Tags("shared"))
	app.GET("/b", farewell, tork.Tags("shared"))

	tags, _ := documentOf(t, app)["tags"].([]any)
	if len(tags) != 1 {
		t.Errorf("tags = %v, want one entry", tags)
	}
}

// A declared response for the status the handler already answers with does not
// describe it twice.
func TestDeclaredResponseForTheSuccessStatus(t *testing.T) {
	app := newApp()
	app.GET("/items", hello, tork.Responds[greeting](200, "The items."))

	op := operationAt(t, documentOf(t, app), "/items", "get")
	responses, _ := op["responses"].(map[string]any)
	if len(responses) != 1 {
		t.Errorf("responses = %v, want only one description of 200", responses)
	}
}

// A redirect chooses its status per request, so its own description says
// nothing about one; a response type that names a content type but no Go type
// describes the media without a schema.
type csvResponse struct{}

func (csvResponse) Spec() tork.ResponseSpec {
	return tork.ResponseSpec{Status: 200, ContentType: "text/csv"}
}

func (csvResponse) WriteResponse(w http.ResponseWriter, _ *http.Request) error {
	w.Header().Set("Content-Type", "text/csv")
	_, err := w.Write([]byte("a,b\n"))
	return err
}

func TestResponsesWithoutAGoType(t *testing.T) {
	app := newApp()
	app.GET("/moved", func(context.Context) (tork.Redirect, error) {
		return tork.SeeOther("/elsewhere"), nil
	})
	app.GET("/export", func(context.Context) (csvResponse, error) {
		return csvResponse{}, nil
	})

	doc := documentOf(t, app)

	moved := operationAt(t, doc, "/moved", "get")
	if responses, _ := moved["responses"].(map[string]any); len(responses) == 0 {
		t.Errorf("a redirect is still a response: %v", moved)
	}

	// A media type with no Go type behind it is described as the media, and
	// carries no schema to describe.
	export := operationAt(t, doc, "/export", "get")
	responses, _ := export["responses"].(map[string]any)
	ok, _ := responses["200"].(map[string]any)
	content, _ := ok["content"].(map[string]any)
	media, present := content["text/csv"].(map[string]any)
	if !present {
		t.Fatalf("content = %v, want the declared media type", content)
	}
	if _, hasSchema := media["schema"]; hasSchema {
		t.Errorf("there is no Go type to describe: %v", media)
	}
}

func containsAny(values []any, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
