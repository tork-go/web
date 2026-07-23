package tork

import (
	"reflect"
	"strconv"
	"strings"
)

// bind builds one input value for one request.
//
// Every field is attempted even after one fails, and the problems are gathered
// rather than returned at the first: a client that sent three bad parameters
// should learn about three, not be told about them one round trip at a time.
// Only a failure that makes the rest meaningless — a body that is the wrong
// media type, or too large to read — stops the walk.
func (p *inputPlan) bind(ex *exchange) (reflect.Value, error) {
	value := reflect.New(p.typ).Elem()
	var invalid fieldErrors

	for i := range p.fields {
		binder := &p.fields[i]
		raw, err := binder.lookup(ex)
		if err != nil {
			return reflect.Value{}, err
		}

		dst := fieldAt(value, binder.index)
		if len(raw) == 0 {
			if binder.required {
				invalid = append(invalid, FieldError{
					Field:   binder.errField,
					Issue:   IssueFieldRequired,
					Message: binder.name + " is required.",
				})
				continue
			}
			// A default is the author's own value, so it is not put
			// through the rules the client's values are judged by.
			if binder.fallback != nil {
				dst.Set(*binder.fallback)
			}
			continue
		}
		if binder.csv {
			raw = strings.Split(raw[len(raw)-1], ",")
		}
		if failure := binder.decode(dst, raw); failure != nil {
			invalid = append(invalid, FieldError{
				Field:   binder.errField,
				Issue:   failure.issue,
				Message: binder.name + " " + failure.predicate + ".",
			})
			continue
		}

		// Transforms run first, so a rule judges the value the handler will
		// actually be given rather than the one that arrived.
		applyTransforms(binder.transforms, dst)
		invalid = append(invalid, applyRules(binder.rules, dst, binder.name, binder.errField)...)
	}

	for i := range p.files {
		if err := p.files[i].bind(ex, value); err != nil {
			return reflect.Value{}, err
		}
	}

	if p.body != nil {
		dst := value
		if len(p.body.index) > 0 {
			dst = fieldAt(value, p.body.index)
		}
		bodyInvalid, err := ex.decodeBody(dst)
		if err != nil {
			return reflect.Value{}, err
		}
		if len(bodyInvalid) > 0 {
			// The body did not decode, so there is nothing to check the
			// rules against.
			invalid = append(invalid, bodyInvalid...)
		} else {
			invalid = append(invalid, checkBody(p.body.checks, dst, "")...)
		}
	}

	if len(invalid) > 0 {
		return reflect.Value{}, invalid
	}
	return value, nil
}

// checkBody runs the rules declared for a decoded body.
//
// Presence in a body is not the same question as presence in a query string:
// encoding/json has already filled the struct, and a field that was left out is
// indistinguishable from one sent as its zero value — unless it is an Optional,
// whose zero value is exactly "not sent". So the zero value is what counts as
// absent here, which is what makes Required mean what a reader expects, and
// what keeps a rule from being run against a field nobody filled in.
//
// The prefix is how far into the document the walk has come, and is what turns
// a rule declared on BillingAddress.ZipCode into a complaint about
// billing.zipCode.
func checkBody(checks []bodyCheck, body reflect.Value, prefix string) fieldErrors {
	var invalid fieldErrors
	for i := range checks {
		check := &checks[i]
		value := fieldAt(body, check.index)
		name := joinFieldPath(prefix, check.name)

		switch {
		case check.whole != nil:
			// A whole-document check has nothing to add once a field of
			// the document is already known to be wrong.
			if len(invalid) > 0 {
				continue
			}
			for _, validate := range check.whole {
				for _, failure := range validate(body) {
					failure.Field = joinFieldPath(prefix, failure.Field)
					invalid = append(invalid, failure)
				}
			}

		case check.nested != nil:
			// A struct nobody filled in has nothing inside it to judge.
			if inner, ok := structValue(value); ok {
				invalid = append(invalid, checkBody(check.nested, inner, name)...)
			}

		case check.element != nil:
			for entry := range value.Len() {
				if inner, ok := structValue(value.Index(entry)); ok {
					invalid = append(invalid, checkBody(check.element, inner, indexPath(name, entry))...)
				}
			}

		default:
			invalid = append(invalid, checkField(check, value, name)...)
		}
	}
	return invalid
}

// checkField judges one value of a body.
func checkField(check *bodyCheck, value reflect.Value, name string) fieldErrors {
	// Transforms run before the value is judged, and before it is asked
	// whether it is there at all — so trimming a field of nothing but
	// spaces leaves it absent, and Required says so.
	applyTransforms(check.transforms, value)

	if value.IsZero() {
		if check.required {
			return fieldErrors{{
				Field:   name,
				Issue:   IssueFieldRequired,
				Message: name + " is required.",
			}}
		}
		return nil
	}
	return applyRules(check.rules, value, name, name)
}

// structValue reaches the struct behind a field, and reports false for a
// pointer that is nil.
func structValue(value reflect.Value) (reflect.Value, bool) {
	if value.Kind() != reflect.Pointer {
		return value, true
	}
	if value.IsNil() {
		return reflect.Value{}, false
	}
	return value.Elem(), true
}

// joinFieldPath adds one name to a path, skipping the empty name an embedded
// struct contributes — encoding/json writes its fields at the top level, so
// that is where a complaint about them belongs.
func joinFieldPath(prefix, name string) string {
	switch {
	case prefix == "":
		return name
	case name == "":
		return prefix
	default:
		return prefix + "." + name
	}
}

// indexPath names one entry of a list, as items[0].
func indexPath(name string, at int) string {
	return name + "[" + strconv.Itoa(at) + "]"
}

// fieldAt walks to a field, taking the short path for the common case of one
// that is not embedded.
func fieldAt(value reflect.Value, index []int) reflect.Value {
	if len(index) == 1 {
		return value.Field(index[0])
	}
	return value.FieldByIndex(index)
}

// lookup reads every value the request carries for this parameter.
//
// An empty value counts as no value, which is what makes "?page=" mean the
// same as leaving page out and lets a default apply to both. A field that has
// to tell those apart is written as an Optional or a pointer, where the
// distinction is the point.
func (b *fieldBinder) lookup(ex *exchange) ([]string, error) {
	switch b.source {
	case sourcePath:
		return nonEmpty(ex.request.PathValue(b.name)), nil

	case sourceQuery:
		return nonEmpty(ex.queryValues()[b.name]...), nil

	case sourceHeader:
		return nonEmpty(ex.request.Header.Values(b.name)...), nil

	case sourceCookie:
		cookie, err := ex.request.Cookie(b.name)
		if err != nil {
			// The only error is that there is no such cookie.
			return nil, nil
		}
		return nonEmpty(cookie.Value), nil

	default:
		form, err := ex.formValues()
		if err != nil {
			return nil, err
		}
		return nonEmpty(form[b.name]...), nil
	}
}

// bind attaches uploaded files, which never pass through a string and so have
// no decoder.
func (b *fileBinder) bind(ex *exchange, value reflect.Value) error {
	files, err := ex.formFiles()
	if err != nil {
		return err
	}

	uploaded := files[b.name]
	if len(uploaded) == 0 {
		return nil
	}

	dst := fieldAt(value, b.index)
	if b.multiple {
		dst.Set(reflect.ValueOf(uploaded))
		return nil
	}
	dst.Set(reflect.ValueOf(uploaded[0]))
	return nil
}

// nonEmpty drops the values that are present but say nothing.
//
// It returns the slice it was given when there is nothing to drop, which is
// almost always, and builds a new one otherwise. Filtering in place would be
// cheaper still and wrong: the slice belongs to the parsed query or form,
// which is cached for the whole request.
func nonEmpty(values ...string) []string {
	for i, value := range values {
		if value == "" {
			return withoutEmpty(values, i)
		}
	}
	return values
}

// withoutEmpty copies values, skipping the empty ones, knowing the first of
// them is at start.
func withoutEmpty(values []string, start int) []string {
	kept := make([]string, start, len(values)-1)
	copy(kept, values[:start])
	for _, value := range values[start+1:] {
		if value != "" {
			kept = append(kept, value)
		}
	}
	return kept
}
