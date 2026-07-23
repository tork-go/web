package tork_test

import (
	"context"
	"net/http"
	"reflect"
	"testing"
)

// The derived wire name is a contract, so it is asserted through the only door
// it is visible from: a request that binds only if the name matches.
func TestDerivedWireNames(t *testing.T) {
	tests := []struct {
		field string
		want  string
	}{
		{"Page", "page"},
		{"PageSize", "pageSize"},
		{"ItemID", "itemId"},
		{"ID", "id"},
		{"HTTPServer", "httpServer"},
		{"OAuth2Token", "oAuth2Token"},
		{"X", "x"},
	}

	for _, tt := range tests {
		t.Run(tt.field, func(t *testing.T) {
			if got := deriveName(t, tt.field); got != tt.want {
				t.Errorf("%s derives %q, want %q", tt.field, got, tt.want)
			}
		})
	}
}

// deriveName builds a one-field input struct at run time and reports which
// query parameter it reads.
func deriveName(t *testing.T, field string) string {
	t.Helper()

	structType := reflect.StructOf([]reflect.StructField{{
		Name: field,
		Type: reflect.TypeFor[string](),
		Tag:  `query:""`,
	}})

	var seen string
	handler := reflect.MakeFunc(
		reflect.FuncOf(
			[]reflect.Type{reflect.TypeFor[context.Context](), structType},
			[]reflect.Type{reflect.TypeFor[string](), reflect.TypeFor[error]()},
			false,
		),
		func(args []reflect.Value) []reflect.Value {
			seen = args[1].Field(0).String()
			return []reflect.Value{reflect.ValueOf("ok"), reflect.Zero(reflect.TypeFor[error]())}
		},
	).Interface()

	app := newApp()
	app.GET("/", handler)

	// Every candidate name is sent, and the one the field bound is the one
	// the framework derived.
	query := "/?"
	for _, candidate := range []string{
		"page", "pageSize", "itemId", "id", "httpServer", "oAuth2Token", "x",
	} {
		query += candidate + "=" + candidate + "&"
	}

	rec := do(t, app, "GET", query, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	return seen
}
