// Package tork is a web framework that reads like FastAPI and runs like
// hand-written net/http.
//
// Handlers are ordinary functions. They take a context, one or more structs
// describing the request, and whatever services they need; they return a
// value and an error. Nothing is registered in a container, no interface has
// to be satisfied, and the signature itself is the specification: the
// framework reads it to learn what to decode, what to validate, what to
// inject, and what to write into the OpenAPI document.
//
// It reads the signature exactly once. At startup every route's handler is
// classified and compiled into closures — a binder per input field, a check
// per validation rule, an encoder per response type — so tag parsing, name
// lookup, and type inspection never happen while a request is in flight.
// That is what separates this package from a reflection-driven framework:
// the convenience is paid for at boot, not per request. The generator (see
// gen/) later replaces those closures with straight-line Go, which removes
// the last of the reflection; the runtime here stays the definition of what
// the generated code must do.
//
// Routers own metadata rather than merely dispatching. A router carries a
// prefix, tags, dependencies, providers, and declared error responses, and
// anything included into it inherits them, so a feature package hands back
// one *Router and the application mounts it without repeating itself.
//
// Routing itself is net/http.ServeMux. Its patterns already spell path
// parameters as {name}, so this package adds no router of its own and stays
// compatible with any middleware written as func(http.Handler) http.Handler.
package tork
