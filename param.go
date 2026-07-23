package tork

import (
	"math"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
)

// param is what every typed field builder is made of: the declaration it is
// adding to, and the builder it reports mistakes to.
//
// The typed builders below exist so that the rules a field can have are
// decided by the compiler rather than checked later. StringParam has MaxLen
// and IntParam does not, so MaxLen on an integer is not a rule that fails — it
// is a program that does not build. It is the same bargain orm.StringColumn
// and orm.IntColumn make, for the same reason.
//
// Every builder below is reached from two places: a Source, which declares a
// parameter, and BodyRules, which declares a body field. They are the same
// builders, so a rule learned in one place works in the other.
type param struct {
	spec    *fieldSpec
	builder *InputBuilder
}

func (p param) add(r rule) { p.spec.rules = append(p.spec.rules, r) }

func (p param) change(t transform) { p.spec.transforms = append(p.spec.transforms, t) }

func (p param) require() { p.spec.required = true }

func (p param) fallback(value any) {
	p.spec.defaultValue = reflect.ValueOf(value)
	p.spec.hasValue = true
}

// ---------------------------------------------------------------- strings

// StringParam declares a string field and the rules it accepts.
type StringParam struct{ param }

// String declares a string field.
func (s *Source) String(field *string, name string) *StringParam {
	return &StringParam{param{s.declare(field, name, "tork.Source.String"), s.builder}}
}

// OptionalString declares a string field that remembers whether it was sent.
// Its rules are checked against the value when there is one, and skipped when
// there is not.
func (s *Source) OptionalString(field *Optional[string], name string) *StringParam {
	return &StringParam{param{s.declare(field, name, "tork.Source.OptionalString"), s.builder}}
}

// Required refuses a request that does not carry this value.
func (p *StringParam) Required() *StringParam { p.require(); return p }

// Default is the value used when the field is absent. A default is the
// author's own value, so it is not put through the rules below.
func (p *StringParam) Default(value string) *StringParam { p.fallback(value); return p }

// Trim removes leading and trailing whitespace before anything else looks at
// the value, so a length rule measures what was meant rather than what was
// pasted.
func (p *StringParam) Trim() *StringParam {
	p.change(func(v reflect.Value) { v.SetString(strings.TrimSpace(v.String())) })
	return p
}

// Collapse trims, and reduces every run of whitespace inside the value to a
// single space.
func (p *StringParam) Collapse() *StringParam {
	p.change(func(v reflect.Value) { v.SetString(strings.Join(strings.Fields(v.String()), " ")) })
	return p
}

// ToLower folds the value to lower case, for something that is compared or
// stored case-insensitively — an email address, a tag, a slug.
func (p *StringParam) ToLower() *StringParam {
	p.change(func(v reflect.Value) { v.SetString(strings.ToLower(v.String())) })
	return p
}

// ToUpper folds the value to upper case.
func (p *StringParam) ToUpper() *StringParam {
	p.change(func(v reflect.Value) { v.SetString(strings.ToUpper(v.String())) })
	return p
}

// MinLen refuses a string shorter than n characters, counted as runes rather
// than bytes so that a name in any script is measured the way its writer would.
func (p *StringParam) MinLen(n int) *StringParam {
	p.add(newRule(IssueTooShort, plural("must be at least %d character", n),
		func(v reflect.Value) bool { return runeLen(v) >= n }))
	return p
}

// MaxLen refuses a string longer than n characters.
func (p *StringParam) MaxLen(n int) *StringParam {
	p.add(newRule(IssueTooLong, plural("must be at most %d character", n),
		func(v reflect.Value) bool { return runeLen(v) <= n }))
	return p
}

// Range refuses a string outside a length range.
func (p *StringParam) Range(min, max int) *StringParam { return p.MinLen(min).MaxLen(max) }

// Len refuses a string that is not exactly n characters.
func (p *StringParam) Len(n int) *StringParam {
	p.add(newRule(IssueTooShort, plural("must be exactly %d character", n),
		func(v reflect.Value) bool { return runeLen(v) == n }))
	return p
}

// NotBlank refuses a value that is only whitespace, which a length rule alone
// would let through.
func (p *StringParam) NotBlank() *StringParam {
	return p.Check(IssueFieldRequired, "must not be blank", isNotBlank)
}

// OneOf refuses a value outside the set.
func (p *StringParam) OneOf(allowed ...string) *StringParam {
	p.add(newRule(IssueNotInSet, "must be one of "+strings.Join(allowed, ", "),
		func(v reflect.Value) bool { return slices.Contains(allowed, v.String()) }))
	return p
}

// Pattern refuses a value the expression does not match. An expression that
// does not compile is a mistake in the declaration, reported when the
// application builds.
func (p *StringParam) Pattern(expression string) *StringParam {
	compiled, err := regexp.Compile(expression)
	if err != nil {
		p.builder.fail("field %s has an invalid pattern %q: %v", p.spec.fieldName, expression, err)
		return p
	}
	p.add(newRule(IssuePatternMismatch, "is not in the form this field accepts",
		func(v reflect.Value) bool { return compiled.MatchString(v.String()) }))
	return p
}

// Contains, HasPrefix, and HasSuffix are the substring rules, for the cases a
// pattern would be a heavy way to say something simple.
func (p *StringParam) Contains(substring string) *StringParam {
	return p.Check(IssuePatternMismatch, "must contain "+substring,
		func(value string) bool { return strings.Contains(value, substring) })
}

func (p *StringParam) HasPrefix(prefix string) *StringParam {
	return p.Check(IssuePatternMismatch, "must begin with "+prefix,
		func(value string) bool { return strings.HasPrefix(value, prefix) })
}

func (p *StringParam) HasSuffix(suffix string) *StringParam {
	return p.Check(IssuePatternMismatch, "must end with "+suffix,
		func(value string) bool { return strings.HasSuffix(value, suffix) })
}

// The named formats. Each is a rule with its own issue, so a client can tell
// "that is not an email address" from "that is too long".

// Email refuses a value that is not an email address.
func (p *StringParam) Email() *StringParam {
	return p.Check(IssueInvalidEmail, "must be an email address", isEmail)
}

// UUID refuses a value that is not a UUID.
func (p *StringParam) UUID() *StringParam {
	return p.Check(IssueInvalidUUID, "must be a UUID", isUUID)
}

// URL refuses a value that is not an absolute URL.
func (p *StringParam) URL() *StringParam {
	return p.Check(IssueInvalidURL, "must be an absolute URL", isURL)
}

// IP, IPv4, and IPv6 refuse a value that is not an address of that family.
func (p *StringParam) IP() *StringParam {
	return p.Check(IssueInvalidFormat, "must be an IP address", isIP)
}

func (p *StringParam) IPv4() *StringParam {
	return p.Check(IssueInvalidFormat, "must be an IPv4 address", isIPv4)
}

func (p *StringParam) IPv6() *StringParam {
	return p.Check(IssueInvalidFormat, "must be an IPv6 address", isIPv6)
}

// CIDR refuses a value that is not an address with a prefix length.
func (p *StringParam) CIDR() *StringParam {
	return p.Check(IssueInvalidFormat, "must be an address in CIDR notation", isCIDR)
}

// Hostname refuses a value that is not a DNS hostname.
func (p *StringParam) Hostname() *StringParam {
	return p.Check(IssueInvalidFormat, "must be a hostname", isHostname)
}

// Semver refuses a value that is not a semantic version.
func (p *StringParam) Semver() *StringParam {
	return p.Check(IssueInvalidFormat, "must be a semantic version such as 1.4.0", isSemver)
}

// Slug refuses anything but lower-case letters, digits, and single hyphens.
func (p *StringParam) Slug() *StringParam {
	return p.Check(IssueInvalidFormat, "must be a slug, such as summer-sale", isSlug)
}

// CreditCard applies the Luhn check, which says a number is well formed. It
// does not say the card exists.
func (p *StringParam) CreditCard() *StringParam {
	return p.Check(IssueInvalidFormat, "must be a card number", isCreditCard)
}

// Base64 refuses a value that is not standard base64.
func (p *StringParam) Base64() *StringParam {
	return p.Check(IssueInvalidFormat, "must be base64", isBase64)
}

// JSON refuses a value that is not a JSON document, for a field that carries
// one as a string.
func (p *StringParam) JSON() *StringParam {
	return p.Check(IssueInvalidFormat, "must be JSON", isJSON)
}

// Alpha, Alphanumeric, and Numeric are about letters and digits in the Unicode
// sense, so a value in any script passes.
func (p *StringParam) Alpha() *StringParam {
	return p.Check(IssueInvalidFormat, "must contain only letters", isAlpha)
}

func (p *StringParam) Alphanumeric() *StringParam {
	return p.Check(IssueInvalidFormat, "must contain only letters and digits", isAlphanumeric)
}

func (p *StringParam) Numeric() *StringParam {
	return p.Check(IssueInvalidFormat, "must contain only digits", isNumeric)
}

// ASCII refuses anything outside ASCII.
func (p *StringParam) ASCII() *StringParam {
	return p.Check(IssueInvalidFormat, "must contain only ASCII characters", isASCII)
}

// Hex refuses anything but hexadecimal digits.
func (p *StringParam) Hex() *StringParam {
	return p.Check(IssueInvalidFormat, "must be hexadecimal", isHex)
}

// Lowercase and Uppercase refuse a value that is not already in that case. To
// fold it instead of refusing it, use ToLower or ToUpper.
func (p *StringParam) Lowercase() *StringParam {
	return p.Check(IssueInvalidFormat, "must be lower case", isLower)
}

func (p *StringParam) Uppercase() *StringParam {
	return p.Check(IssueInvalidFormat, "must be upper case", isUpper)
}

// Check adds a rule of your own.
//
// The issue is what a client switches on and the predicate completes the
// sentence "<field> ...", so Check("reserved_word", "must not be a reserved
// word", ...) answers with "name must not be a reserved word.".
//
//	b.String(&in.Name).Check("reserved_word", "must not be a reserved word",
//	    func(name string) bool { return !reserved[name] })
//
// The predicate takes the field's own type, so there is nothing to assert and
// nothing to get wrong.
func (p *StringParam) Check(issue, predicate string, ok func(string) bool) *StringParam {
	p.add(newRule(issue, predicate, func(v reflect.Value) bool { return ok(v.String()) }))
	return p
}

// ------------------------------------------------------------------- ints

// IntParam declares an integer field and the rules it accepts. It serves int
// and int64 alike, since the rules are the same and the width is the field's
// own business.
type IntParam struct{ param }

// Int declares an int field.
func (s *Source) Int(field *int, name string) *IntParam {
	return &IntParam{param{s.declare(field, name, "tork.Source.Int"), s.builder}}
}

// OptionalInt declares an int field that remembers whether it was sent.
func (s *Source) OptionalInt(field *Optional[int], name string) *IntParam {
	return &IntParam{param{s.declare(field, name, "tork.Source.OptionalInt"), s.builder}}
}

// Int64 declares an int64 field.
func (s *Source) Int64(field *int64, name string) *IntParam {
	return &IntParam{param{s.declare(field, name, "tork.Source.Int64"), s.builder}}
}

// OptionalInt64 declares an int64 field that remembers whether it was sent.
func (s *Source) OptionalInt64(field *Optional[int64], name string) *IntParam {
	return &IntParam{param{s.declare(field, name, "tork.Source.OptionalInt64"), s.builder}}
}

// Required refuses a request that does not carry this value.
func (p *IntParam) Required() *IntParam { p.require(); return p }

// Default is the value used when the field is absent.
func (p *IntParam) Default(value int) *IntParam { p.fallback(value); return p }

// Default64 is Default for a field declared with Int64.
func (p *IntParam) Default64(value int64) *IntParam { p.fallback(value); return p }

// Min refuses a value below min.
func (p *IntParam) Min(min int) *IntParam {
	p.add(newRule(IssueMinimumNotMet, sprintf("must be at least %d", min),
		func(v reflect.Value) bool { return v.Int() >= int64(min) }))
	return p
}

// Max refuses a value above max.
func (p *IntParam) Max(max int) *IntParam {
	p.add(newRule(IssueMaximumExceeded, sprintf("must be at most %d", max),
		func(v reflect.Value) bool { return v.Int() <= int64(max) }))
	return p
}

// Range refuses a value outside min and max, both included.
func (p *IntParam) Range(min, max int) *IntParam { return p.Min(min).Max(max) }

// Positive, NonNegative, Negative, and NonPositive are the bounds around zero,
// which are common enough to be worth naming.
func (p *IntParam) Positive() *IntParam {
	return p.Check(IssueMinimumNotMet, "must be greater than zero",
		func(value int64) bool { return value > 0 })
}

func (p *IntParam) NonNegative() *IntParam {
	return p.Check(IssueMinimumNotMet, "must not be negative",
		func(value int64) bool { return value >= 0 })
}

func (p *IntParam) Negative() *IntParam {
	return p.Check(IssueMaximumExceeded, "must be less than zero",
		func(value int64) bool { return value < 0 })
}

func (p *IntParam) NonPositive() *IntParam {
	return p.Check(IssueMaximumExceeded, "must not be greater than zero",
		func(value int64) bool { return value <= 0 })
}

// OneOf refuses a value outside the set.
func (p *IntParam) OneOf(allowed ...int) *IntParam {
	p.add(newRule(IssueNotInSet, "must be one of "+joinInts(allowed),
		func(v reflect.Value) bool { return slices.Contains(allowed, int(v.Int())) }))
	return p
}

// MultipleOf refuses a value that is not a multiple of step.
func (p *IntParam) MultipleOf(step int) *IntParam {
	p.add(newRule(IssueNotMultipleOf, sprintf("must be a multiple of %d", step),
		func(v reflect.Value) bool { return step != 0 && v.Int()%int64(step) == 0 }))
	return p
}

// Check adds a rule of your own. The predicate takes an int64, which every
// integer width fits.
func (p *IntParam) Check(issue, predicate string, ok func(int64) bool) *IntParam {
	p.add(newRule(issue, predicate, func(v reflect.Value) bool { return ok(v.Int()) }))
	return p
}

// ----------------------------------------------------------------- floats

// FloatParam declares a floating-point field and the rules it accepts.
type FloatParam struct{ param }

// Float64 declares a float64 field.
func (s *Source) Float64(field *float64, name string) *FloatParam {
	return &FloatParam{param{s.declare(field, name, "tork.Source.Float64"), s.builder}}
}

// OptionalFloat64 declares a float64 field that remembers whether it was sent.
func (s *Source) OptionalFloat64(field *Optional[float64], name string) *FloatParam {
	return &FloatParam{param{s.declare(field, name, "tork.Source.OptionalFloat64"), s.builder}}
}

// Required refuses a request that does not carry this value.
func (p *FloatParam) Required() *FloatParam { p.require(); return p }

// Default is the value used when the field is absent.
func (p *FloatParam) Default(value float64) *FloatParam { p.fallback(value); return p }

// Min refuses a value below min.
func (p *FloatParam) Min(min float64) *FloatParam {
	p.add(newRule(IssueMinimumNotMet, sprintf("must be at least %v", min),
		func(v reflect.Value) bool { return v.Float() >= min }))
	return p
}

// Max refuses a value above max.
func (p *FloatParam) Max(max float64) *FloatParam {
	p.add(newRule(IssueMaximumExceeded, sprintf("must be at most %v", max),
		func(v reflect.Value) bool { return v.Float() <= max }))
	return p
}

// Range refuses a value outside min and max, both included.
func (p *FloatParam) Range(min, max float64) *FloatParam { return p.Min(min).Max(max) }

// Positive and NonNegative are the bounds around zero.
func (p *FloatParam) Positive() *FloatParam {
	return p.Check(IssueMinimumNotMet, "must be greater than zero",
		func(value float64) bool { return value > 0 })
}

func (p *FloatParam) NonNegative() *FloatParam {
	return p.Check(IssueMinimumNotMet, "must not be negative",
		func(value float64) bool { return value >= 0 })
}

// Finite refuses the values JSON cannot even carry but a Go float can hold:
// the infinities and NaN.
func (p *FloatParam) Finite() *FloatParam {
	return p.Check(IssueInvalidNumber, "must be a finite number",
		func(value float64) bool { return !math.IsInf(value, 0) && !math.IsNaN(value) })
}

// Check adds a rule of your own.
func (p *FloatParam) Check(issue, predicate string, ok func(float64) bool) *FloatParam {
	p.add(newRule(issue, predicate, func(v reflect.Value) bool { return ok(v.Float()) }))
	return p
}

// ------------------------------------------------------------------ bools

// BoolParam declares a bool field. There is little to say about a boolean
// beyond whether it has to be there.
type BoolParam struct{ param }

// Bool declares a bool field.
func (s *Source) Bool(field *bool, name string) *BoolParam {
	return &BoolParam{param{s.declare(field, name, "tork.Source.Bool"), s.builder}}
}

// OptionalBool declares a bool field that remembers whether it was sent, which
// is the only way to tell false from absent.
func (s *Source) OptionalBool(field *Optional[bool], name string) *BoolParam {
	return &BoolParam{param{s.declare(field, name, "tork.Source.OptionalBool"), s.builder}}
}

// Required refuses a request that does not carry this value.
func (p *BoolParam) Required() *BoolParam { p.require(); return p }

// Default is the value used when the field is absent.
func (p *BoolParam) Default(value bool) *BoolParam { p.fallback(value); return p }

// MustBe refuses anything but the given answer, which is how a terms-accepted
// box is spelled.
func (p *BoolParam) MustBe(expected bool) *BoolParam {
	predicate := "must be true"
	if !expected {
		predicate = "must be false"
	}
	p.add(newRule(IssueNotInSet, predicate, func(v reflect.Value) bool { return v.Bool() == expected }))
	return p
}

// ------------------------------------------------------------------ times

// TimeParam declares a time.Time field and the rules it accepts.
type TimeParam struct{ param }

// Time declares a time.Time field, read as RFC 3339.
func (s *Source) Time(field *time.Time, name string) *TimeParam {
	return &TimeParam{param{s.declare(field, name, "tork.Source.Time"), s.builder}}
}

// OptionalTime declares a time.Time field that remembers whether it was sent.
func (s *Source) OptionalTime(field *Optional[time.Time], name string) *TimeParam {
	return &TimeParam{param{s.declare(field, name, "tork.Source.OptionalTime"), s.builder}}
}

// Required refuses a request that does not carry this value.
func (p *TimeParam) Required() *TimeParam { p.require(); return p }

// Default is the value used when the field is absent.
func (p *TimeParam) Default(value time.Time) *TimeParam { p.fallback(value); return p }

// UTC moves the value to UTC before anything else looks at it, so a handler
// never has to wonder which zone it was given.
func (p *TimeParam) UTC() *TimeParam {
	p.change(func(v reflect.Value) { v.Set(reflect.ValueOf(v.Interface().(time.Time).UTC())) })
	return p
}

// After refuses a time at or before the boundary.
func (p *TimeParam) After(boundary time.Time) *TimeParam {
	return p.Check(IssueTooEarly, "must be after "+boundary.UTC().Format(time.RFC3339),
		func(value time.Time) bool { return value.After(boundary) })
}

// Before refuses a time at or after the boundary.
func (p *TimeParam) Before(boundary time.Time) *TimeParam {
	return p.Check(IssueTooLate, "must be before "+boundary.UTC().Format(time.RFC3339),
		func(value time.Time) bool { return value.Before(boundary) })
}

// Past and Future are After and Before against the moment the request is
// judged, which is what a date of birth or an expiry needs.
func (p *TimeParam) Past() *TimeParam {
	return p.Check(IssueTooLate, "must be in the past",
		func(value time.Time) bool { return value.Before(time.Now()) })
}

func (p *TimeParam) Future() *TimeParam {
	return p.Check(IssueTooEarly, "must be in the future",
		func(value time.Time) bool { return value.After(time.Now()) })
}

// Check adds a rule of your own.
func (p *TimeParam) Check(issue, predicate string, ok func(time.Time) bool) *TimeParam {
	p.add(newRule(issue, predicate, func(v reflect.Value) bool { return ok(v.Interface().(time.Time)) }))
	return p
}

// -------------------------------------------------------------- durations

// DurationParam declares a time.Duration field and the rules it accepts.
type DurationParam struct{ param }

// Duration declares a time.Duration field, read in Go's own form.
func (s *Source) Duration(field *time.Duration, name string) *DurationParam {
	return &DurationParam{param{s.declare(field, name, "tork.Source.Duration"), s.builder}}
}

// Required refuses a request that does not carry this value.
func (p *DurationParam) Required() *DurationParam { p.require(); return p }

// Default is the value used when the field is absent.
func (p *DurationParam) Default(value time.Duration) *DurationParam { p.fallback(value); return p }

// Min refuses a duration shorter than min.
func (p *DurationParam) Min(min time.Duration) *DurationParam {
	return p.Check(IssueMinimumNotMet, "must be at least "+min.String(),
		func(value time.Duration) bool { return value >= min })
}

// Max refuses a duration longer than max.
func (p *DurationParam) Max(max time.Duration) *DurationParam {
	return p.Check(IssueMaximumExceeded, "must be at most "+max.String(),
		func(value time.Duration) bool { return value <= max })
}

// Positive refuses zero and anything below it.
func (p *DurationParam) Positive() *DurationParam {
	return p.Check(IssueMinimumNotMet, "must be greater than zero",
		func(value time.Duration) bool { return value > 0 })
}

// Check adds a rule of your own.
func (p *DurationParam) Check(issue, predicate string, ok func(time.Duration) bool) *DurationParam {
	p.add(newRule(issue, predicate, func(v reflect.Value) bool { return ok(time.Duration(v.Int())) }))
	return p
}

// ------------------------------------------------------------ string lists

// StringsParam declares a []string field and the rules it accepts.
type StringsParam struct{ param }

// Strings declares a []string field, filled by a repeated parameter or by a
// JSON array.
func (s *Source) Strings(field *[]string, name string) *StringsParam {
	return &StringsParam{param{s.declare(field, name, "tork.Source.Strings"), s.builder}}
}

// Required refuses a request that does not carry this value.
func (p *StringsParam) Required() *StringsParam { p.require(); return p }

// CSV reads one comma-separated value instead of a repeated parameter.
func (p *StringsParam) CSV() *StringsParam { p.spec.csv = true; return p }

// MinItems refuses a list with fewer than n entries.
func (p *StringsParam) MinItems(n int) *StringsParam {
	p.add(minItemsRule(n))
	return p
}

// MaxItems refuses a list with more than n entries.
func (p *StringsParam) MaxItems(n int) *StringsParam {
	p.add(maxItemsRule(n))
	return p
}

// NonEmpty refuses an empty list.
func (p *StringsParam) NonEmpty() *StringsParam { return p.MinItems(1) }

// Unique refuses a list that repeats a value.
func (p *StringsParam) Unique() *StringsParam {
	p.add(uniqueRule())
	return p
}

// OneOf refuses a list containing anything outside the set.
func (p *StringsParam) OneOf(allowed ...string) *StringsParam {
	p.add(newRule(IssueNotInSet, "must contain only "+strings.Join(allowed, ", "),
		func(v reflect.Value) bool {
			for i := range v.Len() {
				if !slices.Contains(allowed, v.Index(i).String()) {
					return false
				}
			}
			return true
		}))
	return p
}

// Each applies string rules and transforms to every entry of the list.
//
//	b.Strings(&in.Tags).Each(func(tag *tork.StringParam) {
//	    tag.Trim().ToLower().MaxLen(20)
//	})
//
// The entry builder is the ordinary string builder, so everything a string can
// be asked is available — and a failure names the list rather than the entry,
// since the entry has no name of its own.
func (p *StringsParam) Each(build func(item *StringParam)) *StringsParam {
	entry := &fieldSpec{fieldName: p.spec.fieldName}
	build(&StringParam{param{entry, p.builder}})
	p.spec.rules = append(p.spec.rules, eachRules(entry)...)
	if len(entry.transforms) > 0 {
		p.change(eachTransform(entry.transforms))
	}
	return p
}

// Check adds a rule of your own, against the whole list.
func (p *StringsParam) Check(issue, predicate string, ok func([]string) bool) *StringsParam {
	p.add(newRule(issue, predicate, func(v reflect.Value) bool { return ok(v.Interface().([]string)) }))
	return p
}

// --------------------------------------------------------------- int lists

// IntsParam declares a []int field and the rules it accepts.
type IntsParam struct{ param }

// Ints declares a []int field.
func (s *Source) Ints(field *[]int, name string) *IntsParam {
	return &IntsParam{param{s.declare(field, name, "tork.Source.Ints"), s.builder}}
}

// Required refuses a request that does not carry this value.
func (p *IntsParam) Required() *IntsParam { p.require(); return p }

// CSV reads one comma-separated value instead of a repeated parameter.
func (p *IntsParam) CSV() *IntsParam { p.spec.csv = true; return p }

// MinItems refuses a list with fewer than n entries.
func (p *IntsParam) MinItems(n int) *IntsParam {
	p.add(minItemsRule(n))
	return p
}

// MaxItems refuses a list with more than n entries.
func (p *IntsParam) MaxItems(n int) *IntsParam {
	p.add(maxItemsRule(n))
	return p
}

// NonEmpty refuses an empty list.
func (p *IntsParam) NonEmpty() *IntsParam { return p.MinItems(1) }

// Unique refuses a list that repeats a value.
func (p *IntsParam) Unique() *IntsParam {
	p.add(uniqueRule())
	return p
}

// Each applies integer rules to every entry of the list.
func (p *IntsParam) Each(build func(item *IntParam)) *IntsParam {
	entry := &fieldSpec{fieldName: p.spec.fieldName}
	build(&IntParam{param{entry, p.builder}})
	p.spec.rules = append(p.spec.rules, eachRules(entry)...)
	return p
}

// Check adds a rule of your own, against the whole list.
func (p *IntsParam) Check(issue, predicate string, ok func([]int) bool) *IntsParam {
	p.add(newRule(issue, predicate, func(v reflect.Value) bool { return ok(v.Interface().([]int)) }))
	return p
}

// ------------------------------------------------------------------ shared

// minItemsRule, maxItemsRule, and uniqueRule are the list rules that do not
// care what the list holds.
func minItemsRule(n int) rule {
	return newRule(IssueTooFewItems, plural("must have at least %d value", n),
		func(v reflect.Value) bool { return v.Len() >= n })
}

func maxItemsRule(n int) rule {
	return newRule(IssueTooManyItems, plural("must have at most %d value", n),
		func(v reflect.Value) bool { return v.Len() <= n })
}

func uniqueRule() rule {
	return newRule(IssueDuplicateItems, "must not repeat a value", func(v reflect.Value) bool {
		seen := make(map[any]bool, v.Len())
		for i := range v.Len() {
			entry := v.Index(i).Interface()
			if seen[entry] {
				return false
			}
			seen[entry] = true
		}
		return true
	})
}

// eachRules lifts the rules declared for one entry into rules about the list.
//
// A failure is reported against the list, because that is the field the client
// named; the message says every value rather than the value, so it reads as
// what it is.
func eachRules(entry *fieldSpec) []rule {
	lifted := make([]rule, 0, len(entry.rules))
	for _, r := range entry.rules {
		lifted = append(lifted, newPhrasedRule(
			r.issue,
			func(name string) string { return "every value in " + r.message(name) },
			func(v reflect.Value) bool {
				for i := range v.Len() {
					if !r.check(v.Index(i)) {
						return false
					}
				}
				return true
			},
		))
	}
	return lifted
}

// eachTransform lifts the transforms declared for one entry into a transform
// over the list.
func eachTransform(transforms []transform) transform {
	return func(v reflect.Value) {
		for i := range v.Len() {
			for _, change := range transforms {
				change(v.Index(i))
			}
		}
	}
}

// runeLen measures a string the way a person counts it.
func runeLen(v reflect.Value) int { return len([]rune(v.String())) }
