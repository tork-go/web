package tork_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"

	"github.com/tork-go/web"
)

type PatchItemBody struct {
	tork.JSONBody
	Name  tork.Optional[string] `json:"name,omitzero"`
	Price tork.Optional[int64]  `json:"price,omitzero"`
}

// The three states a PATCH has to tell apart.
func TestOptionalDistinguishesAbsentNullAndValue(t *testing.T) {
	var got PatchItemBody
	app := newApp()
	app.PATCH("/items/{item_id}", func(_ context.Context, body PatchItemBody) (string, error) {
		got = body
		return "ok", nil
	})

	rec := post(t, app, "PATCH", "/items/42", "application/json", `{"name":"Boots","price":null}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	name, ok := got.Name.Get()
	if !ok || name != "Boots" {
		t.Errorf("Name = %q, ok = %v", name, ok)
	}
	if !got.Name.IsSet() || got.Name.IsNull() {
		t.Errorf("Name: set = %v, null = %v", got.Name.IsSet(), got.Name.IsNull())
	}

	if !got.Price.IsSet() || !got.Price.IsNull() {
		t.Errorf("Price: set = %v, null = %v", got.Price.IsSet(), got.Price.IsNull())
	}
	if _, ok := got.Price.Get(); ok {
		t.Error("an explicit null should not read as a value")
	}
}

func TestOptionalLeftOutIsAbsent(t *testing.T) {
	var got PatchItemBody
	app := newApp()
	app.PATCH("/items/{item_id}", func(_ context.Context, body PatchItemBody) (string, error) {
		got = body
		return "ok", nil
	})

	post(t, app, "PATCH", "/items/42", "application/json", `{}`)

	if got.Name.IsSet() || got.Name.IsNull() {
		t.Errorf("Name: set = %v, null = %v", got.Name.IsSet(), got.Name.IsNull())
	}
	if !got.Name.IsZero() {
		t.Error("an absent Optional should be zero")
	}
}

type OptionalQueryInput struct {
	Search tork.Optional[string] `query:"search"`
	Page   tork.Optional[int]    `query:"page"`
}

// A query string has no null, so an Optional there tells present from absent.
func TestOptionalInAQueryString(t *testing.T) {
	got, rec := bound[OptionalQueryInput](t, "GET", "/", "/?search=boots", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	if search, ok := got.Search.Get(); !ok || search != "boots" {
		t.Errorf("Search = %q, ok = %v", search, ok)
	}
	if got.Page.IsSet() {
		t.Error("Page was not sent and should not be set")
	}
	if page := got.Page.Or(10); page != 10 {
		t.Errorf("Or = %d", page)
	}
}

func TestOptionalReportsAnUnreadableValue(t *testing.T) {
	_, rec := bound[OptionalQueryInput](t, "GET", "/", "/?page=abc", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}
	if details := decodeFieldErrors(t, rec); details[0].Issue != tork.IssueInvalidInteger {
		t.Errorf("field error = %+v", details[0])
	}
}

// omitzero is what makes an untouched Optional disappear from a response while
// an explicit null still writes one.
func TestOptionalInAResponse(t *testing.T) {
	tests := []struct {
		name string
		body PatchItemBody
		want string
	}{
		{"absent", PatchItemBody{}, `{}`},
		{"value", PatchItemBody{Name: tork.Some("Boots")}, `{"name":"Boots"}`},
		{"null", PatchItemBody{Name: tork.Null[string]()}, `{"name":null}`},
		{
			"both",
			PatchItemBody{Name: tork.Some("Boots"), Price: tork.Null[int64]()},
			`{"name":"Boots","price":null}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encoded, err := json.Marshal(tt.body)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(encoded) != tt.want {
				t.Errorf("encoded = %s, want %s", encoded, tt.want)
			}
		})
	}
}

// Without omitzero the field is written, and an absent Optional has to be
// something; null is the only honest answer.
func TestOptionalWithoutOmitzeroWritesNull(t *testing.T) {
	type response struct {
		Name tork.Optional[string] `json:"name"`
	}

	encoded, err := json.Marshal(response{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if string(encoded) != `{"name":null}` {
		t.Errorf("encoded = %s", encoded)
	}
}

func TestOptionalRejectsAValueOfTheWrongType(t *testing.T) {
	var body PatchItemBody
	err := json.Unmarshal([]byte(`{"price":"free"}`), &body)
	if err == nil {
		t.Error("a string should not decode into an Optional[int64]")
	}
}
