package tork_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/tork-go/web"
)

// bound builds a one-route application around an input struct and returns what
// the handler received, decoded back out of the response.
func bound[T any](t *testing.T, method, path, target string, body func() (string, string)) (T, *httptest.ResponseRecorder) {
	t.Helper()

	var got T
	app := newApp()
	app.Handle(method, path, func(_ context.Context, in T) (T, error) {
		got = in
		return in, nil
	})

	request := httptest.NewRequest(method, target, nil)
	if body != nil {
		contentType, content := body()
		request = httptest.NewRequest(method, target, strings.NewReader(content))
		request.Header.Set("Content-Type", contentType)
	}

	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, request)
	return got, rec
}

type PathInput struct {
	ItemID string `path:"item_id"`
}

func TestPathBinding(t *testing.T) {
	got, rec := bound[PathInput](t, "GET", "/items/{item_id}", "/items/42", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.ItemID != "42" {
		t.Errorf("ItemID = %q", got.ItemID)
	}
}

type QueryInput struct {
	Page    int           `query:"page" default:"1"`
	Limit   int           `query:"limit" default:"20"`
	Search  string        `query:"search"`
	Ratio   float64       `query:"ratio"`
	Active  bool          `query:"active"`
	Sort    []string      `query:"sort"`
	Tags    []string      `query:"tags,csv"`
	Cursor  *string       `query:"cursor"`
	After   time.Time     `query:"after"`
	Timeout time.Duration `query:"timeout"`
}

func TestQueryBinding(t *testing.T) {
	target := "/search?page=3&search=shoes&ratio=1.5&active=true" +
		"&sort=name&sort=-created&tags=new,sale&cursor=abc" +
		"&after=2026-07-23T16:37:23Z&timeout=1h30m"

	got, rec := bound[QueryInput](t, "GET", "/search", target, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	if got.Page != 3 {
		t.Errorf("Page = %d", got.Page)
	}
	if got.Limit != 20 {
		t.Errorf("Limit = %d, want the default", got.Limit)
	}
	if got.Search != "shoes" || got.Ratio != 1.5 || !got.Active {
		t.Errorf("scalars = %+v", got)
	}
	if !equalStrings(got.Sort, []string{"name", "-created"}) {
		t.Errorf("Sort = %v", got.Sort)
	}
	if !equalStrings(got.Tags, []string{"new", "sale"}) {
		t.Errorf("Tags = %v", got.Tags)
	}
	if got.Cursor == nil || *got.Cursor != "abc" {
		t.Errorf("Cursor = %v", got.Cursor)
	}
	if !got.After.Equal(time.Date(2026, 7, 23, 16, 37, 23, 0, time.UTC)) {
		t.Errorf("After = %v", got.After)
	}
	if got.Timeout != 90*time.Minute {
		t.Errorf("Timeout = %v", got.Timeout)
	}
}

// An absent parameter takes its default; one sent empty is treated as absent,
// so "?page=" and leaving page out mean the same thing.
func TestAbsentAndEmptyBothTakeTheDefault(t *testing.T) {
	for _, target := range []string{"/search", "/search?page=&limit="} {
		got, rec := bound[QueryInput](t, "GET", "/search", target, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s: status %d: %s", target, rec.Code, rec.Body)
		}
		if got.Page != 1 || got.Limit != 20 {
			t.Errorf("%s: Page = %d, Limit = %d", target, got.Page, got.Limit)
		}
	}
}

// A parameter with no default and no value is simply left zero.
func TestAbsentWithoutDefaultStaysZero(t *testing.T) {
	got, _ := bound[QueryInput](t, "GET", "/search", "/search", nil)
	if got.Search != "" || got.Cursor != nil || !got.After.IsZero() {
		t.Errorf("got %+v", got)
	}
}

// Something has to be chosen when a scalar arrives twice, and the last value
// is what a client overriding an earlier one would expect.
func TestRepeatedScalarKeepsTheLastValue(t *testing.T) {
	got, _ := bound[QueryInput](t, "GET", "/search", "/search?page=1&page=7", nil)
	if got.Page != 7 {
		t.Errorf("Page = %d", got.Page)
	}
}

type HeaderCookieInput struct {
	TraceID   string `header:"X-Trace-ID"`
	UserAgent string `header:"User-Agent"`
	Session   string `cookie:"session"`
	Theme     string `cookie:"theme"`
}

func TestHeaderAndCookieBinding(t *testing.T) {
	app := newApp()
	var got HeaderCookieInput
	app.GET("/", func(_ context.Context, in HeaderCookieInput) (HeaderCookieInput, error) {
		got = in
		return in, nil
	})

	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Set("X-Trace-ID", "trace-1")
	request.Header.Set("User-Agent", "tork-test")
	request.AddCookie(&http.Cookie{Name: "session", Value: "s3ss10n"})

	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, request)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.TraceID != "trace-1" || got.UserAgent != "tork-test" {
		t.Errorf("headers = %+v", got)
	}
	if got.Session != "s3ss10n" {
		t.Errorf("Session = %q", got.Session)
	}
	if got.Theme != "" {
		t.Errorf("a cookie that was not sent bound %q", got.Theme)
	}
}

type RepeatedHeaderInput struct {
	Forwarded []string `header:"X-Forwarded-For"`
	TraceID   string   `header:"x-trace-id"`
}

// HTTP header names are case-insensitive, so the tag may be spelled either way,
// and a header sent more than once fills a slice.
func TestHeadersAreCaseInsensitiveAndMayRepeat(t *testing.T) {
	app := newApp()
	var got RepeatedHeaderInput
	app.GET("/", func(_ context.Context, in RepeatedHeaderInput) (string, error) {
		got = in
		return "ok", nil
	})

	request := httptest.NewRequest("GET", "/", nil)
	request.Header.Add("X-Forwarded-For", "10.0.0.1")
	request.Header.Add("X-Forwarded-For", "10.0.0.2")
	request.Header.Set("X-Trace-ID", "trace-1")

	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, request)

	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if !equalStrings(got.Forwarded, []string{"10.0.0.1", "10.0.0.2"}) {
		t.Errorf("Forwarded = %v", got.Forwarded)
	}
	if got.TraceID != "trace-1" {
		t.Errorf("TraceID = %q, want the header matched despite the tag's case", got.TraceID)
	}
}

type DerivedNamesInput struct {
	Page     int    `query:""`
	PageSize int    `query:""`
	ItemID   string `path:""`
	Search   string `query:"q"`
}

func TestNamesDerivedFromTheFieldName(t *testing.T) {
	got, rec := bound[DerivedNamesInput](t, "GET", "/items/{itemId}",
		"/items/7?page=2&pageSize=50&q=boots", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Page != 2 || got.PageSize != 50 || got.ItemID != "7" || got.Search != "boots" {
		t.Errorf("got %+v", got)
	}
}

// A shared set of parameters can be embedded rather than repeated.
type Pagination struct {
	Page  int `query:"page" default:"1"`
	Limit int `query:"limit" default:"20"`
}

type EmbeddedInput struct {
	Pagination
	Search string `query:"search"`
}

func TestEmbeddedStructsAreWalked(t *testing.T) {
	got, rec := bound[EmbeddedInput](t, "GET", "/search", "/search?page=4&search=hat", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Page != 4 || got.Limit != 20 || got.Search != "hat" {
		t.Errorf("got %+v", got)
	}
}

// A type with its own textual form binds without the framework knowing it.
type SKU struct{ code string }

func (s *SKU) UnmarshalText(text []byte) error {
	if !strings.HasPrefix(string(text), "sku_") {
		return fmt.Errorf("a SKU begins with sku_")
	}
	s.code = string(text)
	return nil
}

type TextInput struct {
	SKU SKU `query:"sku"`
}

func TestTextUnmarshalerBinding(t *testing.T) {
	_, rec := bound[TextInput](t, "GET", "/", "/?sku=sku_1", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	_, bad := bound[TextInput](t, "GET", "/", "/?sku=nope", nil)
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("status %d", bad.Code)
	}
	details := decodeFieldErrors(t, bad)
	if details[0].Field != "query.sku" || details[0].Issue != tork.IssueInvalidFormat {
		t.Errorf("field error = %+v", details[0])
	}
}

func TestUnreadableValuesAreReportedPerField(t *testing.T) {
	tests := []struct {
		query     string
		wantField string
		wantIssue string
		wantMsg   string
	}{
		{"page=abc", "query.page", tork.IssueInvalidInteger, "page must be a whole number."},
		{"ratio=abc", "query.ratio", tork.IssueInvalidNumber, "ratio must be a number."},
		{"active=maybe", "query.active", tork.IssueInvalidBoolean, "active must be true or false."},
		{"after=yesterday", "query.after", tork.IssueInvalidDateTime, "after must be an RFC 3339 timestamp."},
		{"timeout=soon", "query.timeout", tork.IssueInvalidDuration, "timeout must be a duration such as 1h30m."},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			_, rec := bound[QueryInput](t, "GET", "/search", "/search?"+tt.query, nil)
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

// Every field is attempted, so one round trip reports everything wrong.
func TestEveryBadFieldIsReportedAtOnce(t *testing.T) {
	_, rec := bound[QueryInput](t, "GET", "/search", "/search?page=abc&ratio=xyz&active=maybe", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}

	details := decodeFieldErrors(t, rec)
	if len(details) != 3 {
		t.Fatalf("details = %+v", details)
	}
	e := decodeError(t, rec)
	if e.Message != "Validation failed for 3 fields." {
		t.Errorf("message = %q", e.Message)
	}
}

// Problems found in different input structs still make one answer.
type FirstInput struct {
	Page int `query:"page"`
}

type SecondInput struct {
	Limit int `query:"limit"`
}

func TestProblemsFromSeveralInputsAreJoined(t *testing.T) {
	app := newApp()
	app.GET("/", func(_ context.Context, a FirstInput, b SecondInput) (int, error) {
		return a.Page + b.Limit, nil
	})

	rec := do(t, app, "GET", "/?page=abc&limit=xyz", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	details := decodeFieldErrors(t, rec)
	if len(details) != 2 {
		t.Fatalf("details = %+v", details)
	}
	if details[0].Field != "query.page" || details[1].Field != "query.limit" {
		t.Errorf("fields = %s, %s", details[0].Field, details[1].Field)
	}
}

// An out-of-range value is refused rather than silently truncated.
type NarrowInput struct {
	Small int8   `query:"small"`
	Count uint16 `query:"count"`
}

func TestValuesAreParsedWithinTheFieldsWidth(t *testing.T) {
	_, rec := bound[NarrowInput](t, "GET", "/", "/?small=999", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}

	_, negative := bound[NarrowInput](t, "GET", "/", "/?count=-1", nil)
	details := decodeFieldErrors(t, negative)
	if details[0].Message != "count must be a whole number that is not negative." {
		t.Errorf("message = %q", details[0].Message)
	}
}

type BoolFormsInput struct {
	Flag bool `query:"flag"`
}

// Checkboxes send "on", which strconv.ParseBool does not take.
func TestBooleanSpellings(t *testing.T) {
	tests := map[string]bool{
		"true": true, "1": true, "yes": true, "on": true, "ON": true,
		"false": false, "0": false, "no": false, "off": false,
	}

	for raw, want := range tests {
		t.Run(raw, func(t *testing.T) {
			got, rec := bound[BoolFormsInput](t, "GET", "/", "/?flag="+raw, nil)
			if rec.Code != http.StatusOK {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}
			if got.Flag != want {
				t.Errorf("flag = %v, want %v", got.Flag, want)
			}
		})
	}
}

// decodeFieldErrors reads the details of a validation failure.
func decodeFieldErrors(t *testing.T, rec *httptest.ResponseRecorder) []tork.FieldError {
	t.Helper()

	var envelope struct {
		Details []tork.FieldError `json:"details"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode %s: %v", rec.Body, err)
	}
	return envelope.Details
}
