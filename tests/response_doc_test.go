package tork_test

import (
	"reflect"
	"testing"

	"github.com/tork-go/web"
)

// notFoundError and conflictError stand in for two distinct domain errors a
// handler or a dependency might throw — Throws only needs a type, so these
// carry nothing.
type notFoundError struct{}

func (notFoundError) Error() string { return "not found" }

type conflictError struct{}

func (conflictError) Error() string { return "conflict" }

func TestRespondsIsCollectedOnARoute(t *testing.T) {
	app := newApp()
	app.GET("/", hello, tork.Responds[greeting](404, "no such greeting"))

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	doc, ok := routes[0].Responses[404]
	if !ok {
		t.Fatal("status 404 was not recorded")
	}
	if doc.Status != 404 {
		t.Errorf("status = %d", doc.Status)
	}
	if doc.Type != reflect.TypeFor[greeting]() {
		t.Errorf("type = %v", doc.Type)
	}
	if doc.Description != "no such greeting" {
		t.Errorf("description = %q", doc.Description)
	}
}

// A route-level Responds for a status the router already declared replaces
// the router's, the same way every other inherited field a route redeclares
// does; a status only the router declared is still inherited untouched.
func TestRespondsMergesByStatusRouteOverridesRouter(t *testing.T) {
	items := tork.NewRouter(
		tork.Prefix("/items"),
		tork.Responds[tork.Error](404, "router: not found"),
		tork.Responds[tork.Error](500, "router: internal error"),
	)
	items.GET("/", hello, tork.Responds[tork.Error](404, "route: no such item"))

	app := newApp()
	app.Include(items)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := routes[0].Responses[404].Description; got != "route: no such item" {
		t.Errorf("404 description = %q, want the route's own", got)
	}
	if got := routes[0].Responses[500].Description; got != "router: internal error" {
		t.Errorf("500 description = %q, want the inherited one", got)
	}
}

func TestThrowsAccumulatesAcrossRouterAndRoute(t *testing.T) {
	items := tork.NewRouter(tork.Prefix("/items"), tork.Throws[conflictError]())
	items.GET("/", hello, tork.Throws[notFoundError]())

	app := newApp()
	app.Include(items)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(routes[0].Throws) != 2 {
		t.Fatalf("throws = %v, want 2 entries", routes[0].Throws)
	}
	if _, ok := routes[0].Throws[reflect.TypeFor[conflictError]()]; !ok {
		t.Error("the router's Throws was not inherited")
	}
	if _, ok := routes[0].Throws[reflect.TypeFor[notFoundError]()]; !ok {
		t.Error("the route's own Throws was not recorded")
	}
}

// Throws has no status to collide over, so the same type declared twice is
// not two entries — it is the same operation still failing the same way.
func TestThrowsDeduplicatesTheSameType(t *testing.T) {
	app := newApp()
	app.GET("/", hello, tork.Throws[notFoundError](), tork.Throws[notFoundError]())

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if len(routes[0].Throws) != 1 {
		t.Errorf("throws = %v, want 1 entry", routes[0].Throws)
	}
}

// Two routers included into one parent must not see each other's Responds or
// Throws, which is only true because inherited() clones the maps rather than
// sharing them — the same guarantee TestSiblingRoutersDoNotShareTags proves
// for tags.
func TestSiblingRoutersDoNotShareResponsesOrThrows(t *testing.T) {
	items := tork.NewRouter(
		tork.Prefix("/items"),
		tork.Responds[tork.Error](404, "items: not found"),
		tork.Throws[notFoundError](),
	)
	items.GET("/", hello)
	users := tork.NewRouter(
		tork.Prefix("/users"),
		tork.Responds[tork.Error](409, "users: conflict"),
		tork.Throws[conflictError](),
	)
	users.GET("/", farewell)

	app := newApp()
	app.Include(items)
	app.Include(users)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if _, ok := routes[0].Responses[409]; ok {
		t.Error("items route picked up users' 409")
	}
	if _, ok := routes[0].Throws[reflect.TypeFor[conflictError]()]; ok {
		t.Error("items route picked up users' Throws")
	}
	if _, ok := routes[1].Responses[404]; ok {
		t.Error("users route picked up items' 404")
	}
	if _, ok := routes[1].Throws[reflect.TypeFor[notFoundError]()]; ok {
		t.Error("users route picked up items' Throws")
	}
}

// The application is also the root of the router tree, so a Responds or
// Throws declared where New is called reaches every route the same way one
// declared on any other router would — the same reasoning that already lets
// Tags and Use be declared there.
func TestRespondsAndThrowsDeclaredOnTheApplicationReachEveryRoute(t *testing.T) {
	app := newApp(tork.Responds[tork.Error](500, "app: internal error"), tork.Throws[conflictError]())
	app.GET("/", hello)

	routes, err := app.Routes()
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	if got := routes[0].Responses[500].Description; got != "app: internal error" {
		t.Errorf("500 description = %q", got)
	}
	if _, ok := routes[0].Throws[reflect.TypeFor[conflictError]()]; !ok {
		t.Error("the application's Throws was not inherited")
	}
}
