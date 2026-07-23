package tork

import (
	"encoding/json"
	"net/http"
	"reflect"
)

// ResponseSpec is what a Responder type promises about every value of that
// type, independent of any one instance: the status it answers with, the
// content type of its body, and the Go type of that body. It is asked once,
// at build, against a zero value — which is why it must not read anything an
// instance carries — so that the OpenAPI document a later phase builds does
// not need a request to know what an operation returns.
type ResponseSpec struct {
	// Status is the status this type answers with. A type whose real status
	// is chosen per instance, such as Response or Redirect, reports its
	// default here and documents the rest with Responds.
	Status int
	// ContentType is the response's Content-Type, empty when the response
	// carries no body.
	ContentType string
	// BodyType is the Go type written as the body, nil when there is none.
	BodyType reflect.Type
}

// Responder is a value that decides its own status, headers, and body,
// instead of leaving compileResults to assume 200 JSON.
//
// A type answers two different questions at two different times. Spec is
// asked once, at build, against a zero value of the type, and must be true
// of every instance; WriteResponse is asked once per request, against the
// actual value, and does the writing. Every response type this package
// defines derives what WriteResponse writes from the same place Spec reads,
// so within one type the two answers cannot drift apart — a Responder
// defined outside this package should follow the same rule for the same
// reason: two methods that independently decided the status could disagree,
// and one that only one of them reads cannot.
//
// Implementing Responder is what lets an application define a response type
// of its own; compileResults finds it through this interface alone, nothing
// else has to know about it.
type Responder interface {
	Spec() ResponseSpec
	WriteResponse(w http.ResponseWriter, r *http.Request) error
}

// responderType is Responder as a reflect.Type, checked once per handler
// result at build rather than once per request.
var responderType = reflect.TypeFor[Responder]()

// Response is a JSON body with a status and headers a handler chooses
// itself, for the one time in ten a plain T's default of 200 is not the
// answer. Everything else about a handler's return value stays exactly as
// plain as returning T alone — Response exists so that the rare case does
// not cost the common one anything.
//
//	func createItem(ctx context.Context, in CreateItemInput) (tork.Response[Item], error) {
//	    item := save(in)
//	    return tork.Respond(http.StatusCreated, item).WithLocation("/items/" + item.ID), nil
//	}
//
// The struct literal form works equally well —
// tork.Response[Item]{Status: 201, Body: item} — Respond exists only because
// a generic function infers T from its argument and a generic struct
// literal does not.
type Response[T any] struct {
	// Status is the response's status code. Zero resolves to 200, the same
	// answer a plain T would give.
	Status int
	// Body is written as the response's JSON body.
	Body T
	// Headers are written on the response in addition to Content-Type.
	Headers http.Header
}

// Respond builds a Response with the given status and body — the reading a
// return statement wants.
func Respond[T any](status int, body T) Response[T] {
	return Response[T]{Status: status, Body: body}
}

// WithHeader adds one header and returns the response, so a constructor and
// its headers read as one expression.
//
// The header map is cloned rather than written into in place: Response is a
// value, and two chains built from the same base — base.WithHeader("A", …)
// and base.WithHeader("B", …) — must not silently share, and so overwrite,
// one map underneath both.
func (r Response[T]) WithHeader(key, value string) Response[T] {
	r.Headers = cloneHeader(r.Headers)
	r.Headers.Set(key, value)
	return r
}

// WithLocation sets the Location header, for the common case of a response
// naming the resource it just created or moved.
func (r Response[T]) WithLocation(url string) Response[T] {
	return r.WithHeader("Location", url)
}

// resolvedStatus is the status Response actually answers with. Spec and
// WriteResponse both read it from here rather than each resolving Status
// themselves, which is what keeps them from being able to disagree.
func (r Response[T]) resolvedStatus() int {
	if r.Status == 0 {
		return http.StatusOK
	}
	return r.Status
}

// Spec reports the default status, 200, since a zero-valued Response cannot
// say what a real instance's Status field will hold. A route that answers
// with something other than 200 should also declare it with Responds, so
// the OpenAPI document names it beside this default.
func (r Response[T]) Spec() ResponseSpec {
	return ResponseSpec{
		Status:      http.StatusOK,
		ContentType: contentTypeJSON,
		BodyType:    reflect.TypeFor[T](),
	}
}

// WriteResponse marshals Body before writing anything, for the same reason
// writeJSON does: a value that cannot be encoded is then still a clean
// failure rather than a status and half a body already on the wire.
func (r Response[T]) WriteResponse(w http.ResponseWriter, _ *http.Request) error {
	body, err := json.Marshal(r.Body)
	if err != nil {
		return err
	}
	return writeBody(w, r.resolvedStatus(), contentTypeJSON, r.Headers, body)
}

// cloneHeader copies h, or returns an empty header when h is nil, so a
// caller can always set into the result without asking whether the source
// existed.
func cloneHeader(h http.Header) http.Header {
	if h == nil {
		return http.Header{}
	}
	return h.Clone()
}
