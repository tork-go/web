package tork

import (
	"fmt"
	"reflect"
	"strings"
	"time"
)

// Body is a declared set of body rules, returned by DefineBody so the
// declaration has something to be assigned to — and so that Check can be
// chained onto it.
type Body[T any] struct {
	rules *bodyRules
}

// DefineBody declares the rules a request body's fields have to pass.
//
//	type CreateItemBody struct {
//	    tork.JSONBody
//	    Name  string `json:"name"`
//	    Price int64  `json:"price"`
//	}
//
//	var createItemBody = tork.DefineBody(func(b *tork.BodyRules, in *CreateItemBody) {
//	    b.String(&in.Name).Required().Range(2, 120)
//	    b.Int64(&in.Price).Min(0)
//	})
//
// Only the rules move into code. The wire name of each field stays in its json
// tag, because that is what encoding/json reads to decode the document in the
// first place, and saying it twice would be one more thing that can disagree.
//
// The rules apply wherever the body type is used — taken directly by a handler,
// or as the field of an input struct — because they belong to the type.
//
// A field is checked only when it holds something. A body cannot tell a field
// that was left out from one sent as its zero value, so the zero value is what
// counts as absent: use Required to insist on one, and tork.Optional to tell an
// explicit null from an omission.
func DefineBody[T any](build func(b *BodyRules, in *T)) *Body[T] {
	template := new(T)
	spec := &inputSpec{typ: reflect.TypeFor[T]()}
	inner := &InputBuilder{spec: spec, template: reflect.ValueOf(template).Elem()}

	if spec.typ.Kind() != reflect.Struct {
		spec.errs = append(spec.errs, fmt.Errorf("tork.DefineBody: %s is not a struct", spec.typ))
		registerBodyErrors(spec)
		return &Body[T]{rules: &bodyRules{}}
	}

	build(&BodyRules{inner: inner}, template)

	fields := make([]bodyFieldSpec, 0, len(spec.fields))
	for i := range spec.fields {
		declared := &spec.fields[i]
		name, ok := jsonName(spec.typ, declared.index)
		if !ok {
			spec.errs = append(spec.errs, fmt.Errorf(
				"tork.DefineBody: field %s of %s is not serialized, so no rule can apply to it",
				declared.fieldName, spec.typ))
			continue
		}
		fields = append(fields, bodyFieldSpec{
			index: declared.index, name: name, required: declared.required,
			transforms: declared.transforms, rules: declared.rules,
		})
	}

	if len(spec.errs) > 0 {
		registerBodyErrors(spec)
		return &Body[T]{rules: &bodyRules{}}
	}

	rules := &bodyRules{fields: fields}
	registerBody(spec.typ, rules)
	return &Body[T]{rules: rules}
}

// Check adds a validator that sees the whole decoded body, which is the only
// way to say something about two fields at once.
//
//	var bookingBody = tork.DefineBody(func(b *tork.BodyRules, in *BookingBody) {
//	    b.Time(&in.From).Required()
//	    b.Time(&in.Until).Required()
//	}).Check(func(in *BookingBody) []tork.FieldError {
//	    if !in.Until.After(in.From) {
//	        return []tork.FieldError{{
//	            Field:   "until",
//	            Issue:   "before_start",
//	            Message: "until must be after from.",
//	        }}
//	    }
//	    return nil
//	})
//
// It runs after every field has been judged on its own, and only when they all
// passed: a check comparing two fields has nothing useful to say about a
// document that is already known to be wrong. Return nil when there is nothing
// to report; the issue is yours to name.
//
// The fields named are relative to the body, so a body nested inside another
// still reports the path a client would recognise.
func (b *Body[T]) Check(validate func(in *T) []FieldError) *Body[T] {
	b.rules.whole = append(b.rules.whole, func(value reflect.Value) fieldErrors {
		return validate(value.Addr().Interface().(*T))
	})
	return b
}

// bodyErrors holds the mistakes a body declaration made, so that they surface
// when an application that uses the type is built rather than nowhere at all.
var bodyErrors = map[reflect.Type][]error{}

func registerBodyErrors(spec *inputSpec) {
	registry.Lock()
	defer registry.Unlock()
	bodyErrors[spec.typ] = spec.errs
}

func lookupBodyErrors(t reflect.Type) []error {
	registry.RLock()
	defer registry.RUnlock()
	return bodyErrors[t]
}

// jsonName is the name a field is serialized under, and reports false for a
// field encoding/json will not write at all.
func jsonName(structType reflect.Type, index []int) (string, bool) {
	field := structType.FieldByIndex(index)

	tag, ok := field.Tag.Lookup("json")
	if !ok {
		return field.Name, true
	}
	name, _, _ := strings.Cut(tag, ",")
	switch name {
	case "-":
		return "", false
	case "":
		return field.Name, true
	default:
		return name, true
	}
}

// BodyRules declares the rules a body's fields have to pass.
//
// Its methods take only the field, since the name comes from the json tag. It
// is otherwise the same typed builder used for parameters, so the rules a field
// can have are still decided by its type.
type BodyRules struct {
	inner *InputBuilder
}

// String declares rules for a string field.
func (b *BodyRules) String(field *string) *StringParam {
	return &StringParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.String"), b.inner}}
}

// Int declares rules for an int field.
func (b *BodyRules) Int(field *int) *IntParam {
	return &IntParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.Int"), b.inner}}
}

// Int64 declares rules for an int64 field.
func (b *BodyRules) Int64(field *int64) *IntParam {
	return &IntParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.Int64"), b.inner}}
}

// Float64 declares rules for a float64 field.
func (b *BodyRules) Float64(field *float64) *FloatParam {
	return &FloatParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.Float64"), b.inner}}
}

// Bool declares rules for a bool field.
func (b *BodyRules) Bool(field *bool) *BoolParam {
	return &BoolParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.Bool"), b.inner}}
}

// Time declares rules for a time.Time field.
func (b *BodyRules) Time(field *time.Time) *TimeParam {
	return &TimeParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.Time"), b.inner}}
}

// Strings declares rules for a []string field.
func (b *BodyRules) Strings(field *[]string) *StringsParam {
	return &StringsParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.Strings"), b.inner}}
}

// Ints declares rules for a []int field.
func (b *BodyRules) Ints(field *[]int) *IntsParam {
	return &IntsParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.Ints"), b.inner}}
}

// Duration declares rules for a time.Duration field.
func (b *BodyRules) Duration(field *time.Duration) *DurationParam {
	return &DurationParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.Duration"), b.inner}}
}

// The Optional forms, for a field that remembers whether it was sent. Their
// rules are checked against the value when there is one, and skipped when the
// field was left out or sent as null.

func (b *BodyRules) OptionalString(field *Optional[string]) *StringParam {
	return &StringParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.OptionalString"), b.inner}}
}

func (b *BodyRules) OptionalInt(field *Optional[int]) *IntParam {
	return &IntParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.OptionalInt"), b.inner}}
}

func (b *BodyRules) OptionalInt64(field *Optional[int64]) *IntParam {
	return &IntParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.OptionalInt64"), b.inner}}
}

func (b *BodyRules) OptionalFloat64(field *Optional[float64]) *FloatParam {
	return &FloatParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.OptionalFloat64"), b.inner}}
}

func (b *BodyRules) OptionalBool(field *Optional[bool]) *BoolParam {
	return &BoolParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.OptionalBool"), b.inner}}
}

func (b *BodyRules) OptionalTime(field *Optional[time.Time]) *TimeParam {
	return &TimeParam{param{b.inner.add(sourceQuery, field, "", "tork.BodyRules.OptionalTime"), b.inner}}
}
