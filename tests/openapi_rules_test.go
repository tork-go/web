package tork_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"slices"
	"testing"
	"time"

	"github.com/tork-go/web"
)

// RulesInput carries one field per rule under test. Every rule is declared
// against its own parameter, so a request can break exactly one of them.
type RulesInput struct {
	MinLen   string
	MaxLen   string
	ExactLen string
	OneOf    string
	Pattern  string
	Contains string
	Prefix   string
	Suffix   string
	NotBlank string

	Email    string
	UUID     string
	URL      string
	IPv4     string
	IPv6     string
	Hostname string
	Semver   string
	Slug     string
	Base64   string
	JSONText string

	Alpha  string
	Alnum  string
	Digits string
	ASCII  string
	Hex    string
	Lower  string
	Upper  string

	Min      int
	Max      int
	Positive int
	NonNeg   int
	Negative int
	NonPos   int
	IntOneOf int
	Multiple int

	FMin      float64
	FMax      float64
	FPositive float64
	FNonNeg   float64
	FOneOf    float64
	FMultiple float64

	MustBe bool

	// The rules below deliberately contribute nothing to the schema.
	Custom     string
	IP         string
	CIDR       string
	Card       string
	Finite     float64
	Future     time.Time
	MinDuraton time.Duration
}

var rulesInput = tork.DefineInput(func(b *tork.InputBuilder, in *RulesInput) {
	b.Query.String(&in.MinLen, "minLen").MinLen(3)
	b.Query.String(&in.MaxLen, "maxLen").MaxLen(3)
	b.Query.String(&in.ExactLen, "exactLen").Len(4)
	b.Query.String(&in.OneOf, "oneOf").OneOf("red", "blue")
	b.Query.String(&in.Pattern, "pattern").Pattern("^x+$")
	b.Query.String(&in.Contains, "contains").Contains("ab")
	b.Query.String(&in.Prefix, "prefix").HasPrefix("ab")
	b.Query.String(&in.Suffix, "suffix").HasSuffix("ab")
	b.Query.String(&in.NotBlank, "notBlank").NotBlank()

	b.Query.String(&in.Email, "email").Email()
	b.Query.String(&in.UUID, "uuid").UUID()
	b.Query.String(&in.URL, "url").URL()
	b.Query.String(&in.IPv4, "ipv4").IPv4()
	b.Query.String(&in.IPv6, "ipv6").IPv6()
	b.Query.String(&in.Hostname, "hostname").Hostname()
	b.Query.String(&in.Semver, "semver").Semver()
	b.Query.String(&in.Slug, "slug").Slug()
	b.Query.String(&in.Base64, "base64").Base64()
	b.Query.String(&in.JSONText, "jsonText").JSON()

	b.Query.String(&in.Alpha, "alpha").Alpha()
	b.Query.String(&in.Alnum, "alnum").Alphanumeric()
	b.Query.String(&in.Digits, "digits").Numeric()
	b.Query.String(&in.ASCII, "ascii").ASCII()
	b.Query.String(&in.Hex, "hex").Hex()
	b.Query.String(&in.Lower, "lower").Lowercase()
	b.Query.String(&in.Upper, "upper").Uppercase()

	b.Query.Int(&in.Min, "min").Min(5)
	b.Query.Int(&in.Max, "max").Max(5)
	b.Query.Int(&in.Positive, "positive").Positive()
	b.Query.Int(&in.NonNeg, "nonNeg").NonNegative()
	b.Query.Int(&in.Negative, "negative").Negative()
	b.Query.Int(&in.NonPos, "nonPos").NonPositive()
	b.Query.Int(&in.IntOneOf, "intOneOf").OneOf(1, 2)
	b.Query.Int(&in.Multiple, "multiple").MultipleOf(3)

	b.Query.Float64(&in.FMin, "fMin").Min(1.5)
	b.Query.Float64(&in.FMax, "fMax").Max(2.5)
	b.Query.Float64(&in.FPositive, "fPositive").Positive()
	b.Query.Float64(&in.FNonNeg, "fNonNeg").NonNegative()
	b.Query.Float64(&in.FOneOf, "fOneOf").OneOf(1.5, 2.5)
	b.Query.Float64(&in.FMultiple, "fMultiple").MultipleOf(0.5)

	b.Query.Bool(&in.MustBe, "mustBe").MustBe(true)

	b.Query.String(&in.Custom, "custom").Check("not_x", "must be x",
		func(value string) bool { return value == "x" })
	b.Query.String(&in.IP, "ip").IP()
	b.Query.String(&in.CIDR, "cidr").CIDR()
	b.Query.String(&in.Card, "card").CreditCard()
	b.Query.Float64(&in.Finite, "finite").Finite()
	b.Query.Time(&in.Future, "future").Future()
	b.Query.Duration(&in.MinDuraton, "minDuration").Min(time.Second)
})

// rulesApp is the one application every rule test reads, so the runtime and
// the document under test are the same build.
func rulesApp(t *testing.T) *tork.App {
	t.Helper()
	_ = rulesInput
	app := newApp()
	app.GET("/probe", func(_ context.Context, in RulesInput) (greeting, error) {
		return greeting{Message: "ok"}, nil
	})
	return app
}

// paramSchemas reads the document and returns each parameter's schema by name.
func paramSchemas(t *testing.T, app *tork.App) map[string]map[string]any {
	t.Helper()
	doc, err := app.OpenAPI()
	if err != nil {
		t.Fatalf("document: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(doc.JSON(), &decoded); err != nil {
		t.Fatalf("document does not parse: %v", err)
	}

	paths, _ := decoded["paths"].(map[string]any)
	item, _ := paths["/probe"].(map[string]any)
	get, _ := item["get"].(map[string]any)
	params, _ := get["parameters"].([]any)

	schemas := map[string]map[string]any{}
	for _, p := range params {
		param, _ := p.(map[string]any)
		name, _ := param["name"].(string)
		schema, _ := param["schema"].(map[string]any)
		schemas[name] = schema
	}
	return schemas
}

// sends one value for one parameter and reports whether it was refused.
func refuses(t *testing.T, app *tork.App, name, value string) bool {
	t.Helper()
	target := "/probe?" + url.Values{name: {value}}.Encode()
	rec := httptest.NewRecorder()
	handlerOf(t, app).ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
	return rec.Code == http.StatusBadRequest
}

// Every rule is checked against the value it refuses, the value it accepts,
// and the keyword it emits — in one case, so a rule whose keyword stops
// matching its check cannot pass by having the other half tested elsewhere.
func TestRulesReachTheSchemaAndStillRun(t *testing.T) {
	app := rulesApp(t)
	schemas := paramSchemas(t, app)

	tests := []struct {
		param    string
		bad      string
		good     string
		keywords map[string]any
	}{
		{"minLen", "ab", "abc", map[string]any{"minLength": 3}},
		{"maxLen", "abcd", "abc", map[string]any{"maxLength": 3}},
		{"exactLen", "abc", "abcd", map[string]any{"minLength": 4, "maxLength": 4}},
		{"oneOf", "green", "red", map[string]any{"enum": []any{"red", "blue"}}},
		{"pattern", "y", "xx", map[string]any{"pattern": "^x+$"}},
		{"contains", "zz", "zabz", map[string]any{"pattern": "ab"}},
		{"prefix", "zab", "abz", map[string]any{"pattern": "^ab"}},
		{"suffix", "abz", "zab", map[string]any{"pattern": "ab$"}},
		{"notBlank", "   ", "x", map[string]any{"pattern": `\S`}},

		{"email", "nope", "a@b.co", map[string]any{"format": "email"}},
		{"uuid", "nope", "f47ac10b-58cc-4372-a567-0e02b2c3d479", map[string]any{"format": "uuid"}},
		{"url", "nope", "https://example.com", map[string]any{"format": "uri"}},
		{"ipv4", "nope", "192.168.0.1", map[string]any{"format": "ipv4"}},
		{"ipv6", "nope", "::1", map[string]any{"format": "ipv6"}},
		{"hostname", "not a host", "example.com", map[string]any{"format": "hostname"}},
		{"semver", "1.2", "1.2.3", map[string]any{"pattern": `^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?(?:\+[0-9A-Za-z.-]+)?$`}},
		{"slug", "Summer Sale", "summer-sale", map[string]any{"pattern": `^[a-z0-9]+(?:-[a-z0-9]+)*$`}},
		{"base64", "!!!", "aGk=", map[string]any{"contentEncoding": "base64"}},
		{"jsonText", "{", "{}", map[string]any{"contentMediaType": "application/json"}},

		{"alpha", "a1", "abc", map[string]any{"pattern": `^\p{L}+$`}},
		{"alnum", "a-1", "a1", map[string]any{"pattern": `^[\p{L}\p{Nd}]+$`}},
		{"digits", "a", "123", map[string]any{"pattern": `^\p{Nd}+$`}},
		{"ascii", "é", "abc", map[string]any{"pattern": `^[\x00-\x7F]*$`}},
		{"hex", "zz", "1aF", map[string]any{"pattern": `^[0-9a-fA-F]+$`}},
		{"lower", "Abc", "abc", map[string]any{"pattern": `^[^\p{Lu}\p{Lt}]*$`}},
		{"upper", "aBC", "ABC", map[string]any{"pattern": `^[^\p{Ll}\p{Lt}]*$`}},

		{"min", "4", "5", map[string]any{"minimum": 5}},
		{"max", "6", "5", map[string]any{"maximum": 5}},
		{"positive", "0", "1", map[string]any{"exclusiveMinimum": 0}},
		{"nonNeg", "-1", "0", map[string]any{"minimum": 0}},
		{"negative", "0", "-1", map[string]any{"exclusiveMaximum": 0}},
		{"nonPos", "1", "0", map[string]any{"maximum": 0}},
		{"intOneOf", "3", "2", map[string]any{"enum": []any{1, 2}}},
		{"multiple", "4", "9", map[string]any{"multipleOf": 3}},

		{"fMin", "1", "2", map[string]any{"minimum": 1.5}},
		{"fMax", "3", "2", map[string]any{"maximum": 2.5}},
		{"fPositive", "0", "1", map[string]any{"exclusiveMinimum": 0}},
		{"fNonNeg", "-1", "0", map[string]any{"minimum": 0}},
		{"fOneOf", "2", "1.5", map[string]any{"enum": []any{1.5, 2.5}}},
		{"fMultiple", "0.75", "1.5", map[string]any{"multipleOf": 0.5}},

		{"mustBe", "false", "true", map[string]any{"const": true}},
	}

	for _, tt := range tests {
		t.Run(tt.param, func(t *testing.T) {
			if !refuses(t, app, tt.param, tt.bad) {
				t.Errorf("%s=%q should have been refused", tt.param, tt.bad)
			}
			if refuses(t, app, tt.param, tt.good) {
				t.Errorf("%s=%q should have been accepted", tt.param, tt.good)
			}

			schema, ok := schemas[tt.param]
			if !ok {
				t.Fatalf("no parameter named %q in the document", tt.param)
			}
			for keyword, want := range tt.keywords {
				got, present := schema[keyword]
				if !present {
					t.Fatalf("schema for %s is missing %q: %v", tt.param, keyword, schema)
				}
				if !sameJSON(got, want) {
					t.Errorf("%s.%s = %v, want %v", tt.param, keyword, got, want)
				}
			}
		})
	}
}

// A rule with no JSON Schema equivalent contributes nothing, on purpose. This
// is asserted rather than left implicit so that adding a keyword to one of
// them is a deliberate change to a test rather than a silent one.
func TestRulesThatDeliberatelyDescribeNothing(t *testing.T) {
	app := rulesApp(t)
	schemas := paramSchemas(t, app)

	// Every keyword a rule could have contributed. A parameter below carries
	// its type, and nothing else.
	constraints := []string{
		"minLength", "maxLength", "pattern", "format", "enum", "const",
		"minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum",
		"multipleOf", "minItems", "maxItems", "uniqueItems",
		"contentEncoding", "contentMediaType",
	}

	// A time and a duration still carry a format, but it comes from the type
	// rather than from the rule — a time.Time is a date-time whether or not
	// anything was declared about it.
	tests := []struct {
		param       string
		fromTheType []string
	}{
		{"custom", nil},
		{"ip", nil},
		{"cidr", nil},
		{"card", nil},
		{"finite", nil},
		{"future", []string{"format"}},
		{"minDuration", []string{"format"}},
	}

	for _, tt := range tests {
		t.Run(tt.param, func(t *testing.T) {
			schema, ok := schemas[tt.param]
			if !ok {
				t.Fatalf("no parameter named %q", tt.param)
			}
			for _, keyword := range constraints {
				if slices.Contains(tt.fromTheType, keyword) {
					continue
				}
				if _, present := schema[keyword]; present {
					t.Errorf("the rule on %s should describe nothing, but the schema carries %q: %v",
						tt.param, keyword, schema)
				}
			}
		})
	}
}

// A custom Check still runs, even though it says nothing in the document.
func TestCustomCheckStillRuns(t *testing.T) {
	app := rulesApp(t)
	if !refuses(t, app, "custom", "y") {
		t.Error("a custom check should still refuse what it refuses")
	}
	if refuses(t, app, "custom", "x") {
		t.Error("a custom check should still accept what it accepts")
	}
}

// ListRulesInput carries the rules that describe a list rather than a value.
type ListRulesInput struct {
	Few    []string
	Many   []string
	Unique []string
	Colors []string
	Codes  []string
}

var listRulesInput = tork.DefineInput(func(b *tork.InputBuilder, in *ListRulesInput) {
	b.Query.Strings(&in.Few, "few").MinItems(2)
	b.Query.Strings(&in.Many, "many").MaxItems(2)
	b.Query.Strings(&in.Unique, "unique").Unique()
	b.Query.Strings(&in.Colors, "colors").OneOf("red", "blue")
	b.Query.Strings(&in.Codes, "codes").Each(func(item *tork.StringParam) { item.Len(2) })
})

// A rule about the list lands on the array; a rule about its entries lands on
// the items, which is the distinction Each exists to make.
func TestListRulesReachTheSchemaAndStillRun(t *testing.T) {
	_ = listRulesInput
	app := newApp()
	app.GET("/probe", func(_ context.Context, in ListRulesInput) (greeting, error) {
		return greeting{Message: "ok"}, nil
	})
	schemas := paramSchemas(t, app)

	send := func(name string, values ...string) bool {
		rec := httptest.NewRecorder()
		target := "/probe?" + url.Values{name: values}.Encode()
		handlerOf(t, app).ServeHTTP(rec, httptest.NewRequest("GET", target, nil))
		return rec.Code == http.StatusBadRequest
	}

	t.Run("minItems", func(t *testing.T) {
		if !send("few", "a") || send("few", "a", "b") {
			t.Error("MinItems does not run as declared")
		}
		if !sameJSON(schemas["few"]["minItems"], 2) {
			t.Errorf("minItems = %v", schemas["few"]["minItems"])
		}
	})

	t.Run("maxItems", func(t *testing.T) {
		if !send("many", "a", "b", "c") || send("many", "a", "b") {
			t.Error("MaxItems does not run as declared")
		}
		if !sameJSON(schemas["many"]["maxItems"], 2) {
			t.Errorf("maxItems = %v", schemas["many"]["maxItems"])
		}
	})

	t.Run("uniqueItems", func(t *testing.T) {
		if !send("unique", "a", "a") || send("unique", "a", "b") {
			t.Error("Unique does not run as declared")
		}
		if schemas["unique"]["uniqueItems"] != true {
			t.Errorf("uniqueItems = %v", schemas["unique"]["uniqueItems"])
		}
	})

	t.Run("enum on the items", func(t *testing.T) {
		if !send("colors", "green") || send("colors", "red", "blue") {
			t.Error("OneOf does not run as declared")
		}
		items, _ := schemas["colors"]["items"].(map[string]any)
		if !sameJSON(items["enum"], []any{"red", "blue"}) {
			t.Errorf("items.enum = %v, want the set on the entries", schemas["colors"])
		}
		if _, onTheArray := schemas["colors"]["enum"]; onTheArray {
			t.Error("the set describes the entries, not the list")
		}
	})

	t.Run("Each lands on the items", func(t *testing.T) {
		if !send("codes", "abc") || send("codes", "ab", "cd") {
			t.Error("Each does not run as declared")
		}
		items, _ := schemas["codes"]["items"].(map[string]any)
		if !sameJSON(items["minLength"], 2) || !sameJSON(items["maxLength"], 2) {
			t.Errorf("items = %v, want the entry's length rule", items)
		}
		if _, onTheArray := schemas["codes"]["minLength"]; onTheArray {
			t.Error("a rule declared with Each describes the entries, not the list")
		}
	})
}

// sameJSON compares a decoded value against what a test wrote, through JSON so
// that 1 and 1.0 are the same number.
func sameJSON(got, want any) bool {
	a, _ := json.Marshal(got)
	b, _ := json.Marshal(want)
	return string(a) == string(b)
}
