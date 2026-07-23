package tork

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// Error is the body every failing request answers with.
//
// One shape for every failure is the point. A client that can read one error
// can read all of them: the code says what went wrong in a form code can
// switch on, the message says it in a form a person can read, and details
// carries whatever that particular failure needs — a list of field problems
// for a rejected body, an object describing the limit for a throttled
// request. Only details changes shape, and only because forcing a validation
// failure and a rate limit into one schema would serve neither.
//
//	{
//	  "code": "RESOURCE_NOT_FOUND",
//	  "message": "No order found with ID 'ord_98765'.",
//	  "status": 404,
//	  "timestamp": "2026-07-23T16:37:23Z",
//	  "path": "/api/v1/orders/ord_98765",
//	  "details": {"resource": "Order", "identifier": "ord_98765"}
//	}
//
// Timestamp and Path are filled in when the response is written, not when the
// error is constructed: a handler has no business reading the clock, and does
// not know the path it was reached by. Whatever an error carries in those
// fields is overwritten.
type Error struct {
	// Code is the machine-readable name of the failure, in the
	// SCREAMING_SNAKE_CASE clients switch on. Unset, it is derived from
	// the status.
	Code string `json:"code"`
	// Message is the human-readable explanation, and should say what
	// happened rather than name the status again.
	Message string `json:"message"`
	// Status is the HTTP status code, repeated in the body so a client
	// holding only the body still knows what it is looking at.
	Status int `json:"status"`
	// Timestamp is when the response was written, to the second, in UTC.
	Timestamp Timestamp `json:"timestamp"`
	// Path is the path of the request that failed.
	Path string `json:"path"`
	// Details is whatever this failure needs to be actionable, and is
	// omitted when it needs nothing. Convention: a []FieldError for
	// anything rejecting input, an object for everything else.
	Details any `json:"details,omitempty"`
}

// Error makes an *Error usable as an ordinary Go error, so a handler can
// return one directly.
func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

// HTTPError returns the error itself, which is what makes a constructed
// *Error the simplest possible implementation of the interface.
func (e *Error) HTTPError() Error { return *e }

// WithDetails attaches the details of a failure and returns the error, so a
// constructor and its details read as one expression.
func (e *Error) WithDetails(details any) *Error {
	e.Details = details
	return e
}

// WithMessage replaces the message, for a caller who wants a constructor's
// status and code but their own wording.
func (e *Error) WithMessage(message string) *Error {
	e.Message = message
	return e
}

// fieldErrors is what one request's field problems travel as between the
// binder that found them and the response that reports them.
//
// It is an error so that a binder has one thing to return, and an HTTPError so
// that the ordinary error path serves it without a special case. Being a slice
// is what lets the problems from several input structs be joined into the one
// answer a client should get.
type fieldErrors []FieldError

func (f fieldErrors) Error() string {
	return ValidationFailed(f...).Error()
}

func (f fieldErrors) HTTPError() Error {
	return *ValidationFailed(f...)
}

// HTTPError is what an error implements to choose the response it becomes.
//
// One method rather than separate status and body methods, because the two
// cannot then disagree: the status served is the status in the body.
//
// Any error in a handler's return may implement it, at any depth — the
// framework unwraps with errors.As — so a domain package can define its own
// error types and have them answer correctly without importing anything from
// the transport layer beyond this interface.
type HTTPError interface {
	error
	HTTPError() Error
}

// FieldError is one thing wrong with one field, and is what Details carries
// for any failure that rejected input.
type FieldError struct {
	// Field names the offending field as the client spelled it, which for
	// a nested body field is a dotted path: "billing.zipCode".
	Field string `json:"field"`
	// Issue is the machine-readable reason, so a client can react to
	// "field_required" without parsing prose.
	Issue string `json:"issue"`
	// Message is the same reason for a person to read.
	Message string `json:"message"`
}

// The issues a rejected request can carry.
//
// They are constants because Issue is the half of a FieldError that client
// code acts on: a form that highlights the offending input, a retry that knows
// a timestamp was malformed rather than missing. Prose belongs in Message,
// which may be reworded at any time; these may not.
const (
	// IssueFieldRequired is a value the request had to carry and did not.
	IssueFieldRequired = "field_required"
	// IssueInvalidInteger, IssueInvalidNumber, and IssueInvalidBoolean are
	// values that were not of the shape the field reads.
	IssueInvalidInteger = "invalid_integer"
	IssueInvalidNumber  = "invalid_number"
	IssueInvalidBoolean = "invalid_boolean"
	// IssueInvalidDateTime and IssueInvalidDuration are the two time forms.
	IssueInvalidDateTime = "invalid_datetime"
	IssueInvalidDuration = "invalid_duration"
	// IssueInvalidFormat is a value a type with its own textual form
	// refused.
	IssueInvalidFormat = "invalid_format"
	// IssueInvalidType is a JSON value of the wrong kind: a string where a
	// number belongs.
	IssueInvalidType = "invalid_type"
	// IssueInvalidJSON is a body that is not JSON at all.
	IssueInvalidJSON = "invalid_json"
	// IssueBodyRequired is a missing body where one was expected.
	IssueBodyRequired = "body_required"
	// IssueUnknownField is a body field nothing declared, reported only when
	// the application asked for strict bodies.
	IssueUnknownField = "unknown_field"
)

// The issues a value that was read but refused can carry.
//
// These come from validation rules rather than from decoding: the value was of
// the right shape and the wrong content. They are separate constants from the
// ones above so a client can tell "I sent nonsense" from "I sent something
// sensible that is not allowed here".
const (
	// IssueMinimumNotMet and IssueMaximumExceeded are numeric bounds.
	IssueMinimumNotMet   = "minimum_not_met"
	IssueMaximumExceeded = "maximum_exceeded"
	// IssueTooShort and IssueTooLong are length bounds on a string.
	IssueTooShort = "too_short"
	IssueTooLong  = "too_long"
	// IssueTooFewItems, IssueTooManyItems, and IssueDuplicateItems are the
	// same for a list.
	IssueTooFewItems    = "too_few_items"
	IssueTooManyItems   = "too_many_items"
	IssueDuplicateItems = "duplicate_items"
	// IssueNotInSet is a value outside the set the field accepts.
	IssueNotInSet = "not_in_set"
	// IssuePatternMismatch is a string that did not match its pattern.
	IssuePatternMismatch = "pattern_mismatch"
	// IssueNotMultipleOf is a number that is not a multiple of its step.
	IssueNotMultipleOf = "not_multiple_of"
	// IssueTooEarly and IssueTooLate are bounds on a time.
	IssueTooEarly = "too_early"
	IssueTooLate  = "too_late"
	// IssueInvalidEmail, IssueInvalidUUID, and IssueInvalidURL are the named
	// string formats.
	IssueInvalidEmail = "invalid_email"
	IssueInvalidUUID  = "invalid_uuid"
	IssueInvalidURL   = "invalid_url"
)

// ResourceDetails is what a not-found failure carries: which kind of thing
// was looked for, and which one.
type ResourceDetails struct {
	Resource   string `json:"resource"`
	Identifier string `json:"identifier"`
}

// RateLimitDetails is what a throttled request carries, and is enough for a
// client to decide when to try again without guessing.
type RateLimitDetails struct {
	Limit             int       `json:"limit"`
	Window            string    `json:"window"`
	RetryAfterSeconds int       `json:"retryAfterSeconds"`
	ResetAt           Timestamp `json:"resetAt"`
}

// Timestamp is a time that reads as RFC 3339 to the second, in UTC.
//
// time.Time marshals with whatever fractional precision it happens to carry,
// which makes one server's errors differ from another's and a golden file
// impossible to write. Truncating at the boundary rather than at construction
// means a caller can hand over any time.Time and get the same answer.
type Timestamp time.Time

// MarshalJSON writes the time as "2026-07-23T16:37:23Z".
func (t Timestamp) MarshalJSON() ([]byte, error) {
	return []byte(strconv.Quote(time.Time(t).UTC().Format(time.RFC3339))), nil
}

// UnmarshalJSON reads back what MarshalJSON wrote, so a client — or a test —
// can decode an error body into the same type the server built it from.
func (t *Timestamp) UnmarshalJSON(data []byte) error {
	var text string
	if err := json.Unmarshal(data, &text); err != nil {
		return err
	}
	parsed, err := time.Parse(time.RFC3339, text)
	if err != nil {
		return err
	}
	*t = Timestamp(parsed)
	return nil
}

// String renders the timestamp the way it is served.
func (t Timestamp) String() string {
	return time.Time(t).UTC().Format(time.RFC3339)
}

// NewError builds an error with an explicit status, code, and message. The
// named constructors below are this with the usual arguments filled in; reach
// for it when none of them fits.
func NewError(status int, code, message string) *Error {
	return &Error{Code: code, Message: message, Status: status}
}

// BadRequest reports a request the server could read but will not act on.
func BadRequest(message string) *Error {
	return NewError(http.StatusBadRequest, "BAD_REQUEST", message)
}

// Unauthorized reports a request that was not authenticated.
func Unauthorized(message string) *Error {
	return NewError(http.StatusUnauthorized, "UNAUTHORIZED", message)
}

// Forbidden reports an authenticated request that is not allowed to do this.
func Forbidden(message string) *Error {
	return NewError(http.StatusForbidden, "FORBIDDEN", message)
}

// Conflict reports a request that contradicts the state it would change.
func Conflict(message string) *Error {
	return NewError(http.StatusConflict, "CONFLICT", message)
}

// NotFound reports that a named resource does not exist, and carries both
// halves of the answer in its details so a client need not parse the message.
//
//	tork.NotFound("Order", "ord_98765")
func NotFound(resource, identifier string) *Error {
	message := fmt.Sprintf("No %s found with ID '%s'.", strings.ToLower(resource), identifier)
	return NewError(http.StatusNotFound, "RESOURCE_NOT_FOUND", message).
		WithDetails(ResourceDetails{Resource: resource, Identifier: identifier})
}

// ValidationFailed reports input the server refused, one entry per offending
// field. It is what the framework itself returns when binding or validation
// rejects a request, and what a handler should return when its own checks do.
func ValidationFailed(fields ...FieldError) *Error {
	noun := "fields"
	if len(fields) == 1 {
		noun = "field"
	}
	message := fmt.Sprintf("Validation failed for %d %s.", len(fields), noun)
	return NewError(http.StatusBadRequest, "VALIDATION_ERROR", message).WithDetails(fields)
}

// RateLimited reports a throttled request.
func RateLimited(message string, details RateLimitDetails) *Error {
	return NewError(http.StatusTooManyRequests, "RATE_LIMIT_EXCEEDED", message).WithDetails(details)
}

// Internal is the answer to a failure the client can do nothing about, and
// deliberately says nothing: the cause is logged, not served.
func Internal() *Error {
	return NewError(http.StatusInternalServerError, "INTERNAL_ERROR", "An unexpected error occurred.")
}

// statusCodes names the statuses whose code should not simply be their
// standard reason phrase shouted. Everything else is derived, so a status
// this table has never heard of still gets a sensible code.
var statusCodes = map[int]string{
	http.StatusNotFound:            "RESOURCE_NOT_FOUND",
	http.StatusTooManyRequests:     "RATE_LIMIT_EXCEEDED",
	http.StatusInternalServerError: "INTERNAL_ERROR",
}

// codeForStatus is the code an error gets when it did not choose one.
func codeForStatus(status int) string {
	if code, ok := statusCodes[status]; ok {
		return code
	}
	text := http.StatusText(status)
	if text == "" {
		return "ERROR"
	}
	return screamingSnake(text)
}

// screamingSnake turns a reason phrase into a code: "Unsupported Media Type"
// becomes "UNSUPPORTED_MEDIA_TYPE".
//
// Only spacing separates words. Punctuation is dropped rather than becoming a
// separator, so "I'm a teapot" is IM_A_TEAPOT and not I_M_A_TEAPOT, and a
// separator is written only once something follows it, which is what keeps a
// trailing one from ever being emitted.
func screamingSnake(text string) string {
	var b strings.Builder
	separated := false
	for _, r := range text {
		switch {
		case r == ' ' || r == '-' || r == '_':
			separated = b.Len() > 0
		case r >= 'a' && r <= 'z':
			writeSeparated(&b, r-32, &separated)
		case (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			writeSeparated(&b, r, &separated)
		}
	}
	return b.String()
}

// writeSeparated writes one rune of a code, preceded by the underscore its
// pending separator earned.
func writeSeparated(b *strings.Builder, r rune, separated *bool) {
	if *separated {
		b.WriteByte('_')
		*separated = false
	}
	b.WriteRune(r)
}

// ErrorMapper turns an error the framework does not recognise into one it
// does, and is how a domain error becomes a status without the domain package
// knowing about HTTP.
//
// It returns nil for errors it has nothing to say about. Mappers are tried in
// the order they were declared, and the first non-nil answer wins.
//
//	tork.OnError(func(err error) *tork.Error {
//	    if errors.Is(err, orm.ErrNoRows) {
//	        return tork.NotFound("Order", "")
//	    }
//	    return nil
//	})
type ErrorMapper func(error) *Error

// OnError adds a mapper for errors that do not implement HTTPError. Declaring
// it more than once is normal; each mapper handles what it recognises.
func OnError(mapper ErrorMapper) Option {
	return newOption("OnError", scopeApp, func(m *meta) error {
		if mapper == nil {
			return fmt.Errorf("mapper must not be nil")
		}
		m.errorMappers = append(m.errorMappers, mapper)
		return nil
	})
}

// ErrorWriter serializes a finished error. Replacing it replaces the wire
// format entirely, for an API that has to answer in a shape it did not
// choose; the default writes Error as JSON.
type ErrorWriter func(http.ResponseWriter, *http.Request, Error) error

// WriteErrorsWith replaces the error serializer.
func WriteErrorsWith(writer ErrorWriter) Option {
	return newOption("WriteErrorsWith", scopeApp, func(m *meta) error {
		if writer == nil {
			return fmt.Errorf("writer must not be nil")
		}
		m.errorWriter = writer
		return nil
	})
}

// Logger replaces the logger the framework reports unexpected failures to.
func Logger(logger *slog.Logger) Option {
	return newOption("Logger", scopeApp, func(m *meta) error {
		if logger == nil {
			return fmt.Errorf("logger must not be nil")
		}
		m.logger = logger
		return nil
	})
}
