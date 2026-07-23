package tork_test

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tork-go/web"
)

// TypesBody exists to have one field of every JSON shape, so that a wrong
// value for each proves the message names the shape the client should have
// sent.
type TypesBody struct {
	tork.JSONBody
	Name   string         `json:"name"`
	Count  int            `json:"count"`
	Ratio  float64        `json:"ratio"`
	Active bool           `json:"active"`
	Tags   []string       `json:"tags"`
	Extra  map[string]any `json:"extra"`
}

func TestWrongJSONTypesNameWhatWasWanted(t *testing.T) {
	app := newApp()
	app.POST("/things", func(_ context.Context, body TypesBody) (string, error) {
		return "ok", nil
	})

	tests := []struct {
		body string
		want string
	}{
		{`{"name":1}`, "name must be a string."},
		{`{"count":"x"}`, "count must be a whole number."},
		{`{"ratio":"x"}`, "ratio must be a number."},
		{`{"active":"x"}`, "active must be true or false."},
		{`{"tags":"x"}`, "tags must be an array."},
		{`{"extra":"x"}`, "extra must be an object."},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			rec := post(t, app, "POST", "/things", "application/json", tt.body)
			details := decodeFieldErrors(t, rec)
			if len(details) != 1 || details[0].Message != tt.want {
				t.Errorf("details = %+v", details)
			}
		})
	}
}

// A body of the wrong shape entirely has no field to blame, so it is reported
// against the body itself.
func TestWrongBodyShapeIsBlamedOnTheBody(t *testing.T) {
	app := newApp()
	app.POST("/things", func(_ context.Context, body TypesBody) (string, error) {
		return "ok", nil
	})

	rec := post(t, app, "POST", "/things", "application/json", `[1,2,3]`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	details := decodeFieldErrors(t, rec)
	if details[0].Field != "body" || details[0].Issue != tork.IssueInvalidType {
		t.Errorf("field error = %+v", details[0])
	}
}

// errReader fails part-way through, the way a connection that drops does.
type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("the client hung up") }

func TestUnreadableBodyIsAClientError(t *testing.T) {
	app := newApp()
	app.POST("/things", func(_ context.Context, body TypesBody) (string, error) {
		return "ok", nil
	})

	request := httptest.NewRequest("POST", "/things", io.NopCloser(errReader{}))
	request.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, request)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if e := decodeError(t, rec); e.Code != "BAD_REQUEST" {
		t.Errorf("code = %q", e.Code)
	}
}

type UintInput struct {
	Count uint16 `query:"count"`
}

func TestUnsignedBinding(t *testing.T) {
	got, rec := bound[UintInput](t, "GET", "/", "/?count=65535", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Count != 65535 {
		t.Errorf("Count = %d", got.Count)
	}
}

type PointerAndSliceInput struct {
	Page  *int  `query:"page"`
	Sizes []int `query:"sizes"`
}

// A container reports what went wrong inside it, naming the field rather than
// the element.
func TestContainersReportABadValueInside(t *testing.T) {
	for _, target := range []string{"/?page=abc", "/?sizes=1&sizes=abc"} {
		_, rec := bound[PointerAndSliceInput](t, "GET", "/", target, nil)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("%s: status %d", target, rec.Code)
		}
		if details := decodeFieldErrors(t, rec); details[0].Issue != tork.IssueInvalidInteger {
			t.Errorf("%s: field error = %+v", target, details[0])
		}
	}
}

func TestContainersOfUndecodableTypesAreRefused(t *testing.T) {
	tests := []struct {
		name    string
		handler any
	}{
		{
			name: "pointer",
			handler: func(context.Context, struct {
				Weird *chan int `query:"weird"`
			}) (string, error) {
				return "", nil
			},
		},
		{
			name: "slice",
			handler: func(context.Context, struct {
				Weird []chan int `query:"weird"`
			}) (string, error) {
				return "", nil
			},
		},
		{
			name: "optional",
			handler: func(context.Context, struct {
				Weird tork.Optional[chan int] `query:"weird"`
			}) (string, error) {
				return "", nil
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := newApp()
			app.GET("/", tt.handler)

			if msg := buildError(t, app); !strings.Contains(msg, "no parameter can be read into a chan int") {
				t.Errorf("error = %q", msg)
			}
		})
	}
}

// A bad field inside an embedded struct is reported like any other.
type BadPagination struct {
	Page int `query:"page" default:"lots"`
}

type EmbeddedBadInput struct {
	BadPagination
	Search string `query:"search"`
}

func TestEmbeddedStructsReportTheirOwnProblems(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context, EmbeddedBadInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, `field Page has an invalid default "lots"`) {
		t.Errorf("error = %q", msg)
	}
}

// The marked body is the one that arrives second here, so it is the one that
// finds the body already claimed.
func TestTaggedBodyThenMarkedBodyCollide(t *testing.T) {
	app := newApp()
	app.POST("/items", func(context.Context, FirstBodyInput, MarkedBody) (string, error) {
		return "", nil
	})

	if msg := buildError(t, app); !strings.Contains(msg, "is a second request body") {
		t.Errorf("error = %q", msg)
	}
}

func TestMaxBodyBytesMustBePositive(t *testing.T) {
	app := tork.New(tork.MaxBodyBytes(0))
	app.GET("/", hello)

	if msg := buildError(t, app); !strings.Contains(msg, "tork.MaxBodyBytes: limit must be positive") {
		t.Errorf("error = %q", msg)
	}
}

// A mapper sees a binding failure like any other error, message included,
// which is what lets an application rewrite how rejected input is reported.
func TestMapperSeesABindingFailure(t *testing.T) {
	var seen string
	app := newApp(tork.OnError(func(err error) *tork.Error {
		seen = err.Error()
		return nil
	}))
	app.GET("/", func(_ context.Context, in QueryInput) (string, error) { return "", nil })

	rec := do(t, app, "GET", "/?page=abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if !strings.Contains(seen, "VALIDATION_ERROR") {
		t.Errorf("the mapper saw %q", seen)
	}
}

// An empty value among several is dropped, and the ones after it are kept.
type RepeatedInput struct {
	Sort []string `query:"sort"`
}

func TestEmptyValuesAreDroppedFromARepeatedParameter(t *testing.T) {
	got, rec := bound[RepeatedInput](t, "GET", "/", "/?sort=a&sort=&sort=b&sort=", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !equalStrings(got.Sort, []string{"a", "b"}) {
		t.Errorf("Sort = %v", got.Sort)
	}
}

// A struct with only files still has to notice that the request is not a form.
type FileOnlyInput struct {
	Avatar *multipart.FileHeader `form:"avatar"`
}

func TestFileFieldRequiresAForm(t *testing.T) {
	app := newApp()
	app.POST("/upload", func(_ context.Context, in FileOnlyInput) (string, error) {
		return "ok", nil
	})

	rec := post(t, app, "POST", "/upload", "application/json", `{}`)
	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
}

// A plain struct with no tags and no marker is a dependency, not an input.
type Service struct{ Name string }

func TestUntaggedStructValueIsNotAnInput(t *testing.T) {
	app := newApp()
	app.GET("/", func(context.Context, Service) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "cannot supply a value of type tork_test.Service") {
		t.Errorf("error = %q", msg)
	}
}

func TestOptionalOrReturnsTheValueWhenThereIsOne(t *testing.T) {
	got, _ := bound[OptionalQueryInput](t, "GET", "/", "/?search=boots", nil)
	if got.Search.Or("fallback") != "boots" {
		t.Errorf("Or = %q", got.Search.Or("fallback"))
	}
}
