package tork

import "encoding/json"

// Optional is a value that may be absent, may be null, and may be neither.
//
// JSON has three answers to "what did the client say about this field" and Go
// has two, which is why a *T is not enough for a PATCH: a nil pointer cannot
// say whether the field was left out or sent as null, and those mean opposite
// things — leave it alone, or clear it.
//
//	type PatchItemBody struct {
//	    tork.JSONBody
//	    Name  tork.Optional[string] `json:"name,omitzero"`
//	    Price tork.Optional[int64]  `json:"price,omitzero"`
//	}
//
//	if name, ok := body.Name.Get(); ok {
//	    item.Name = name          // sent, and not null
//	}
//	if body.Price.IsNull() {
//	    item.Price = nil          // sent as null
//	}
//
// The zero value is the absent one, and IsZero says so, which is what makes
// `omitzero` leave an untouched Optional out of a response entirely while
// still writing an explicit null for one that holds one.
//
// The fields are unexported on purpose. An Optional built as a literal could
// otherwise carry a value it does not consider set, and there is no reading of
// that a caller meant; Some and Null are the two things anyone wants to say.
type Optional[T any] struct {
	value T
	set   bool
	null  bool
}

// Some is a value that is present.
func Some[T any](value T) Optional[T] {
	return Optional[T]{value: value, set: true}
}

// Null is a value that was explicitly sent as null.
func Null[T any]() Optional[T] {
	return Optional[T]{set: true, null: true}
}

// Get returns the value and whether there is one. It reports false for both an
// absent field and an explicit null, which is what a caller that only wants to
// know "did they give me a usable value" should ask.
func (o Optional[T]) Get() (T, bool) {
	return o.value, o.set && !o.null
}

// Or returns the value, or the fallback when there is none.
func (o Optional[T]) Or(fallback T) T {
	if value, ok := o.Get(); ok {
		return value
	}
	return fallback
}

// IsSet reports whether the field was mentioned at all, null included.
func (o Optional[T]) IsSet() bool { return o.set }

// IsNull reports whether the field was mentioned and was null.
func (o Optional[T]) IsNull() bool { return o.null }

// IsZero reports whether the field was left out, which is the question
// encoding/json asks a type when a field is tagged omitzero.
func (o Optional[T]) IsZero() bool { return !o.set }

// MarshalJSON writes the value, or null.
//
// An absent Optional also writes null, because by the time this is called the
// decision to include the field has already been made — tag the field omitzero
// to have it left out instead.
func (o Optional[T]) MarshalJSON() ([]byte, error) {
	if !o.set || o.null {
		return []byte("null"), nil
	}
	return json.Marshal(o.value)
}

// UnmarshalJSON records that the field was present, and whether it was null.
//
// encoding/json calls this only for a field the document actually contains, so
// absence needs no handling here: it is the zero value, already correct.
func (o *Optional[T]) UnmarshalJSON(data []byte) error {
	o.set = true
	if string(data) == "null" {
		o.null = true
		return nil
	}
	return json.Unmarshal(data, &o.value)
}

// target exposes the innards to this package's binders, which have to write
// through the same unexported fields the constructors do.
//
// It returns a pointer to the value rather than the value, because that is
// what a decoder needs to write into, and it is the reason binding an Optional
// costs one interface assertion rather than a reflective method call.
func (o *Optional[T]) target() (value any, set *bool, null *bool) {
	return &o.value, &o.set, &o.null
}

// optionalTarget is what every Optional satisfies, and is how the binder
// recognises one without knowing T.
type optionalTarget interface {
	target() (value any, set *bool, null *bool)
}

// JSONBody marks a struct as a request body, so it can be taken as a handler
// parameter directly instead of wrapped in a struct that tags it.
//
//	type CreateItemBody struct {
//	    tork.JSONBody
//	    Name  string `json:"name"  validate:"required"`
//	    Price int64  `json:"price" validate:"min=0"`
//	}
//
//	func createItem(ctx context.Context, body CreateItemBody) (ItemResponse, error)
//
// Without it there would be no way to tell a body apart from an injected
// dependency: both are plain structs, and a body's fields carry json tags,
// which say nothing about where the value comes from.
//
// A struct that embeds it must not also carry path, query, header, cookie, or
// form tags. Mixing a body with parameters is what the wrapper form is for,
// and doing both here would leave two answers to what the struct is.
type JSONBody struct{}
