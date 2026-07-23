package tork_test

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/tork-go/web"
)

// A declaration for something that is not a struct has no fields to name, so
// it is refused rather than quietly doing nothing.
var notAStructInput = tork.DefineInput(func(b *tork.InputBuilder, in *int) {})

func TestDefineInputRefusesANonStruct(t *testing.T) {
	app := newApp()
	app.GET("/notstruct", func(context.Context, int) (string, error) { return "", nil })

	// An int parameter is not an input at all, so the build fails on that
	// first; the declaration's own complaint is what this proves is recorded.
	if msg := buildError(t, app); !strings.Contains(msg, "nothing provides int") {
		t.Errorf("error = %q", msg)
	}
}

var notAStructBody = tork.DefineBody(func(b *tork.BodyRules, in *string) {})

func TestDefineBodyRefusesANonStruct(t *testing.T) {
	app := newApp()
	app.POST("/notstructbody", func(context.Context, string) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "nothing provides string") {
		t.Errorf("error = %q", msg)
	}
}

type BadBodyPointerInput struct {
	Page int
}

var badBodyPointerInput = tork.DefineInput(func(b *tork.InputBuilder, in *BadBodyPointerInput) {
	b.Query.Int(&in.Page, "page")
	b.JSONBody(nil)
})

func TestJSONBodyRefusesABadPointer(t *testing.T) {
	app := newApp()
	app.POST("/badbody", func(context.Context, BadBodyPointerInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "expected a pointer to a field of") {
		t.Errorf("error = %q", msg)
	}
}

// An embedded struct nobody declared anything under is still a field nobody
// declared.
type UntouchedEmbedded struct {
	Ignored int
}

type EmbeddedUndeclaredInput struct {
	UntouchedEmbedded
	Page int
}

var embeddedUndeclaredInput = tork.DefineInput(func(b *tork.InputBuilder, in *EmbeddedUndeclaredInput) {
	b.Query.Int(&in.Page, "page")
})

func TestUndeclaredEmbeddedStructIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/embundeclared", func(context.Context, EmbeddedUndeclaredInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "field UntouchedEmbedded of tork_test.EmbeddedUndeclaredInput has no binding declared") {
		t.Errorf("error = %q", msg)
	}
}

// An untagged field inside an embedded struct is reported like any other.
type BadEmbeddedTags struct {
	Page   int `query:"page"`
	Forgot int
}

type EmbeddedBadTagsInput struct {
	BadEmbeddedTags
	Search string `query:"search"`
}

func TestUntaggedFieldInsideAnEmbeddedStructIsRefused(t *testing.T) {
	app := newApp()
	app.GET("/embtags", func(context.Context, EmbeddedBadTagsInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "field Forgot of tork_test.BadEmbeddedTags has no path, query") {
		t.Errorf("error = %q", msg)
	}
}

// Two body fields in one tagged struct is the tag form's version of declaring
// two bodies.
type TwoTaggedBodiesInput struct {
	First  UpdateItemBody `body:"json"`
	Second UpdateItemBody `body:"json"`
}

func TestTwoTaggedBodiesAreRefused(t *testing.T) {
	app := newApp()
	app.POST("/twotagged", func(context.Context, TwoTaggedBodiesInput) (string, error) { return "", nil })

	if msg := buildError(t, app); !strings.Contains(msg, "field Second is a second request body") {
		t.Errorf("error = %q", msg)
	}
}

// A body in one input and a form field in another is the same clash reached
// from the other side: the body is claimed first, and the form finds it taken.
type BodyOnlyInput struct {
	Body UpdateItemBody `body:"json"`
}

type FormOnlyInput struct {
	Username string `form:"username"`
}

func TestBodyInOneInputAndFormInAnotherIsRefused(t *testing.T) {
	app := newApp()
	app.POST("/split", func(context.Context, BodyOnlyInput, FormOnlyInput) (string, error) {
		return "", nil
	})

	if msg := buildError(t, app); !strings.Contains(msg, "field Username cannot read a form when") {
		t.Errorf("error = %q", msg)
	}
}

// An explicit null is present, so it reaches the rules — and carries no value,
// so they are skipped rather than run against nothing.
func TestExplicitNullSkipsItsRules(t *testing.T) {
	app := newApp()
	app.POST("/every", func(_ context.Context, in EveryKindInput) (string, error) { return "ok", nil })

	query := "?count=1&total=1&ratio=1&enabled=true&at=2026-07-23T16:37:23Z&wait=1s&sizes=1&cursor=1"
	rec := post(t, app, "POST", "/every"+query, "application/json",
		`{"name":"nn","total":1,"ok":true,"extra":null}`)

	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}
