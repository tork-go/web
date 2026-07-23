package tork_test

import (
	"context"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/tork-go/web"
)

// ------------------------------------------------------------- transforms

type CleanInput struct {
	Name  string
	Email string
	Code  string
	Note  string
	Tags  []string
	At    time.Time
}

var cleanInput = tork.DefineInput(func(b *tork.InputBuilder, in *CleanInput) {
	b.Query.String(&in.Name, "name").Trim().MaxLen(5)
	b.Query.String(&in.Email, "email").Trim().ToLower().Email()
	b.Query.String(&in.Code, "code").ToUpper()
	b.Query.String(&in.Note, "note").Collapse()
	b.Query.Strings(&in.Tags, "tags").CSV().Each(func(tag *tork.StringParam) {
		tag.Trim().ToLower()
	})
	b.Query.Time(&in.At, "at").UTC()
})

func TestTransformsRunBeforeRules(t *testing.T) {
	var got CleanInput
	app := newApp()
	app.GET("/clean", func(_ context.Context, in CleanInput) (string, error) {
		got = in
		return "ok", nil
	})

	rec := do(t, app, "GET", "/clean?name=++Ada++&email=++ADA@Example.COM+&code=abc"+
		"&note=too++++many+++spaces&tags=+A+,+B+&at=2026-07-23T18:37:23%2B02:00", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	if got.Name != "Ada" {
		t.Errorf("Name = %q, want it trimmed", got.Name)
	}
	if got.Email != "ada@example.com" {
		t.Errorf("Email = %q", got.Email)
	}
	if got.Code != "ABC" {
		t.Errorf("Code = %q", got.Code)
	}
	if got.Note != "too many spaces" {
		t.Errorf("Note = %q", got.Note)
	}
	if !equalStrings(got.Tags, []string{"a", "b"}) {
		t.Errorf("Tags = %v", got.Tags)
	}
	if got.At.Location() != time.UTC || got.At.Hour() != 16 {
		t.Errorf("At = %v", got.At)
	}
}

// "  Ada  " is seven characters and "Ada" is three, so the rule only passes
// because it was asked after the trim.
func TestALengthRuleSeesTheTrimmedValue(t *testing.T) {
	_, rec := bound[CleanInput](t, "GET", "/clean", "/clean?name=++TooLong++", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if details := decodeFieldErrors(t, rec); details[0].Issue != tork.IssueTooLong {
		t.Errorf("field error = %+v", details[0])
	}
}

// ------------------------------------------------------------ each entry

type EachInput struct {
	Tags  []string
	Sizes []int
}

var eachInput = tork.DefineInput(func(b *tork.InputBuilder, in *EachInput) {
	b.Query.Strings(&in.Tags, "tags").CSV().Each(func(tag *tork.StringParam) {
		tag.MaxLen(4).Slug()
	})
	b.Query.Ints(&in.Sizes, "sizes").CSV().Each(func(size *tork.IntParam) {
		size.Range(1, 10)
	})
})

func TestEachEntryIsJudged(t *testing.T) {
	app := newApp()
	app.GET("/each", func(_ context.Context, in EachInput) (string, error) { return "ok", nil })

	tests := []struct {
		query   string
		issue   string
		message string
	}{
		{"tags=ok,toolong", tork.IssueTooLong, "every value in tags must be at most 4 characters."},
		{"tags=ok,NOPE", tork.IssueInvalidFormat, "every value in tags must be a slug, such as summer-sale."},
		{"sizes=1,99", tork.IssueMaximumExceeded, "every value in sizes must be at most 10."},
	}

	for _, tt := range tests {
		t.Run(tt.query, func(t *testing.T) {
			rec := do(t, app, "GET", "/each?"+tt.query, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}

			details := decodeFieldErrors(t, rec)
			if details[0].Issue != tt.issue || details[0].Message != tt.message {
				t.Errorf("field error = %+v", details[0])
			}
		})
	}
}

func TestEveryEntryPassing(t *testing.T) {
	app := newApp()
	app.GET("/each", func(_ context.Context, in EachInput) (EachInput, error) { return in, nil })

	rec := do(t, app, "GET", "/each?tags=a,b-c&sizes=1,10", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// --------------------------------------------------------- custom checks

var reserved = map[string]bool{"admin": true, "root": true}

type CustomInput struct {
	Name  string
	Count int
	Price float64
	Wait  time.Duration
	When  time.Time
	Tags  []string
	Sizes []int
}

var customInput = tork.DefineInput(func(b *tork.InputBuilder, in *CustomInput) {
	b.Query.String(&in.Name, "name").
		Check("reserved_word", "must not be a reserved word", func(name string) bool {
			return !reserved[name]
		})
	b.Query.Int(&in.Count, "count").
		Check("must_be_even", "must be an even number", func(count int64) bool {
			return count%2 == 0
		})
	b.Query.Float64(&in.Price, "price").
		Check("too_precise", "must have at most two decimal places", func(price float64) bool {
			return price*100 == float64(int64(price*100))
		})
	b.Query.Duration(&in.Wait, "wait").
		Check("not_round", "must be a whole number of seconds", func(wait time.Duration) bool {
			return wait%time.Second == 0
		})
	b.Query.Time(&in.When, "when").
		Check("not_monday", "must not be a Monday", func(when time.Time) bool {
			return when.Weekday() != time.Monday
		})
	b.Query.Strings(&in.Tags, "tags").
		Check("needs_pair", "must come in pairs", func(tags []string) bool {
			return len(tags)%2 == 0
		})
	b.Query.Ints(&in.Sizes, "sizes").
		Check("needs_pair", "must come in pairs", func(sizes []int) bool {
			return len(sizes)%2 == 0
		})
})

func TestCustomChecks(t *testing.T) {
	app := newApp()
	app.GET("/custom", func(_ context.Context, in CustomInput) (string, error) { return "ok", nil })

	tests := []struct {
		query   string
		issue   string
		message string
	}{
		{"name=admin", "reserved_word", "name must not be a reserved word."},
		{"count=3", "must_be_even", "count must be an even number."},
		{"price=1.005", "too_precise", "price must have at most two decimal places."},
		{"wait=1500ms", "not_round", "wait must be a whole number of seconds."},
		{"when=2026-07-20T00:00:00Z", "not_monday", "when must not be a Monday."},
		{"tags=a&tags=b&tags=c", "needs_pair", "tags must come in pairs."},
		{"sizes=1&sizes=2&sizes=3", "needs_pair", "sizes must come in pairs."},
	}

	for _, tt := range tests {
		t.Run(tt.issue+"/"+tt.query, func(t *testing.T) {
			rec := do(t, app, "GET", "/custom?"+tt.query, nil)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status %d: %s", rec.Code, rec.Body)
			}

			details := decodeFieldErrors(t, rec)
			if details[0].Issue != tt.issue || details[0].Message != tt.message {
				t.Errorf("field error = %+v", details[0])
			}
		})
	}
}

func TestCustomChecksPassing(t *testing.T) {
	app := newApp()
	app.GET("/custom", func(_ context.Context, in CustomInput) (string, error) { return "ok", nil })

	rec := do(t, app, "GET", "/custom?name=ada&count=2&price=1.25&wait=2s"+
		"&when=2026-07-21T00:00:00Z&tags=a&tags=b&sizes=1&sizes=2", nil)
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// ----------------------------------------------------------- the formats

type FormatsInput struct {
	IP       string
	V4       string
	V6       string
	Net      string
	Host     string
	Version  string
	Slug     string
	Card     string
	Encoded  string
	Document string
	Letters  string
	Alnum    string
	Digits   string
	Plain    string
	Hexed    string
	Lower    string
	Upper    string
	Filled   string
	Starts   string
	Ends     string
	Holds    string
}

var formatsInput = tork.DefineInput(func(b *tork.InputBuilder, in *FormatsInput) {
	b.Query.String(&in.IP, "ip").IP()
	b.Query.String(&in.V4, "v4").IPv4()
	b.Query.String(&in.V6, "v6").IPv6()
	b.Query.String(&in.Net, "net").CIDR()
	b.Query.String(&in.Host, "host").Hostname()
	b.Query.String(&in.Version, "version").Semver()
	b.Query.String(&in.Slug, "slug").Slug()
	b.Query.String(&in.Card, "card").CreditCard()
	b.Query.String(&in.Encoded, "encoded").Base64()
	b.Query.String(&in.Document, "document").JSON()
	b.Query.String(&in.Letters, "letters").Alpha()
	b.Query.String(&in.Alnum, "alnum").Alphanumeric()
	b.Query.String(&in.Digits, "digits").Numeric()
	b.Query.String(&in.Plain, "plain").ASCII()
	b.Query.String(&in.Hexed, "hexed").Hex()
	b.Query.String(&in.Lower, "lower").Lowercase()
	b.Query.String(&in.Upper, "upper").Uppercase()
	b.Query.String(&in.Filled, "filled").NotBlank()
	b.Query.String(&in.Starts, "starts").HasPrefix("sku_")
	b.Query.String(&in.Ends, "ends").HasSuffix(".json")
	b.Query.String(&in.Holds, "holds").Contains("-")
})

func TestFormatsAccepted(t *testing.T) {
	app := newApp()
	app.GET("/formats", func(_ context.Context, in FormatsInput) (string, error) { return "ok", nil })

	accepted := []string{
		"ip=10.0.0.1", "ip=2001:db8::1",
		"v4=10.0.0.1", "v6=2001:db8::1",
		"net=10.0.0.0/8", "host=api.example.com", "host=localhost",
		"version=1.4.0", "version=2.0.0-rc.1%2Bbuild.5",
		"slug=summer-sale", "slug=a",
		"card=4242424242424242", "card=4242-4242-4242-4242",
		"encoded=aGVsbG8%3D", "document=%7B%22a%22%3A1%7D",
		"letters=Ada", "letters=Ünicode", "alnum=ada42", "digits=42",
		"plain=hello", "hexed=deadBEEF42",
		"lower=already+lower", "upper=ALREADY+UPPER",
		"filled=x", "starts=sku_1", "ends=data.json", "holds=a-b",
	}

	for _, query := range accepted {
		t.Run(query, func(t *testing.T) {
			rec := do(t, app, "GET", "/formats?"+query, nil)
			if rec.Code != http.StatusOK {
				t.Errorf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}
}

func TestFormatsRefused(t *testing.T) {
	app := newApp()
	app.GET("/formats", func(_ context.Context, in FormatsInput) (string, error) { return "ok", nil })

	refused := []string{
		"ip=nope", "v4=2001:db8::1", "v6=10.0.0.1", "v6=::ffff:10.0.0.1",
		"net=10.0.0.0", "host=-bad.example.com", "host=a..b",
		"version=1.4", "version=01.2.3",
		"slug=Summer", "slug=-x", "slug=x-", "slug=a--b",
		"card=1234567890123456", "card=abcd", "card=42",
		"encoded=not+base64!", "document=%7Bbroken",
		"letters=ada42", "alnum=ada-42", "digits=4.2",
		"plain=caf%C3%A9", "hexed=xyz",
		"lower=Mixed", "upper=Mixed",
		"filled=+++", "starts=item_1", "ends=data.xml", "holds=ab",
	}

	for _, query := range refused {
		t.Run(query, func(t *testing.T) {
			rec := do(t, app, "GET", "/formats?"+query, nil)
			if rec.Code != http.StatusBadRequest {
				t.Errorf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}
}

// ------------------------------------------------------- numeric extras

type SignInput struct {
	Positive    int
	NonNegative int
	Negative    int
	NonPositive int
	Rate        float64
	Ratio       float64
	Finite      float64
	Wait        time.Duration
	Agreed      bool
	Refused     bool
}

var signInput = tork.DefineInput(func(b *tork.InputBuilder, in *SignInput) {
	b.Query.Int(&in.Positive, "positive").Positive()
	b.Query.Int(&in.NonNegative, "nonNegative").NonNegative()
	b.Query.Int(&in.Negative, "negative").Negative()
	b.Query.Int(&in.NonPositive, "nonPositive").NonPositive()
	b.Query.Float64(&in.Rate, "rate").Positive()
	b.Query.Float64(&in.Ratio, "ratio").NonNegative()
	b.Query.Float64(&in.Finite, "finite").Finite()
	b.Query.Duration(&in.Wait, "wait").Positive()
	b.Query.Bool(&in.Agreed, "agreed").MustBe(true)
	b.Query.Bool(&in.Refused, "refused").MustBe(false)
})

func TestSignAndBoundRules(t *testing.T) {
	app := newApp()
	app.GET("/sign", func(_ context.Context, in SignInput) (string, error) { return "ok", nil })

	refused := []string{
		"positive=0", "nonNegative=-1", "negative=0", "nonPositive=1",
		"rate=0", "ratio=-0.5", "finite=NaN", "finite=Inf",
		"wait=0s", "agreed=false", "refused=true",
	}
	for _, query := range refused {
		t.Run("refused/"+query, func(t *testing.T) {
			if rec := do(t, app, "GET", "/sign?"+query, nil); rec.Code != http.StatusBadRequest {
				t.Errorf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}

	accepted := []string{
		"positive=1", "nonNegative=0", "negative=-1", "nonPositive=0",
		"rate=0.5", "ratio=0", "finite=1.5", "wait=1s", "agreed=true", "refused=false",
	}
	for _, query := range accepted {
		t.Run("accepted/"+query, func(t *testing.T) {
			if rec := do(t, app, "GET", "/sign?"+query, nil); rec.Code != http.StatusOK {
				t.Errorf("status %d: %s", rec.Code, rec.Body)
			}
		})
	}
}

type WhenInput struct {
	Born    time.Time
	Expires time.Time
}

var whenInput = tork.DefineInput(func(b *tork.InputBuilder, in *WhenInput) {
	b.Query.Time(&in.Born, "born").Past()
	b.Query.Time(&in.Expires, "expires").Future()
})

func TestPastAndFuture(t *testing.T) {
	app := newApp()
	app.GET("/when", func(_ context.Context, in WhenInput) (string, error) { return "ok", nil })

	past := time.Now().Add(-24 * time.Hour).UTC().Format(time.RFC3339)
	future := time.Now().Add(24 * time.Hour).UTC().Format(time.RFC3339)

	if rec := do(t, app, "GET", "/when?born="+past+"&expires="+future, nil); rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
	if rec := do(t, app, "GET", "/when?born="+future, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a future birth date was accepted: %d", rec.Code)
	}
	if rec := do(t, app, "GET", "/when?expires="+past, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a past expiry was accepted: %d", rec.Code)
	}
}

type ListExtrasInput struct {
	Tags  []string
	Sizes []int
}

var listExtrasInput = tork.DefineInput(func(b *tork.InputBuilder, in *ListExtrasInput) {
	b.Query.Strings(&in.Tags, "tags").CSV().NonEmpty()
	b.Query.Ints(&in.Sizes, "sizes").CSV().NonEmpty().Unique()
})

func TestNonEmptyLists(t *testing.T) {
	app := newApp()
	app.GET("/lists", func(_ context.Context, in ListExtrasInput) (string, error) { return "ok", nil })

	if rec := do(t, app, "GET", "/lists?sizes=1,1", nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a repeated size was accepted: %d", rec.Code)
	}
	if rec := do(t, app, "GET", "/lists?tags=a&sizes=1,2", nil); rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// ------------------------------------------------------ nested documents

type Address struct {
	Line1   string `json:"line1"`
	ZipCode string `json:"zipCode"`
	Country string `json:"country"`
}

var addressRules = tork.DefineBody(func(b *tork.BodyRules, in *Address) {
	b.String(&in.Line1).Required().Trim().MaxLen(60)
	b.String(&in.ZipCode).Required().Trim().ToUpper()
	b.String(&in.Country).Len(2).ToUpper()
})

type LineItem struct {
	SKU      string `json:"sku"`
	Quantity int    `json:"quantity"`
}

var lineItemRules = tork.DefineBody(func(b *tork.BodyRules, in *LineItem) {
	b.String(&in.SKU).Required().HasPrefix("sku_")
	b.Int(&in.Quantity).Positive()
})

type CheckoutBody struct {
	tork.JSONBody
	Reference string     `json:"reference"`
	Billing   Address    `json:"billing"`
	Shipping  *Address   `json:"shipping"`
	Items     []LineItem `json:"items"`
}

var checkoutBody = tork.DefineBody(func(b *tork.BodyRules, in *CheckoutBody) {
	b.String(&in.Reference).Required()
})

func TestNestedBodyIsCheckedThrough(t *testing.T) {
	app := newApp()
	app.POST("/checkout", func(_ context.Context, body CheckoutBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/checkout", "application/json", `{
		"reference": "r1",
		"billing": {"line1": "1 High St", "country": "GBR"},
		"items": [{"sku": "sku_1", "quantity": 1}, {"sku": "nope", "quantity": -1}]
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	got := map[string]string{}
	for _, detail := range decodeFieldErrors(t, rec) {
		got[detail.Field] = detail.Issue
	}

	want := map[string]string{
		"billing.zipCode":   tork.IssueFieldRequired,
		"billing.country":   tork.IssueTooShort,
		"items[1].sku":      tork.IssuePatternMismatch,
		"items[1].quantity": tork.IssueMinimumNotMet,
	}
	for field, issue := range want {
		if got[field] != issue {
			t.Errorf("%s = %q, want %q (got %v)", field, got[field], issue, got)
		}
	}
}

// A nested struct nobody sent has nothing inside it to complain about.
func TestAbsentNestedStructIsNotChecked(t *testing.T) {
	app := newApp()
	app.POST("/checkout", func(_ context.Context, body CheckoutBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/checkout", "application/json", `{
		"reference": "r1",
		"billing": {"line1": "1 High St", "zipCode": "sw1a 1aa"}
	}`)
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// Transforms reach into a nested document too.
func TestNestedTransforms(t *testing.T) {
	var got CheckoutBody
	app := newApp()
	app.POST("/checkout", func(_ context.Context, body CheckoutBody) (string, error) {
		got = body
		return "ok", nil
	})

	rec := post(t, app, "POST", "/checkout", "application/json", `{
		"reference": "r1",
		"billing": {"line1": "  1 High St  ", "zipCode": " sw1a 1aa ", "country": "gb"},
		"shipping": {"line1": "2 Low St", "zipCode": "e1 6an"}
	}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if got.Billing.Line1 != "1 High St" || got.Billing.ZipCode != "SW1A 1AA" || got.Billing.Country != "GB" {
		t.Errorf("billing = %+v", got.Billing)
	}
	if got.Shipping.ZipCode != "E1 6AN" {
		t.Errorf("shipping = %+v", got.Shipping)
	}
}

// A pointer to a nested struct is checked when it is there.
func TestNestedPointerIsChecked(t *testing.T) {
	app := newApp()
	app.POST("/checkout", func(_ context.Context, body CheckoutBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/checkout", "application/json", `{
		"reference": "r1",
		"billing": {"line1": "1 High St", "zipCode": "x"},
		"shipping": {"line1": "2 Low St"}
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if details := decodeFieldErrors(t, rec); details[0].Field != "shipping.zipCode" {
		t.Errorf("field error = %+v", details[0])
	}
}

// ------------------------------------------------- whole-document checks

type BookingBody struct {
	tork.JSONBody
	From  time.Time `json:"from"`
	Until time.Time `json:"until"`
	Seats int       `json:"seats"`
}

var bookingBody = tork.DefineBody(func(b *tork.BodyRules, in *BookingBody) {
	b.Time(&in.From).Required()
	b.Time(&in.Until).Required()
	b.Int(&in.Seats).Positive()
}).Check(func(in *BookingBody) []tork.FieldError {
	if !in.Until.After(in.From) {
		return []tork.FieldError{{
			Field:   "until",
			Issue:   "before_start",
			Message: "until must be after from.",
		}}
	}
	return nil
})

func TestWholeDocumentCheck(t *testing.T) {
	app := newApp()
	app.POST("/bookings", func(_ context.Context, body BookingBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/bookings", "application/json",
		`{"from":"2026-07-23T10:00:00Z","until":"2026-07-23T09:00:00Z","seats":2}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}

	details := decodeFieldErrors(t, rec)
	if details[0].Field != "until" || details[0].Issue != "before_start" {
		t.Errorf("field error = %+v", details[0])
	}
}

func TestWholeDocumentCheckPassing(t *testing.T) {
	app := newApp()
	app.POST("/bookings", func(_ context.Context, body BookingBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/bookings", "application/json",
		`{"from":"2026-07-23T09:00:00Z","until":"2026-07-23T10:00:00Z","seats":2}`)
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// A check comparing two fields has nothing useful to say about a document
// already known to be wrong, so it is not asked.
func TestWholeDocumentCheckWaitsForTheFields(t *testing.T) {
	app := newApp()
	app.POST("/bookings", func(_ context.Context, body BookingBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/bookings", "application/json",
		`{"until":"2026-07-23T09:00:00Z","seats":0}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d", rec.Code)
	}

	for _, detail := range decodeFieldErrors(t, rec) {
		if detail.Issue == "before_start" {
			t.Errorf("the whole-document check ran anyway: %+v", detail)
		}
	}
}

// A body nested inside another still reports the path a client would
// recognise.
type OrderBody struct {
	tork.JSONBody
	Reference string      `json:"reference"`
	Booking   BookingBody `json:"booking"`
}

var orderBody = tork.DefineBody(func(b *tork.BodyRules, in *OrderBody) {
	b.String(&in.Reference).Required()
})

func TestNestedWholeDocumentCheckIsPrefixed(t *testing.T) {
	app := newApp()
	app.POST("/orders", func(_ context.Context, body OrderBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/orders", "application/json", `{
		"reference": "r1",
		"booking": {"from":"2026-07-23T10:00:00Z","until":"2026-07-23T09:00:00Z","seats":1}
	}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if details := decodeFieldErrors(t, rec); details[0].Field != "booking.until" {
		t.Errorf("field error = %+v", details[0])
	}
}

// A document that can contain itself is fine; walking it forever is not.
type NodeBody struct {
	tork.JSONBody
	Name     string      `json:"name"`
	Children []*NodeBody `json:"children"`
}

var nodeBody = tork.DefineBody(func(b *tork.BodyRules, in *NodeBody) {
	b.String(&in.Name).Required()
})

func TestSelfReferencingBodyDoesNotLoop(t *testing.T) {
	app := newApp()
	app.POST("/nodes", func(_ context.Context, body NodeBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/nodes", "application/json", `{"name":"root","children":[{"name":"a"}]}`)
	if rec.Code != http.StatusOK {
		t.Errorf("status %d: %s", rec.Code, rec.Body)
	}
}

// An embedded struct's fields are written at the top level by encoding/json,
// so that is where a complaint about them belongs.
type Audited struct {
	CreatedBy string `json:"createdBy"`
}

type AuditedBody struct {
	tork.JSONBody
	Audited
	Title string `json:"title"`
}

var auditedRules = tork.DefineBody(func(b *tork.BodyRules, in *Audited) {
	b.String(&in.CreatedBy).Required()
})

var auditedBody = tork.DefineBody(func(b *tork.BodyRules, in *AuditedBody) {
	b.String(&in.Title).Required()
})

func TestEmbeddedBodyFieldsAreNamedAtTheTopLevel(t *testing.T) {
	app := newApp()
	app.POST("/audited", func(_ context.Context, body AuditedBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/audited", "application/json", `{"title":"t"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if details := decodeFieldErrors(t, rec); details[0].Field != "createdBy" {
		t.Errorf("field error = %+v", details[0])
	}
}

// Trimming a field of nothing but spaces leaves it absent, and Required says
// so — which is the useful reading of both rules together.
type TrimmedRequiredBody struct {
	tork.JSONBody
	Name string `json:"name"`
}

var trimmedRequiredBody = tork.DefineBody(func(b *tork.BodyRules, in *TrimmedRequiredBody) {
	b.String(&in.Name).Trim().Required()
})

func TestTrimThenRequiredRejectsWhitespace(t *testing.T) {
	app := newApp()
	app.POST("/trimmed", func(_ context.Context, body TrimmedRequiredBody) (string, error) { return "ok", nil })

	rec := post(t, app, "POST", "/trimmed", "application/json", `{"name":"   "}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status %d: %s", rec.Code, rec.Body)
	}
	if details := decodeFieldErrors(t, rec); details[0].Issue != tork.IssueFieldRequired {
		t.Errorf("field error = %+v", details[0])
	}
	if !strings.Contains(rec.Body.String(), "name is required.") {
		t.Errorf("body = %s", rec.Body)
	}
}
