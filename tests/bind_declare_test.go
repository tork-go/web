package tork_test

import (
	"context"
	"mime/multipart"
	"strings"
	"testing"

	"github.com/tork-go/web"
)

// Everything here is a mistake in the caller's source, caught when the
// application builds rather than when a request arrives.
func TestInputDeclarationsRefused(t *testing.T) {
	tests := []struct {
		name    string
		path    string
		handler any
		want    string
	}{
		{
			name: "path field with no wildcard",
			path: "/items",
			handler: func(context.Context, struct {
				ItemID string `path:"item_id"`
			}) (string, error) {
				return "", nil
			},
			want: `field ItemID reads the path parameter "item_id", but GET /items has no {item_id} in it`,
		},
		{
			name: "header with no name",
			path: "/",
			handler: func(context.Context, struct {
				TraceID string `header:""`
			}) (string, error) {
				return "", nil
			},
			want: "field TraceID has no header name",
		},
		{
			name: "two source tags on one field",
			path: "/",
			handler: func(context.Context, struct {
				Page int `query:"page" header:"X-Page"`
			}) (string, error) {
				return "", nil
			},
			want: "field Page carries both query and header tags",
		},
		{
			name: "untagged field",
			path: "/",
			handler: func(context.Context, struct {
				Page  int `query:"page"`
				Extra int
			}) (string, error) {
				return "", nil
			},
			want: "field Extra of struct",
		},
		{
			name: "unknown modifier",
			path: "/",
			handler: func(context.Context, struct {
				Tags []string `query:"tags,tsv"`
			}) (string, error) {
				return "", nil
			},
			want: `field Tags has an unknown query modifier "tsv"; the only one is csv`,
		},
		{
			name: "csv on a scalar",
			path: "/",
			handler: func(context.Context, struct {
				Tags string `query:"tags,csv"`
			}) (string, error) {
				return "", nil
			},
			want: "field Tags is marked csv but is not a slice",
		},
		{
			name: "undecodable field type",
			path: "/",
			handler: func(context.Context, struct {
				Weird chan int `query:"weird"`
			}) (string, error) {
				return "", nil
			},
			want: "field Weird: no parameter can be read into a chan int",
		},
		{
			name: "default that does not fit",
			path: "/",
			handler: func(context.Context, struct {
				Page int `query:"page" default:"lots"`
			}) (string, error) {
				return "", nil
			},
			want: `field Page has an invalid default "lots": it must be a whole number`,
		},
		{
			name: "duplicate wire name",
			path: "/",
			handler: func(context.Context, struct {
				Page  int `query:"page"`
				Other int `query:"page"`
			}) (string, error) {
				return "", nil
			},
			want: "field Other reads query.page, which field Page already reads",
		},
		{
			name: "unknown body format",
			path: "/",
			handler: func(context.Context, struct {
				Body UpdateItemBody `body:"xml"`
			}) (string, error) {
				return "", nil
			},
			want: `field Body has body tag "xml"; the only body format is json`,
		},
		{
			name: "file outside a form",
			path: "/",
			handler: func(context.Context, struct {
				Avatar *multipart.FileHeader `query:"avatar"`
			}) (string, error) {
				return "", nil
			},
			want: "field Avatar is an uploaded file, which only a form can carry",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newApp()
			app.GET(tt.path, tt.handler, tork.OperationID("under.test"))

			if msg := buildError(t, app); !strings.Contains(msg, tt.want) {
				t.Errorf("error = %q,\nwant it to contain %q", msg, tt.want)
			}
		})
	}
}

// A struct that both embeds the marker and carries parameters has two answers
// to what it is, so it gets neither.
type ConfusedBody struct {
	tork.JSONBody
	ItemID string `path:"item_id"`
	Name   string `json:"name"`
}

func TestMarkedBodyCannotAlsoCarryParameters(t *testing.T) {
	app := newApp()
	app.POST("/items/{item_id}", func(context.Context, ConfusedBody) (string, error) {
		return "", nil
	})

	msg := buildError(t, app)
	if !strings.Contains(msg, "embeds tork.JSONBody and also carries parameter tags") {
		t.Errorf("error = %q", msg)
	}
}

type FirstBodyInput struct {
	Body UpdateItemBody `body:"json"`
}

type SecondBodyInput struct {
	Body UpdateItemBody `body:"json"`
}

func TestOnlyOneBodyPerHandler(t *testing.T) {
	app := newApp()
	app.POST("/items", func(context.Context, FirstBodyInput, SecondBodyInput) (string, error) {
		return "", nil
	})

	if msg := buildError(t, app); !strings.Contains(msg, "is a second request body") {
		t.Errorf("error = %q", msg)
	}
}

type FormAndBodyInput struct {
	Username string         `form:"username"`
	Body     UpdateItemBody `body:"json"`
}

// A form and a body both consume the request, which can only be read once.
func TestFormAndBodyCannotBeMixed(t *testing.T) {
	app := newApp()
	app.POST("/items", func(context.Context, FormAndBodyInput) (string, error) {
		return "", nil
	})

	if msg := buildError(t, app); !strings.Contains(msg, "a request body is read once") {
		t.Errorf("error = %q", msg)
	}
}

type BodyThenFormInput struct {
	Body     UpdateItemBody `body:"json"`
	Username string         `form:"username"`
}

func TestBodyThenFormIsAlsoRefused(t *testing.T) {
	app := newApp()
	app.POST("/items", func(context.Context, BodyThenFormInput) (string, error) {
		return "", nil
	})

	if msg := buildError(t, app); !strings.Contains(msg, "a request body is read once") {
		t.Errorf("error = %q", msg)
	}
}

type MarkedBody struct {
	tork.JSONBody
	Name string `json:"name"`
}

func TestMarkedBodyAndTaggedBodyCollide(t *testing.T) {
	app := newApp()
	app.POST("/items", func(context.Context, MarkedBody, FirstBodyInput) (string, error) {
		return "", nil
	})

	if msg := buildError(t, app); !strings.Contains(msg, "is a second request body") {
		t.Errorf("error = %q", msg)
	}
}

// A struct with no binding tags and no marker is not an input at all; it is a
// dependency, which nothing can supply yet.
func TestUntaggedStructIsNotAnInput(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context, *Service) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "cannot supply a value of type *tork_test.Service") {
		t.Errorf("error = %q", msg)
	}
}

// An unexported field cannot be written to, so it is left alone rather than
// refused for having no tag.
type UnexportedFieldInput struct {
	Page   int `query:"page"`
	hidden string
}

func TestUnexportedFieldsAreIgnored(t *testing.T) {
	got, rec := bound[UnexportedFieldInput](t, "GET", "/", "/?page=3", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Page != 3 {
		t.Errorf("Page = %d", got.Page)
	}
}

// A wildcard nothing reads is fine: something further up the tree may be what
// consumes it once dependencies can declare parameters too.
func TestWildcardWithNoFieldIsAllowed(t *testing.T) {
	app := newApp()
	app.GET("/tenants/{tenant}/items/{item_id}", func(_ context.Context, in PathInput) (string, error) {
		return in.ItemID, nil
	})

	rec := do(t, app, "GET", "/tenants/acme/items/42", nil)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if rec.Body.String() != `"42"` {
		t.Errorf("body = %s", rec.Body)
	}
}

// ServeMux spells a multi-segment wildcard {name...}, and the name is the same
// either way.
func TestMultiSegmentWildcard(t *testing.T) {
	type FileInput struct {
		Rest string `path:"rest"`
	}

	app := newApp()
	app.GET("/files/{rest...}", func(_ context.Context, in FileInput) (string, error) {
		return in.Rest, nil
	})

	rec := do(t, app, "GET", "/files/a/b/c.txt", nil)
	if rec.Body.String() != `"a/b/c.txt"` {
		t.Errorf("body = %s", rec.Body)
	}
}
