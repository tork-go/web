package tork_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tork-go/web"
)

// The struct is plain data. Everything about how it is read is code.
type SearchInput struct {
	Page   int
	Limit  int
	Search string
	Sort   []string
	Token  string
	Since  time.Time
	Cursor tork.Optional[string]
}

var launched = time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)

var searchInput = tork.DefineInput(func(b *tork.InputBuilder, in *SearchInput) {
	b.Query.Int(&in.Page, "page").Default(1).Min(1)
	b.Query.Int(&in.Limit, "limit").Default(20).Range(1, 100)
	b.Query.String(&in.Search, "search").MaxLen(20)
	b.Query.Strings(&in.Sort, "sort").MaxItems(2).OneOf("name", "-created")
	b.Query.Time(&in.Since, "since").After(launched)
	b.Query.OptionalString(&in.Cursor, "cursor").MinLen(3)
	b.Header.String(&in.Token, "X-Token").Required()
})

// search builds the one-route application these tests share.
func search(t *testing.T) *tork.App {
	t.Helper()

	app := newApp()
	app.GET("/search", func(_ context.Context, in SearchInput) (SearchInput, error) {
		return in, nil
	})
	return app
}

// searchOK sends a request that satisfies everything except what the test
// changes, so a failure is always about the one thing under test.
func searchOK(t *testing.T, query string) *httptest.ResponseRecorder {
	t.Helper()

	request := httptest.NewRequest("GET", "/search?"+query, nil)
	request.Header.Set("X-Token", "t0ken")
	rec := httptest.NewRecorder()
	handlerOf(t, search(t)).ServeHTTP(rec, request)
	return rec
}

func TestBuilderBindsEveryField(t *testing.T) {
	var got SearchInput
	app := newApp()
	app.GET("/search", func(_ context.Context, in SearchInput) (string, error) {
		got = in
		return "ok", nil
	})

	request := httptest.NewRequest("GET",
		"/search?page=3&limit=50&search=boots&sort=name&since=2026-07-23T16:37:23Z&cursor=abcd", nil)
	request.Header.Set("X-Token", "t0ken")
	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, request)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Page != 3 || got.Limit != 50 || got.Search != "boots" || got.Token != "t0ken" {
		t.Errorf("got %+v", got)
	}
	if !equalStrings(got.Sort, []string{"name"}) {
		t.Errorf("Sort = %v", got.Sort)
	}
	if cursor, ok := got.Cursor.Get(); !ok || cursor != "abcd" {
		t.Errorf("Cursor = %q, ok = %v", cursor, ok)
	}
}

func TestBuilderDefaultsApply(t *testing.T) {
	rec := searchOK(t, "")
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got SearchInput
	decodeJSON(t, rec, &got)
	if got.Page != 1 || got.Limit != 20 {
		t.Errorf("Page = %d, Limit = %d", got.Page, got.Limit)
	}
}

func TestBuilderRules(t *testing.T) {
	tests := []struct {
		query     string
		wantField string
		wantIssue string
		wantMsg   string
	}{
		{"page=0", "query.page", tork.IssueMinimumNotMet, "page must be at least 1."},
		{"limit=0", "query.limit", tork.IssueMinimumNotMet, "limit must be at least 1."},
		{"limit=101", "query.limit", tork.IssueMaximumExceeded, "limit must be at most 100."},
		{"search=" + strings.Repeat("x", 21), "query.search", tork.IssueTooLong, "search must be at most 20 characters."},
		{"sort=name&sort=-created&sort=name", "query.sort", tork.IssueTooManyItems, "sort must have at most 2 values."},
		{"sort=colour", "query.sort", tork.IssueNotInSet, "sort must contain only name, -created."},
		{"since=2019-01-01T00:00:00Z", "query.since", tork.IssueTooEarly, "since must be after 2020-01-01T00:00:00Z."},
		{"cursor=ab", "query.cursor", tork.IssueTooShort, "cursor must be at least 3 characters."},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := searchOK(t, tt.query)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}

			details := decodeFieldErrors(t, rec)
			if len(details) != 1 {
				t.Fatalf("details = %+v", details)
			}
			if details[0].Field != tt.wantField || details[0].Issue != tt.wantIssue || details[0].Message != tt.wantMsg {
				t.Errorf("field error = %+v", details[0])
			}
		})
	}
}

// A field the request did not carry is not judged by rules it never reached.
func TestRulesAreSkippedForAbsentValues(t *testing.T) {
	rec := searchOK(t, "")
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// A default is the author's own value, so it is not put through the rules the
// client's values are judged by.
func TestDefaultsAreNotValidated(t *testing.T) {
	type OddInput struct {
		Page int
	}
	var _ = tork.DefineInput(func(b *tork.InputBuilder, in *OddInput) {
		b.Query.Int(&in.Page, "page").Default(0).Min(1)
	})

	app := newApp()
	app.GET("/odd", func(_ context.Context, in OddInput) (int, error) { return in.Page, nil })

	rec := do(t, app, "GET", "/odd", nil)
	if rec.Code != http.StatusOK || rec.Body.String() != "0" {
		t.Errorf("status %d body %s", rec.Code, rec.Body)
	}
}

func TestRequiredParameter(t *testing.T) {
	rec := httptest.NewRecorder()
	handlerOf(t, search(t)).ServeHTTP(rec, httptest.NewRequest("GET", "/search", nil))

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	details := decodeFieldErrors(t, rec)
	if details[0].Field != "header.X-Token" || details[0].Issue != tork.IssueFieldRequired {
		t.Errorf("field error = %+v", details[0])
	}
	if details[0].Message != "X-Token is required." {
		t.Errorf("message = %q", details[0].Message)
	}
}

// Every rule that fails is reported, as with every field that fails to bind.
func TestEveryBrokenRuleIsReported(t *testing.T) {
	rec := searchOK(t, "page=0&limit=999&search="+strings.Repeat("x", 30))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if details := decodeFieldErrors(t, rec); len(details) != 3 {
		t.Errorf("details = %+v", details)
	}
}

// --------------------------------------------------------------- formats

type AccountInput struct {
	Email   string
	ID      string
	Website string
	Slug    string
	Role    string
	Size    float64
	Step    int
	Window  time.Duration
	Active  bool
	Tags    []string
}

var accountInput = tork.DefineInput(func(b *tork.InputBuilder, in *AccountInput) {
	b.Query.String(&in.Email, "email").Email()
	b.Query.String(&in.ID, "id").UUID()
	b.Query.String(&in.Website, "website").URL()
	b.Query.String(&in.Slug, "slug").Pattern(`^[a-z-]+$`).Len(5)
	b.Query.String(&in.Role, "role").OneOf("admin", "user")
	b.Query.Float64(&in.Size, "size").Range(0.5, 9.5)
	b.Query.Int(&in.Step, "step").MultipleOf(5).OneOf(5, 10, 15)
	b.Query.Duration(&in.Window, "window").Min(time.Minute).Max(time.Hour)
	b.Query.Bool(&in.Active, "active").Default(true)
	b.Query.Strings(&in.Tags, "tags").CSV().Unique().MinItems(1)
})

func TestFormatRules(t *testing.T) {
	app := newApp()
	app.GET("/accounts", func(_ context.Context, in AccountInput) (string, error) { return "ok", nil })

	tests := []struct {
		query string
		issue string
	}{
		{"email=nope", tork.IssueInvalidEmail},
		{"email=a@b", tork.IssueInvalidEmail},
		{"id=not-a-uuid", tork.IssueInvalidUUID},
		{"website=/relative", tork.IssueInvalidURL},
		{"slug=NOPE!", tork.IssuePatternMismatch},
		{"role=owner", tork.IssueNotInSet},
		{"size=99", tork.IssueMaximumExceeded},
		{"size=0.1", tork.IssueMinimumNotMet},
		{"step=7", tork.IssueNotMultipleOf},
		{"window=1s", tork.IssueMinimumNotMet},
		{"window=2h", tork.IssueMaximumExceeded},
		{"tags=a,a", tork.IssueDuplicateItems},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := do(t, app, "GET", "/accounts?"+tt.query, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if details := decodeFieldErrors(t, rec); details[0].Issue != tt.issue {
				t.Errorf("field error = %+v", details[0])
			}
		})
	}
}

func TestValuesThatPassEveryFormatRule(t *testing.T) {
	app := newApp()
	app.GET("/accounts", func(_ context.Context, in AccountInput) (AccountInput, error) { return in, nil })

	rec := do(t, app, "GET", "/accounts?email=ada@example.com"+
		"&id=123e4567-e89b-12d3-a456-426614174000"+
		"&website=https://example.com/x&slug=hello&role=admin"+
		"&size=1.5&step=10&window=30m&tags=a,b", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	var got AccountInput
	decodeJSON(t, rec, &got)
	if !got.Active {
		t.Error("Active should have taken its default")
	}
	if !equalStrings(got.Tags, []string{"a", "b"}) {
		t.Errorf("Tags = %v", got.Tags)
	}
}

// --------------------------------------------------------------- bodies

type NewAccountBody struct {
	tork.JSONBody
	Name  string                `json:"name"`
	Email string                `json:"email"`
	Age   int                   `json:"age"`
	Tags  []string              `json:"tags"`
	Bio   tork.Optional[string] `json:"bio,omitzero"`
}

var newAccountBody = tork.DefineBody(func(b *tork.BodyRules, in *NewAccountBody) {
	b.String(&in.Name).Required().Range(2, 20)
	b.String(&in.Email).Required().Email()
	b.Int(&in.Age).Range(18, 120)
	b.Strings(&in.Tags).MaxItems(2)
	b.OptionalString(&in.Bio).MaxLen(10)
})

func TestBodyRules(t *testing.T) {
	app := newApp()
	app.POST("/accounts", func(_ context.Context, body NewAccountBody) (string, error) { return "ok", nil })

	tests := []struct {
		name  string
		body  string
		field string
		issue string
	}{
		{"required missing", `{"email":"a@b.com"}`, "name", tork.IssueFieldRequired},
		{"too short", `{"name":"a","email":"a@b.com"}`, "name", tork.IssueTooShort},
		{"bad email", `{"name":"Ada","email":"nope"}`, "email", tork.IssueInvalidEmail},
		{"under minimum", `{"name":"Ada","email":"a@b.com","age":3}`, "age", tork.IssueMinimumNotMet},
		{"too many items", `{"name":"Ada","email":"a@b.com","tags":["a","b","c"]}`, "tags", tork.IssueTooManyItems},
		{"optional too long", `{"name":"Ada","email":"a@b.com","bio":"way too long to fit"}`, "bio", tork.IssueTooLong},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := post(t, app, "POST", "/accounts", "application/json", tt.body)
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

func TestBodyRulesPassing(t *testing.T) {
	app := newApp()
	app.POST("/accounts", func(_ context.Context, body NewAccountBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/accounts", "application/json",
		`{"name":"Ada","email":"ada@example.com","age":36,"tags":["a"]}`)
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// A body whose rules were declared applies them wherever the type is used,
// including as a field of an input struct.
type WrappedAccountInput struct {
	AccountID string         `path:"account_id"`
	Body      NewAccountBody `body:"json"`
}

func TestBodyRulesApplyThroughAWrapper(t *testing.T) {
	app := newApp()
	app.PUT("/accounts/{account_id}", func(_ context.Context, in WrappedAccountInput) (string, error) {
		return "ok", nil
	})

	rec := post(t, app, "PUT", "/accounts/7", "application/json", `{"name":"a","email":"nope"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if details := decodeFieldErrors(t, rec); len(details) != 2 {
		t.Errorf("details = %+v", details)
	}
}

// An unset Optional is absent, so its rules are not reached.
func TestOptionalBodyFieldLeftOutIsNotChecked(t *testing.T) {
	app := newApp()
	app.POST("/accounts", func(_ context.Context, body NewAccountBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/accounts", "application/json",
		`{"name":"Ada","email":"ada@example.com"}`)
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// Parameters and body fields are judged in the same pass.
func TestParameterAndBodyRulesAreReportedTogether(t *testing.T) {
	app := newApp()
	app.PUT("/accounts/{account_id}", func(_ context.Context, in WrappedAccountInput) (string, error) {
		return "ok", nil
	})

	rec := post(t, app, "PUT", "/accounts/7", "application/json", `{"name":"a","email":"a@b.com"}`)
	details := decodeFieldErrors(t, rec)
	if len(details) != 1 || details[0].Field != "name" {
		t.Errorf("details = %+v", details)
	}
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder, into any) {
	t.Helper()
	if err := json.Unmarshal(rec.Body.Bytes(), into); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
}
