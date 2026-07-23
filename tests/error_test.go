package tork_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/tork-go/web"
)

// failing builds an application whose one route returns err.
func failing(err error, opts ...tork.Option) *tork.App {
	app := newApp(opts...)
	app.GET("/api/v1/orders/{order_id}", func(context.Context) (greeting, error) {
		return greeting{}, err
	})
	return app
}

// The whole envelope, compared as JSON, because the shape is the contract.
func TestNotFoundEnvelope(t *testing.T) {
	app := failing(tork.NotFound("Order", "ord_98765"))

	rec := do(t, app, "GET", "/api/v1/orders/ord_98765", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}

	want := `{
	  "code": "RESOURCE_NOT_FOUND",
	  "message": "No order found with ID 'ord_98765'.",
	  "status": 404,
	  "timestamp": "2026-07-23T16:37:23Z",
	  "path": "/api/v1/orders/ord_98765",
	  "details": {"resource": "Order", "identifier": "ord_98765"}
	}`
	assertJSON(t, rec.Body.Bytes(), want)
}

func TestValidationEnvelopeCarriesEveryField(t *testing.T) {
	app := failing(tork.ValidationFailed(
		tork.FieldError{Field: "cardNumber", Issue: "card_number_invalid", Message: "Card number must be 16 digits long."},
		tork.FieldError{Field: "zipCode", Issue: "field_required", Message: "Zip code is required for billing."},
	))

	rec := do(t, app, "GET", "/api/v1/orders/x", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d", rec.Code)
	}

	want := `{
	  "code": "VALIDATION_ERROR",
	  "message": "Validation failed for 2 fields.",
	  "status": 400,
	  "timestamp": "2026-07-23T16:37:23Z",
	  "path": "/api/v1/orders/x",
	  "details": [
	    {"field": "cardNumber", "issue": "card_number_invalid", "message": "Card number must be 16 digits long."},
	    {"field": "zipCode", "issue": "field_required", "message": "Zip code is required for billing."}
	  ]
	}`
	assertJSON(t, rec.Body.Bytes(), want)
}

func TestValidationMessageAgreesWithTheFieldCount(t *testing.T) {
	one := tork.ValidationFailed(tork.FieldError{Field: "zipCode"})
	if one.Message != "Validation failed for 1 field." {
		t.Errorf("message = %q", one.Message)
	}
	none := tork.ValidationFailed()
	if none.Message != "Validation failed for 0 fields." {
		t.Errorf("message = %q", none.Message)
	}
}

func TestRateLimitEnvelope(t *testing.T) {
	resetAt := time.Date(2026, 7, 23, 16, 38, 5, 0, time.UTC)
	app := failing(tork.RateLimited(
		"You have exceeded the rate limit of 100 requests per minute.",
		tork.RateLimitDetails{
			Limit:             100,
			Window:            "1m",
			RetryAfterSeconds: 42,
			ResetAt:           tork.Timestamp(resetAt),
		},
	))

	rec := do(t, app, "GET", "/api/v1/orders/x", nil)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d", rec.Code)
	}

	want := `{
	  "code": "RATE_LIMIT_EXCEEDED",
	  "message": "You have exceeded the rate limit of 100 requests per minute.",
	  "status": 429,
	  "timestamp": "2026-07-23T16:37:23Z",
	  "path": "/api/v1/orders/x",
	  "details": {
	    "limit": 100,
	    "window": "1m",
	    "retryAfterSeconds": 42,
	    "resetAt": "2026-07-23T16:38:05Z"
	  }
	}`
	assertJSON(t, rec.Body.Bytes(), want)
}

func TestNamedConstructors(t *testing.T) {
	tests := []struct {
		err        *tork.Error
		wantStatus int
		wantCode   string
	}{
		{tork.BadRequest("no"), 400, "BAD_REQUEST"},
		{tork.Unauthorized("no"), 401, "UNAUTHORIZED"},
		{tork.Forbidden("no"), 403, "FORBIDDEN"},
		{tork.Conflict("no"), 409, "CONFLICT"},
		{tork.NotFound("Order", "1"), 404, "RESOURCE_NOT_FOUND"},
		{tork.Internal(), 500, "INTERNAL_ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.wantCode, func(t *testing.T) {
			if tt.err.Status != tt.wantStatus || tt.err.Code != tt.wantCode {
				t.Errorf("got %d/%s", tt.err.Status, tt.err.Code)
			}
			rec := do(t, failing(tt.err), "GET", "/api/v1/orders/x", nil)
			if rec.Code != tt.wantStatus {
				t.Errorf("served status = %d", rec.Code)
			}
		})
	}
}

func TestInternalErrorSaysNothingUseful(t *testing.T) {
	if got := tork.Internal().Message; got != "An unexpected error occurred." {
		t.Errorf("message = %q", got)
	}
}

func TestErrorImplementsError(t *testing.T) {
	err := tork.NewError(404, "GONE", "it is gone")
	if got := err.Error(); got != "GONE: it is gone" {
		t.Errorf("Error() = %q", got)
	}
}

func TestWithDetailsAndWithMessage(t *testing.T) {
	err := tork.NewError(418, "TEAPOT", "original").
		WithMessage("replaced").
		WithDetails(map[string]any{"brewing": false})

	if err.Message != "replaced" {
		t.Errorf("message = %q", err.Message)
	}
	if err.Details.(map[string]any)["brewing"] != false {
		t.Errorf("details = %v", err.Details)
	}
}

// An error that says nothing still has to serve as something coherent.
func TestEmptyErrorIsCompleted(t *testing.T) {
	rec := do(t, failing(&tork.Error{}), "GET", "/api/v1/orders/x", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	e := decodeError(t, rec)
	if e.Code != "INTERNAL_ERROR" || e.Message != "Internal Server Error" || e.Status != 500 {
		t.Errorf("envelope = %+v", e)
	}
}

func TestCodeIsDerivedFromTheStatusWhenUnset(t *testing.T) {
	tests := []struct {
		status int
		want   string
	}{
		{http.StatusUnprocessableEntity, "UNPROCESSABLE_ENTITY"},
		{http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE"},
		{http.StatusTeapot, "IM_A_TEAPOT"},
		{http.StatusNonAuthoritativeInfo, "NON_AUTHORITATIVE_INFORMATION"},
		{599, "ERROR"},
	}

	for _, tt := range tests {
		t.Run(tt.want, func(t *testing.T) {
			rec := do(t, failing(tork.NewError(tt.status, "", "why")), "GET", "/api/v1/orders/x", nil)
			if e := decodeError(t, rec); e.Code != tt.want {
				t.Errorf("code = %q, want %q", e.Code, tt.want)
			}
		})
	}
}

// A domain error type answers for itself, at any depth, because the framework
// unwraps to find it.
type outOfStockError struct{ SKU string }

func (e outOfStockError) Error() string { return "out of stock: " + e.SKU }

func (e outOfStockError) HTTPError() tork.Error {
	return *tork.Conflict("That item is out of stock.").
		WithDetails(map[string]string{"sku": e.SKU})
}

func TestHTTPErrorAnswersForItself(t *testing.T) {
	rec := do(t, failing(outOfStockError{SKU: "abc"}), "GET", "/api/v1/orders/x", nil)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != "CONFLICT" {
		t.Errorf("code = %q", e.Code)
	}
}

func TestWrappedHTTPErrorIsStillFound(t *testing.T) {
	wrapped := fmt.Errorf("loading the basket: %w", outOfStockError{SKU: "abc"})

	rec := do(t, failing(wrapped), "GET", "/api/v1/orders/x", nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("status = %d", rec.Code)
	}
}

// An error nothing recognises is answered with a bare internal error: its own
// message is as likely to be a connection string as an explanation.
func TestUnrecognisedErrorLeaksNothing(t *testing.T) {
	rec := do(t, failing(errors.New("dial tcp 10.0.0.1:5432: connection refused")), "GET", "/api/v1/orders/x", nil)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d", rec.Code)
	}
	e := decodeError(t, rec)
	if e.Code != "INTERNAL_ERROR" {
		t.Errorf("code = %q", e.Code)
	}
	if e.Message != "An unexpected error occurred." {
		t.Errorf("message leaked detail: %q", e.Message)
	}
}

var errNoRows = errors.New("no rows")

func TestErrorMapperTranslatesADomainError(t *testing.T) {
	app := failing(fmt.Errorf("finding the order: %w", errNoRows), tork.OnError(func(err error) *tork.Error {
		if errors.Is(err, errNoRows) {
			return tork.NotFound("Order", "ord_98765")
		}
		return nil
	}))

	rec := do(t, app, "GET", "/api/v1/orders/ord_98765", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if e := decodeError(t, rec); e.Code != "RESOURCE_NOT_FOUND" {
		t.Errorf("code = %q", e.Code)
	}
}

func TestMappersAreTriedInOrderAndMayDecline(t *testing.T) {
	declined := false
	app := failing(errNoRows,
		tork.OnError(func(error) *tork.Error {
			declined = true
			return nil
		}),
		tork.OnError(func(error) *tork.Error {
			return tork.Forbidden("second mapper answered")
		}),
		tork.OnError(func(error) *tork.Error {
			return tork.Conflict("third mapper should never be asked")
		}),
	)

	rec := do(t, app, "GET", "/api/v1/orders/x", nil)
	if !declined {
		t.Error("the first mapper was never asked")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("status = %d, want the first non-nil answer", rec.Code)
	}
}

// Mappers see everything, including errors that already know their own
// answer, so an application can override the framework itself.
func TestMapperOverridesAnHTTPError(t *testing.T) {
	app := failing(outOfStockError{SKU: "abc"}, tork.OnError(func(error) *tork.Error {
		return tork.NewError(http.StatusTeapot, "OVERRIDDEN", "mapper won")
	}))

	rec := do(t, app, "GET", "/api/v1/orders/x", nil)
	if e := decodeError(t, rec); e.Code != "OVERRIDDEN" {
		t.Errorf("code = %q", e.Code)
	}
}

func TestErrorWriterReplacesTheWireFormat(t *testing.T) {
	app := failing(tork.NotFound("Order", "1"), tork.WriteErrorsWith(
		func(w http.ResponseWriter, _ *http.Request, e tork.Error) error {
			w.Header().Set("Content-Type", "text/plain")
			w.WriteHeader(e.Status)
			_, err := fmt.Fprintf(w, "%s: %s", e.Code, e.Message)
			return err
		},
	))

	rec := do(t, app, "GET", "/api/v1/orders/x", nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d", rec.Code)
	}
	if got := rec.Body.String(); got != `RESOURCE_NOT_FOUND: No order found with ID '1'.` {
		t.Errorf("body = %q", got)
	}
}

// A writer that fails has nowhere to report it — the response is already
// underway — so the request simply ends.
func TestErrorWriterFailureIsSurvivable(t *testing.T) {
	app := failing(tork.NotFound("Order", "1"), tork.WriteErrorsWith(
		func(http.ResponseWriter, *http.Request, tork.Error) error {
			return errors.New("the client hung up")
		},
	))

	do(t, app, "GET", "/api/v1/orders/x", nil)
}

func TestTimestampRoundTrips(t *testing.T) {
	stamp := tork.Timestamp(time.Date(2026, 7, 23, 16, 37, 23, 500, time.UTC))

	encoded, err := json.Marshal(stamp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Sub-second precision is dropped, which is what makes two servers
	// agree and a golden file possible.
	if string(encoded) != `"2026-07-23T16:37:23Z"` {
		t.Fatalf("encoded = %s", encoded)
	}

	var decoded tork.Timestamp
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if decoded.String() != stamp.String() {
		t.Errorf("round trip: %s != %s", decoded, stamp)
	}
}

func TestTimestampIsAlwaysUTC(t *testing.T) {
	zone := time.FixedZone("UTC+5", 5*60*60)
	stamp := tork.Timestamp(time.Date(2026, 7, 23, 21, 37, 23, 0, zone))

	if got := stamp.String(); got != "2026-07-23T16:37:23Z" {
		t.Errorf("String() = %q", got)
	}
}

func TestTimestampRejectsWhatItCannotRead(t *testing.T) {
	var stamp tork.Timestamp

	if err := json.Unmarshal([]byte(`12345`), &stamp); err == nil {
		t.Error("a number should not decode as a timestamp")
	}
	if err := json.Unmarshal([]byte(`"yesterday"`), &stamp); err == nil {
		t.Error("prose should not decode as a timestamp")
	}
}

// assertJSON compares two JSON documents by structure, so the test can be
// written readably without depending on key order or whitespace.
func assertJSON(t *testing.T, got []byte, want string) {
	t.Helper()

	var gotValue, wantValue any
	if err := json.Unmarshal(got, &gotValue); err != nil {
		t.Fatalf("decode response %s: %v", got, err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatalf("decode expectation: %v", err)
	}

	gotNormal, _ := json.MarshalIndent(gotValue, "", "  ")
	wantNormal, _ := json.MarshalIndent(wantValue, "", "  ")
	if string(gotNormal) != string(wantNormal) {
		t.Errorf("response:\n%s\n\nwant:\n%s", gotNormal, wantNormal)
	}
}
