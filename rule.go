package tork

import (
	"reflect"
)

// rule is one check a value has to pass, compiled where it was declared.
//
// The check closure is built by a typed builder that already knows the field's
// kind, which is why it can call v.Int() or v.Len() without asking: a rule that
// does not fit its field cannot be constructed, so it never has to be defended
// against here.
//
// Issue is the code the client switches on. Message is a function of the
// field's name rather than a fixed string, because most rules read as "page
// must be at least 1" but a few — a rule applied to every entry of a list —
// need to say something else about the same name.
type rule struct {
	issue   string
	message func(name string) string
	check   func(reflect.Value) bool
}

// newRule builds a rule whose message is the field's name followed by the
// predicate: "must be at least 1" becomes "page must be at least 1.". Nearly
// every rule is built this way, so every field rejected the same way is told so
// in the same words.
func newRule(issue, predicate string, check func(reflect.Value) bool) rule {
	return rule{
		issue:   issue,
		message: func(name string) string { return name + " " + predicate + "." },
		check:   check,
	}
}

// newPhrasedRule builds a rule that words its own message.
func newPhrasedRule(issue string, message func(name string) string, check func(reflect.Value) bool) rule {
	return rule{issue: issue, message: message, check: check}
}

// transform changes a value before it is judged.
//
// Trimming and case folding are not checks — they are what the value becomes —
// so they run first and the rules see the result. That ordering is the whole
// reason they are a separate list: trimming after a length check would be
// pointless, and checking before trimming would reject values the API is about
// to accept anyway.
type transform func(reflect.Value)

// applyTransforms runs each transform against a bound value, in the order they
// were declared.
func applyTransforms(transforms []transform, field reflect.Value) {
	if len(transforms) == 0 {
		return
	}
	value, present := ruleValue(field)
	if !present {
		return
	}
	for _, change := range transforms {
		change(value)
	}
}

// ruleValue is the value a rule or transform actually works on.
//
// A rule is declared for the type a field holds, not for the wrapper it is
// written in, so an Optional[int] with Min(1) means the int has to be at least
// one. Unwrapping here is also what settles when a rule runs at all: an
// Optional that is unset or explicitly null carries no value, so there is
// nothing to judge and the rules are skipped.
func ruleValue(value reflect.Value) (reflect.Value, bool) {
	if value.CanAddr() {
		if optional, ok := value.Addr().Interface().(optionalTarget); ok {
			inner, set, null := optional.target()
			if !*set || *null {
				return reflect.Value{}, false
			}
			return reflect.ValueOf(inner).Elem(), true
		}
	}
	return value, true
}

// applyRules runs every rule against a bound value and reports what failed.
//
// All of them run rather than stopping at the first, for the same reason every
// field is bound before the request is refused: a client fixing its call should
// learn everything wrong with it at once.
func applyRules(rules []rule, field reflect.Value, name, errField string) fieldErrors {
	if len(rules) == 0 {
		return nil
	}
	value, present := ruleValue(field)
	if !present {
		return nil
	}

	var failures fieldErrors
	for _, r := range rules {
		if !r.check(value) {
			failures = append(failures, FieldError{
				Field:   errField,
				Issue:   r.issue,
				Message: r.message(name),
			})
		}
	}
	return failures
}
