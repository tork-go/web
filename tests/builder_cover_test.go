package tork_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tork-go/web"
)

// EveryKindInput exercises the builder methods the other tests do not reach,
// so that every declaration this package offers is known to work rather than
// only known to compile.
type EveryKindInput struct {
	Name     string
	Count    int
	Total    int64
	Ratio    float64
	Enabled  bool
	At       time.Time
	Wait     time.Duration
	Sizes    []int
	Cursor   tork.Optional[int]
	BodyPart EveryKindBody
}

type EveryKindBody struct {
	Name  string             `json:"name"`
	Total int64              `json:"total"`
	Ratio float64            `json:"ratio"`
	Ok    bool               `json:"ok"`
	At    time.Time          `json:"at"`
	Sizes []int              `json:"sizes"`
	Extra tork.Optional[int] `json:"extra,omitzero"`
	Kept  string             `json:"-"`
}

var everyKindBody = tork.DefineBody(func(b *tork.BodyRules, in *EveryKindBody) {
	b.String(&in.Name).MinLen(2)
	b.Int64(&in.Total).Required().Min(1)
	b.Float64(&in.Ratio).Max(10)
	b.Bool(&in.Ok).Required()
	b.Time(&in.At).Before(farFuture)
	b.Ints(&in.Sizes).MinItems(1)
	b.OptionalInt(&in.Extra).Max(5)
})

var farFuture = time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC)

var everyKindInput = tork.DefineInput(func(b *tork.InputBuilder, in *EveryKindInput) {
	b.Query.String(&in.Name, "name").Default("anon")
	b.Query.Int(&in.Count, "count").Required()
	b.Query.Int64(&in.Total, "total").Default64(7).Required()
	b.Query.Float64(&in.Ratio, "ratio").Required().Default(1.5)
	b.Query.Bool(&in.Enabled, "enabled").Required()
	b.Query.Time(&in.At, "at").Required().Default(farFuture).Before(farFuture)
	b.Query.Duration(&in.Wait, "wait").Required().Default(time.Second)
	b.Query.Ints(&in.Sizes, "sizes").Required()
	b.Query.OptionalInt(&in.Cursor, "cursor").Required()
	b.JSONBody(&in.BodyPart)
})

func TestEveryBuilderKindBinds(t *testing.T) {
	var got EveryKindInput
	app := newApp()
	app.POST("/every", func(_ context.Context, in EveryKindInput) (string, error) {
		got = in
		return "ok", nil
	})

	query := "?name=ada&count=2&total=9&ratio=2.5&enabled=true" +
		"&at=2026-07-23T16:37:23Z&wait=5s&sizes=1&sizes=2&cursor=3"
	rec := post(t, app, "POST", "/every"+query, "application/json",
		`{"name":"nn","total":2,"ratio":1,"ok":true,"at":"2026-07-23T16:37:23Z","sizes":[1],"extra":4}`)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Name != "ada" || got.Count != 2 || got.Total != 9 || got.Ratio != 2.5 || !got.Enabled {
		t.Errorf("got %+v", got)
	}
	if got.Wait != 5*time.Second || len(got.Sizes) != 2 {
		t.Errorf("got %+v", got)
	}
	if cursor, ok := got.Cursor.Get(); !ok || cursor != 3 {
		t.Errorf("Cursor = %d, ok = %v", cursor, ok)
	}
	if got.BodyPart.Name != "nn" || !got.BodyPart.Ok {
		t.Errorf("body = %+v", got.BodyPart)
	}
}

// Every Required declared above is reported when nothing is sent.
func TestEveryRequiredIsReported(t *testing.T) {
	app := newApp()
	app.POST("/every", func(_ context.Context, in EveryKindInput) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/every", "application/json", `{"total":1,"ok":true}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	details := decodeFieldErrors(t, rec)
	if len(details) != 8 {
		t.Fatalf("details = %+v", details)
	}
	for _, detail := range details {
		if detail.Issue != tork.IssueFieldRequired {
			t.Errorf("field error = %+v", detail)
		}
	}
}

func TestBodyRulesForEveryKind(t *testing.T) {
	app := newApp()
	app.POST("/every", func(_ context.Context, in EveryKindInput) (string, error) { return "ok", nil })

	query := "?count=1&total=1&ratio=1&enabled=true&at=2026-07-23T16:37:23Z&wait=1s&sizes=1&cursor=1"
	tests := []struct {
		name  string
		body  string
		field string
		issue string
	}{
		{"int64 min", `{"total":0,"ok":true}`, "total", tork.IssueFieldRequired},
		{"float max", `{"total":1,"ok":true,"ratio":99}`, "ratio", tork.IssueMaximumExceeded},
		{"bool required", `{"total":1}`, "ok", tork.IssueFieldRequired},
		{"time before", `{"total":1,"ok":true,"at":"2200-01-01T00:00:00Z"}`, "at", tork.IssueTooLate},
		{"ints min items", `{"total":1,"ok":true,"sizes":[]}`, "sizes", tork.IssueTooFewItems},
		{"optional int max", `{"total":1,"ok":true,"extra":9}`, "extra", tork.IssueMaximumExceeded},
		{"string min len", `{"total":1,"ok":true,"name":"x"}`, "name", tork.IssueTooShort},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, app, "POST", "/every"+query, "application/json", tt.body)
			details := decodeFieldErrors(t, rec)

			for _, detail := range details {
				if detail.Field == tt.field && detail.Issue == tt.issue {
					return
				}
			}
			t.Errorf("details = %+v, want %s/%s", details, tt.field, tt.issue)
		})
	}
}

// A field encoding/json will not write cannot carry a rule, because nothing
// would ever reach it.
type UnserializedBody struct {
	tork.JSONBody
	Hidden string `json:"-"`
}

var unserializedBody = tork.DefineBody(func(b *tork.BodyRules, in *UnserializedBody) {
	b.String(&in.Hidden).Required()
})

func TestRuleOnAnUnserializedFieldIsRefused(t *testing.T) {
	app := newApp()
	app.POST("/hidden", func(context.Context, UnserializedBody) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "is not serialized, so no rule can apply to it") {
		t.Errorf("error = %q", msg)
	}
}

// A body field with no json tag is serialized under its Go name.
type UntaggedBody struct {
	tork.JSONBody
	Name string
}

var untaggedBody = tork.DefineBody(func(b *tork.BodyRules, in *UntaggedBody) {
	b.String(&in.Name).Required()
})

func TestBodyFieldWithoutAJSONTagUsesItsGoName(t *testing.T) {
	app := newApp()
	app.POST("/untagged", func(context.Context, UntaggedBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/untagged", "application/json", `{}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if details := decodeFieldErrors(t, rec); details[0].Field != "Name" {
		t.Errorf("field error = %+v", details[0])
	}
}

// A json tag that names only options keeps the Go field name.
type OptionsOnlyBody struct {
	tork.JSONBody
	Name string `json:",omitempty"`
}

var optionsOnlyBody = tork.DefineBody(func(b *tork.BodyRules, in *OptionsOnlyBody) {
	b.String(&in.Name).Required()
})

func TestJSONTagWithOnlyOptionsKeepsTheFieldName(t *testing.T) {
	app := newApp()
	app.POST("/options", func(context.Context, OptionsOnlyBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/options", "application/json", `{}`)
	if details := decodeFieldErrors(t, rec); details[0].Field != "Name" {
		t.Errorf("field error = %+v", details[0])
	}
}

// ------------------------------------------------------- declaration errors

// A field nobody declared would silently stay zero forever, so it is refused.
type IncompleteInput struct {
	Page   int
	Forgot string
}

var incompleteInput = tork.DefineInput(func(b *tork.InputBuilder, in *IncompleteInput) {
	b.Query.Int(&in.Page, "page")
})

func TestUndeclaredFieldIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/incomplete", func(context.Context, IncompleteInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "field Forgot of tork_test.IncompleteInput has no binding declared") {
		t.Errorf("error = %q", msg)
	}
}

type TwiceInput struct {
	Page int
}

var twiceInput = tork.DefineInput(func(b *tork.InputBuilder, in *TwiceInput) {
	b.Query.Int(&in.Page, "page")
	b.Query.Int(&in.Page, "p")
})

func TestFieldDeclaredTwiceIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/twice", func(context.Context, TwiceInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "field Page is declared twice") {
		t.Errorf("error = %q", msg)
	}
}

type ForeignInput struct {
	Page int
}

var stray int

var foreignInput = tork.DefineInput(func(b *tork.InputBuilder, in *ForeignInput) {
	b.Query.Int(&stray, "stray")
})

func TestPointerOutsideTheStructIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/foreign", func(context.Context, ForeignInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "does not name a field of tork_test.ForeignInput") {
		t.Errorf("error = %q", msg)
	}
}

type NilPointerInput struct {
	Page int
}

var nilPointerInput = tork.DefineInput(func(b *tork.InputBuilder, in *NilPointerInput) {
	b.Query.Int(nil, "page")
})

func TestNilFieldPointerIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/nilptr", func(context.Context, NilPointerInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "expected a pointer to a field of") {
		t.Errorf("error = %q", msg)
	}
}

type BadPatternInput struct {
	Slug string
}

var badPatternInput = tork.DefineInput(func(b *tork.InputBuilder, in *BadPatternInput) {
	b.Query.String(&in.Slug, "slug").Pattern("([")
})

func TestInvalidPatternIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/pattern", func(context.Context, BadPatternInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "has an invalid pattern") {
		t.Errorf("error = %q", msg)
	}
}

type TwoBodiesInput struct {
	First  UpdateItemBody
	Second UpdateItemBody
}

var twoBodiesInput = tork.DefineInput(func(b *tork.InputBuilder, in *TwoBodiesInput) {
	b.JSONBody(&in.First)
	b.JSONBody(&in.Second)
})

func TestTwoBodiesFromTheBuilderAreRefused(t *testing.T) {
	app := newApp()
	app.POST("/two", func(context.Context, TwoBodiesInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "a request has one body, and one is already declared") {
		t.Errorf("error = %q", msg)
	}
}

// A struct declared both ways has two answers to what it means, so it gets
// neither.
type DoublyDeclaredInput struct {
	Page int `query:"page"`
}

var doublyDeclaredInput = tork.DefineInput(func(b *tork.InputBuilder, in *DoublyDeclaredInput) {
	b.Query.Int(&in.Page, "page")
})

func TestStructDeclaredBothWaysIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/doubly", func(context.Context, DoublyDeclaredInput) (string, error) { return "", nil })

	msg := buildError(t, app)
	if !strings.Contains(msg, "is declared by tork.DefineInput and also carries binding tags") {
		t.Errorf("error = %q", msg)
	}
}

// An embedded struct is declared through, and embedding it is not itself
// reported as an undeclared field.
type EmbeddedPage struct {
	Page int
}

type EmbeddedBuilderInput struct {
	EmbeddedPage
	Search string
}

var embeddedBuilderInput = tork.DefineInput(func(b *tork.InputBuilder, in *EmbeddedBuilderInput) {
	b.Query.Int(&in.Page, "page").Default(1)
	b.Query.String(&in.Search, "search")
})

func TestBuilderWalksEmbeddedStructs(t *testing.T) {
	var got EmbeddedBuilderInput
	app := newApp()
	app.GET("/embedded", func(_ context.Context, in EmbeddedBuilderInput) (string, error) {
		got = in
		return "ok", nil
	})

	rec := do(t, app, "GET", "/embedded?page=4&search=hat", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Page != 4 || got.Search != "hat" {
		t.Errorf("got %+v", got)
	}
}

// A whole embedded struct can also be the body, which is the one case where a
// struct field is itself the target.
type EmbeddedBodyInput struct {
	ItemID string
	Body   UpdateItemBody
}

var embeddedBodyInput = tork.DefineInput(func(b *tork.InputBuilder, in *EmbeddedBodyInput) {
	b.Path.String(&in.ItemID, "item_id")
	b.JSONBody(&in.Body)
})

func TestBuilderDeclaredBody(t *testing.T) {
	var got EmbeddedBodyInput
	app := newApp()
	app.PUT("/items/{item_id}", func(_ context.Context, in EmbeddedBodyInput) (string, error) {
		got = in
		return "ok", nil
	})

	rec := post(t, app, "PUT", "/items/42", "application/json", `{"name":"Boots","price":1}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.ItemID != "42" || got.Body.Name != "Boots" {
		t.Errorf("got %+v", got)
	}
}

func TestUUIDShapesRefused(t *testing.T) {
	app := newApp()
	app.GET("/accounts", func(_ context.Context, in AccountInput) (string, error) { return "ok", nil })

	for _, id := range []string{
		"123e4567e89b12d3a456426614174000",     // no hyphens
		"123e4567-e89b-12d3-a456-42661417400g", // not hex
		"123e4567+e89b-12d3-a456-426614174000", // hyphen in the wrong place
	} {
		rec := do(t, app, "GET", "/accounts?id="+id, nil)
		if rec.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d", id, rec.Code)
		}
	}
}
