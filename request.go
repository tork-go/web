package tork

import (
	"context"
	"net/http"
)

// Request is the escape hatch: the raw request and response, for the handler
// that needs something the framework does not model.
//
// Taking one is a deliberate step outside the typed path — nothing a handler
// reads through here appears in the OpenAPI document, because nothing declared
// it. Reach for it to stream a body, hijack a connection, or read a header no
// struct field wants to own, and use a bound input struct for everything else.
type Request struct {
	request *http.Request
	writer  http.ResponseWriter
}

// HTTP returns the underlying request.
func (r *Request) HTTP() *http.Request { return r.request }

// Writer returns the underlying response writer. Writing to it takes over the
// response: whatever the handler returns afterwards is written on top of what
// is already sent, which is rarely what anyone wants.
func (r *Request) Writer() http.ResponseWriter { return r.writer }

// Context returns the request's context, for a handler that took a Request
// instead of a context.Context.
func (r *Request) Context() context.Context { return r.request.Context() }

// PathValue returns a path wildcard by name, for the rare case where naming it
// in a struct is not worth the struct.
func (r *Request) PathValue(name string) string { return r.request.PathValue(name) }
