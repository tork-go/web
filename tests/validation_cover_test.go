package tork_test

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/tork-go/web"
)

// The Optional forms of every kind, on both a parameter and a body field, so
// that each is known to bind and to be judged rather than only to compile.
type OptionalKindsInput struct {
	Total  tork.Optional[int64]
	Ratio  tork.Optional[float64]
	Active tork.Optional[bool]
	At     tork.Optional[time.Time]
	Body   OptionalKindsBody
}

type OptionalKindsBody struct {
	Total  tork.Optional[int64]     `json:"total,omitzero"`
	Ratio  tork.Optional[float64]   `json:"ratio,omitzero"`
	Active tork.Optional[bool]      `json:"active,omitzero"`
	At     tork.Optional[time.Time] `json:"at,omitzero"`
	Wait   time.Duration            `json:"wait"`
	Sizes  []int                    `json:"sizes"`
}

var optionalKindsBody = tork.DefineBody(func(b *tork.BodyRules, in *OptionalKindsBody) {
	b.OptionalInt64(&in.Total).Min(1)
	b.OptionalFloat64(&in.Ratio).Max(10)
	b.OptionalBool(&in.Active).MustBe(true)
	b.OptionalTime(&in.At).Before(farFuture)
	b.Duration(&in.Wait).Max(time.Hour)
	b.Ints(&in.Sizes).MaxItems(2)
})

var optionalKindsInput = tork.DefineInput(func(b *tork.InputBuilder, in *OptionalKindsInput) {
	b.Query.OptionalInt64(&in.Total, "total").Min(1)
	b.Query.OptionalFloat64(&in.Ratio, "ratio").Max(10)
	b.Query.OptionalBool(&in.Active, "active").MustBe(true)
	b.Query.OptionalTime(&in.At, "at").Before(farFuture)
	b.JSONBody(&in.Body)
})

func TestOptionalKindsBindAndAreJudged(t *testing.T) {
	var got OptionalKindsInput
	app := newApp()
	app.POST("/optional", func(_ context.Context, in OptionalKindsInput) (string, error) {
		got = in
		return "ok", nil
	})

	rec := post(t, app, "POST", "/optional?total=5&ratio=1.5&active=true&at=2026-07-23T16:37:23Z",
		"application/json", `{"total":2,"ratio":2,"active":true,"at":"2026-07-23T16:37:23Z","wait":60,"sizes":[1]}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	if total, ok := got.Total.Get(); !ok || total != 5 {
		t.Errorf("Total = %d, ok = %v", total, ok)
	}
	if ratio, ok := got.Ratio.Get(); !ok || ratio != 1.5 {
		t.Errorf("Ratio = %v, ok = %v", ratio, ok)
	}
	if active, ok := got.Active.Get(); !ok || !active {
		t.Errorf("Active = %v, ok = %v", active, ok)
	}
	if _, ok := got.At.Get(); !ok {
		t.Error("At was not set")
	}
}

func TestOptionalKindRulesAreEnforced(t *testing.T) {
	app := newApp()
	app.POST("/optional", func(_ context.Context, in OptionalKindsInput) (string, error) { return "ok", nil })

	body := `{"total":2,"ratio":2,"active":true,"at":"2026-07-23T16:37:23Z","wait":60,"sizes":[1]}`
	tests := []struct {
		query string
		issue string
	}{
		{"total=0", tork.IssueMinimumNotMet},
		{"ratio=99", tork.IssueMaximumExceeded},
		{"active=false", tork.IssueNotInSet},
		{"at=2200-01-01T00:00:00Z", tork.IssueTooLate},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := post(t, app, "POST", "/optional?"+tt.query, "application/json", body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if details := decodeFieldErrors(t, rec); details[0].Issue != tt.issue {
				t.Errorf("field error = %+v", details[0])
			}
		})
	}
}

func TestBodyOptionalAndListRules(t *testing.T) {
	app := newApp()
	app.POST("/optional", func(_ context.Context, in OptionalKindsInput) (string, error) { return "ok", nil })

	tests := []struct {
		name  string
		body  string
		field string
		issue string
	}{
		{"int64", `{"total":0}`, "total", tork.IssueMinimumNotMet},
		{"float", `{"ratio":99}`, "ratio", tork.IssueMaximumExceeded},
		{"bool", `{"active":false}`, "active", tork.IssueNotInSet},
		{"time", `{"at":"2200-01-01T00:00:00Z"}`, "at", tork.IssueTooLate},
		{"duration", `{"wait":7200000000000}`, "wait", tork.IssueMaximumExceeded},
		{"ints", `{"sizes":[1,2,3]}`, "sizes", tork.IssueTooManyItems},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, app, "POST", "/optional", "application/json", tt.body)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}

			details := decodeFieldErrors(t, rec)
			if details[0].Field != tt.field || details[0].Issue != tt.issue {
				t.Errorf("field error = %+v", details[0])
			}
		})
	}
}

// An Optional that was not sent is not judged, so a rule it would fail passes
// simply by being unreached.
func TestUnsentOptionalIsNotTransformedOrJudged(t *testing.T) {
	app := newApp()
	app.POST("/optional", func(_ context.Context, in OptionalKindsInput) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/optional", "application/json", `{}`)
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// A transform declared on an Optional runs only when there is a value to
// change.
type TrimmedOptionalBody struct {
	tork.JSONBody
	Name tork.Optional[string] `json:"name,omitzero"`
}

var trimmedOptionalBody = tork.DefineBody(func(b *tork.BodyRules, in *TrimmedOptionalBody) {
	b.OptionalString(&in.Name).Trim().MaxLen(3)
})

func TestTransformOnAnOptional(t *testing.T) {
	var got TrimmedOptionalBody
	app := newApp()
	app.POST("/trimmedopt", func(_ context.Context, body TrimmedOptionalBody) (string, error) {
		got = body
		return "ok", nil
	})

	rec := post(t, app, "POST", "/trimmedopt", "application/json", `{"name":"  ada  "}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if name, _ := got.Name.Get(); name != "ada" {
		t.Errorf("Name = %q", name)
	}

	if rec := post(t, app, "POST", "/trimmedopt", "application/json", `{}`); rec.Code != http.StatusOK {
		t.Errorf("an unsent Optional was transformed: %d", rec.Code)
	}
}

// A required string list, which nothing else reaches.
type RequiredListInput struct {
	Tags []string
}

var requiredListInput = tork.DefineInput(func(b *tork.InputBuilder, in *RequiredListInput) {
	b.Query.Strings(&in.Tags, "tags").Required().MaxItems(2)
})

func TestRequiredStringList(t *testing.T) {
	app := newApp()
	app.GET("/reqlist", func(_ context.Context, in RequiredListInput) (string, error) { return "ok", nil })

	rec := do(t, app, "GET", "/reqlist", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if details := decodeFieldErrors(t, rec); details[0].Issue != tork.IssueFieldRequired {
		t.Errorf("field error = %+v", details[0])
	}

	if rec := do(t, app, "GET", "/reqlist?tags=a&tags=b&tags=c", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("three tags were accepted: %d", rec.Code)
	}
}

// A hostname is a series of labels, each with its own limits.
type HostInput struct {
	Host string
}

var hostInput = tork.DefineInput(func(b *tork.InputBuilder, in *HostInput) {
	b.Query.String(&in.Host, "host").Hostname()
})

func TestHostnameLabels(t *testing.T) {
	app := newApp()
	app.GET("/host", func(_ context.Context, in HostInput) (string, error) { return "ok", nil })

	long := "a"
	for range 7 {
		long += long
	}

	refused := []string{
		long, // one label over 63 characters
		long + "." + long + "." + long + "." + long, // over 253 in total
		"api_v1.example.com",                        // underscore is not a hostname character
		"trailing-.example.com",                     // a label may not end with a hyphen
	}
	for _, host := range refused {
		t.Run("refused", func(t *testing.T) {
			if rec := do(t, app, "GET", "/host?host="+host, nil); rec.Code != http.StatusBadRequest {
				t.Errorf("%q was accepted", host)
			}
		})
	}

	// A trailing dot is how a fully qualified name is written.
	if rec := do(t, app, "GET", "/host?host=api.example.com.", nil); rec.Code != http.StatusOK {
		t.Errorf("a fully qualified name was refused: %d", rec.Code)
	}
}
