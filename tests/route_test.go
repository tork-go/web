package tork_test

import (
	"context"
	"strings"
	"testing"

	"github.com/tork-go/web"
)

type greeting struct {
	Message string `json:"message"`
}

func hello(context.Context) (greeting, error) {
	return greeting{Message: "hello"}, nil
}

func farewell(context.Context) (greeting, error) {
	return greeting{Message: "goodbye"}, nil
}

// routePaths is the method-and-path list of a built application, which is
// what most of these tests are really asserting about.
func routePaths(t *testing.T, app *tork.App) []string {
	t.Helper()
	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	var paths []string
	for _, route := range routes {
		paths = append(paths, route.String())
	}
	return paths
}

func TestPrefixesConcatenate(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"))
	items.GET("/", hello)
	items.GET("/{item_id}", farewell)

	app := newApp()
	v1 := app.Version("v1", tork.Prefix("/api/v1"))
	v1.Include(items)

	got := routePaths(t, app)
	want := []string{"GET /api/v1/items", "GET /api/v1/items/{item_id}"}
	if !equalStrings(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func TestIncludeSitePrefixLandsBetweenParentAndChild(t *testing.T) {
	admin := tork.NewRouter(tork.Prefix("/settings"))
	admin.GET("/", hello)

	app := newApp()
	v1 := app.Version("v1", tork.Prefix("/api/v1"))
	v1.Include(admin, tork.Prefix("/admin"))

	got := routePaths(t, app)
	want := []string{"GET /api/v1/admin/settings"}
	if !equalStrings(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func TestRootPathIsTheRouterItself(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	got := routePaths(t, app)
	want := []string{"GET /"}
	if !equalStrings(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func TestTrailingSlashIsTrimmedFromARoutePath(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"))
	items.GET("/archive/", hello)

	app := newApp()
	app.Include(items)

	got := routePaths(t, app)
	want := []string{"GET /items/archive"}
	if !equalStrings(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func TestTagsAccumulateAndDeduplicate(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"), tork.Tags("items"))
	items.GET("/", hello, tork.Tags("custom", "items"))

	app := newApp(tork.Tags("v1"))
	app.Include(items)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := []string{"v1", "items", "custom"}
	if !equalStrings(routes[0].Tags, want) {
		t.Errorf("tags = %v, want %v", routes[0].Tags, want)
	}
}

// Two routers included into one parent must not see each other's tags, which
// is only true because the inherited slice is cloned rather than shared.
func TestSiblingRoutersDoNotShareTags(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"), tork.Tags("items"))
	items.GET("/", hello)
	users := tork.NewRouter(tork.Prefix("/users"), tork.Tags("users"))
	users.GET("/", farewell)

	app := newApp(tork.Tags("api"))
	app.Include(items)
	app.Include(users)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !equalStrings(routes[0].Tags, []string{"api", "items"}) {
		t.Errorf("items tags = %v", routes[0].Tags)
	}
	if !equalStrings(routes[1].Tags, []string{"api", "users"}) {
		t.Errorf("users tags = %v", routes[1].Tags)
	}
}

func TestDeprecatedIsInherited(t *testing.T) {
	legacy := tork.NewRouter(tork.Prefix("/legacy"), tork.Deprecated())
	legacy.GET("/", hello)

	app := newApp()
	app.Include(legacy)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if !routes[0].Deprecated {
		t.Error("route should have inherited Deprecated")
	}
}

func TestSummaryAndDescriptionAreNotInherited(t *testing.T) {
	app := newApp(tork.Description("the API itself"))
	app.GET("/", hello, tork.Summary("say hello"), tork.Description("greets the caller"))
	app.GET("/other", farewell)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if routes[0].Summary != "say hello" || routes[0].Description != "greets the caller" {
		t.Errorf("first route = %+v", routes[0])
	}
	if routes[1].Summary != "" || routes[1].Description != "" {
		t.Errorf("second route inherited documentation: %+v", routes[1])
	}
}

func TestVersionIsCarriedToEveryRouteUnderneath(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"))
	items.GET("/", hello)

	app := newApp()
	v1 := app.Version("v1", tork.Prefix("/api/v1"))
	v1.Include(items)
	app.GET("/health", farewell)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if routes[0].Version != "v1" {
		t.Errorf("versioned route has version %q", routes[0].Version)
	}
	if routes[1].Version != "" {
		t.Errorf("unversioned route has version %q", routes[1].Version)
	}
}

func TestOperationIDDefaultsToTheHandlerName(t *testing.T) {
	app := newApp()
	app.GET("/", hello)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if routes[0].OperationID != "tests_test.hello" {
		t.Errorf("operation ID = %q, want tests_test.hello", routes[0].OperationID)
	}
}

func TestOperationIDFallsBackToThePathForAnAnonymousHandler(t *testing.T) {
	app := newApp()
	app.GET("/items/{item_id}", func(context.Context) (greeting, error) {
		return greeting{}, nil
	})
	app.POST("/", func(context.Context) error { return nil })

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if routes[0].OperationID != "get.items.item_id" {
		t.Errorf("operation ID = %q, want get.items.item_id", routes[0].OperationID)
	}
	if routes[1].OperationID != "post" {
		t.Errorf("operation ID = %q, want post", routes[1].OperationID)
	}
}

// A handler that is not a usable function still has to get as far as being
// reported as one, which means naming the operation without inspecting it.
func TestOperationIDIsDerivedEvenForAnUnusableHandler(t *testing.T) {
	app := newApp()
	app.GET("/nil", nil)
	app.GET("/text", "not a function")

	msg := buildError(t, app)
	if !strings.Contains(msg, "handler is nil") || !strings.Contains(msg, "not a function") {
		t.Errorf("error = %q", msg)
	}
	// Both fell back to the path, so neither was reported as a duplicate
	// operation ID on top of its real problem.
	if strings.Contains(msg, "operation ID") {
		t.Errorf("unusable handlers collided on an operation ID: %q", msg)
	}
}

func TestExplicitOperationIDWins(t *testing.T) {
	app := newApp()
	app.GET("/", hello, tork.OperationID("root.read"))

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if routes[0].OperationID != "root.read" {
		t.Errorf("operation ID = %q", routes[0].OperationID)
	}
}

func TestDuplicateMethodAndPathIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/items", hello, tork.OperationID("a"))
	app.GET("/items", farewell, tork.OperationID("b"))

	if msg := buildError(t, app); !strings.Contains(msg, "GET /items is already declared at") {
		t.Errorf("error = %q", msg)
	}
}

func TestDuplicateOperationIDIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/a", hello)
	app.GET("/b", hello)

	msg := buildError(t, app)
	if !strings.Contains(msg, `operation ID "tests_test.hello" is already used by`) {
		t.Errorf("error = %q", msg)
	}
	if !strings.Contains(msg, "set tork.OperationID on one of them") {
		t.Errorf("error does not suggest the fix: %q", msg)
	}
}

// The same operation ID in two API versions is fine: each version is its own
// document, and a generated client for one never sees the other.
func TestOperationIDsAreUniquePerVersionOnly(t *testing.T) {
	app := newApp()
	v1 := app.Version("v1", tork.Prefix("/v1"))
	v1.GET("/", hello, tork.OperationID("root.read"))
	v2 := app.Version("v2", tork.Prefix("/v2"))
	v2.GET("/", farewell, tork.OperationID("root.read"))

	if _, err := app.Routes(); err != nil {
		t.Fatalf("build: %v", err)
	}
}

func TestPathMustBeginWithASlash(t *testing.T) {
	app := newApp()
	app.GET("items", hello)

	if msg := buildError(t, app); !strings.Contains(msg, `path "items" must begin with a slash`) {
		t.Errorf("error = %q", msg)
	}
}

func TestPrefixMustBeginWithASlash(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("items"))
	items.GET("/", hello)

	app := newApp()
	app.Include(items)

	if msg := buildError(t, app); !strings.Contains(msg, `prefix "items" must begin with a slash`) {
		t.Errorf("error = %q", msg)
	}
}

func TestPrefixMustNotEndWithASlash(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items/"))
	items.GET("/", hello)

	app := newApp()
	app.Include(items)

	if msg := buildError(t, app); !strings.Contains(msg, `prefix "/items/" must not end with a slash`) {
		t.Errorf("error = %q", msg)
	}
}

func TestEmptyPrefixIsAccepted(t *testing.T) {
	items := tork.NewRouter(tork.Prefix(""))
	items.GET("/items", hello)

	app := newApp()
	app.Include(items)

	got := routePaths(t, app)
	if !equalStrings(got, []string{"GET /items"}) {
		t.Errorf("routes = %v", got)
	}
}

func TestIncludingANilRouterIsRefused(t *testing.T) {
	app := newApp()
	app.Include(nil)

	if msg := buildError(t, app); !strings.Contains(msg, "tork.Include: router is nil") {
		t.Errorf("error = %q", msg)
	}
}

func TestHandleTakesAnyMethodAndUppercasesIt(t *testing.T) {
	app := newApp()
	app.Handle("trace", "/trace", hello)

	got := routePaths(t, app)
	if !equalStrings(got, []string{"TRACE /trace"}) {
		t.Errorf("routes = %v", got)
	}
}

func TestEveryMethodHelperDeclaresItsMethod(t *testing.T) {
	app := newApp()
	app.GET("/a", hello)
	app.POST("/a", hello, tork.OperationID("b"))
	app.PUT("/a", hello, tork.OperationID("c"))
	app.PATCH("/a", hello, tork.OperationID("d"))
	app.DELETE("/a", hello, tork.OperationID("e"))
	app.OPTIONS("/a", hello, tork.OperationID("f"))

	router := tork.NewRouter(tork.Prefix("/r"))
	router.GET("/", hello, tork.OperationID("g"))
	router.POST("/", hello, tork.OperationID("h"))
	router.PUT("/", hello, tork.OperationID("i"))
	router.PATCH("/", hello, tork.OperationID("j"))
	router.DELETE("/", hello, tork.OperationID("k"))
	router.OPTIONS("/", hello, tork.OperationID("l"))
	router.Handle("TRACE", "/", hello, tork.OperationID("m"))
	app.Include(router)

	got := routePaths(t, app)
	want := []string{
		"GET /a", "POST /a", "PUT /a", "PATCH /a", "DELETE /a", "OPTIONS /a",
		"GET /r", "POST /r", "PUT /r", "PATCH /r", "DELETE /r", "OPTIONS /r", "TRACE /r",
	}
	if !equalStrings(got, want) {
		t.Errorf("routes = %v, want %v", got, want)
	}
}

func equalStrings(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
