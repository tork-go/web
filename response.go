package tork

import (
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"mime"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
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

// ResponseDoc is one entry Responds or Throws recorded: a response an
// operation may answer with beyond what its result type's own ResponseSpec
// already says. It changes nothing at runtime — the OpenAPI phase reads it
// off Route.Responses and Route.Throws to describe what compileResults
// cannot see for itself, such as a status chosen dynamically or an error a
// dependency throws rather than the handler returning.
type ResponseDoc struct {
	// Status is the status this response answers with. Zero for a Throws
	// entry, which documents a shape rather than one status — the shape's
	// own HTTPError answers for its status, and may answer differently
	// depending on how it was constructed.
	Status int
	// Type is the Go type of the response's body, or of the error itself
	// for a Throws entry.
	Type reflect.Type
	// Description explains the response for a person reading the
	// documentation. Empty for a Throws entry, which names its type instead.
	Description string
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
		Status:      r.resolvedStatus(),
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

// RawResponse is a body the handler has already reduced to bytes, with a
// content type of its own choosing — anything JSON is not the right wire
// format for: CSV, an image, a signed download link, a legacy XML feed.
//
//	func exportCSV(ctx context.Context, in ExportInput) (tork.RawResponse, error) {
//	    return tork.Raw("text/csv", buildCSV(rows)), nil
//	}
type RawResponse struct {
	// Body is written exactly as given; nothing here marshals it.
	Body []byte
	// ContentType is the response's Content-Type.
	ContentType string
	// Status is the response's status code. Zero resolves to 200.
	Status int
	// Headers are written on the response in addition to Content-Type.
	Headers http.Header
}

// Raw builds a RawResponse with the given content type and body.
func Raw(contentType string, body []byte) RawResponse {
	return RawResponse{ContentType: contentType, Body: body}
}

// WithStatus replaces the status and returns the response.
func (r RawResponse) WithStatus(status int) RawResponse {
	r.Status = status
	return r
}

// WithHeader adds one header and returns the response. See Response's
// WithHeader for why the header map is cloned rather than written into in
// place.
func (r RawResponse) WithHeader(key, value string) RawResponse {
	r.Headers = cloneHeader(r.Headers)
	r.Headers.Set(key, value)
	return r
}

// resolvedStatus is the status RawResponse actually answers with. See
// Response.resolvedStatus for why Spec and WriteResponse share it.
func (r RawResponse) resolvedStatus() int {
	if r.Status == 0 {
		return http.StatusOK
	}
	return r.Status
}

// Spec reports the default status, 200, for the same reason Response's
// does: the real value lives on the instance. ContentType has no default to
// fall back on the way Status does, so a zero-valued RawResponse — which is
// what Spec is always asked about — reports it empty; a route that always
// answers with the same content type should also declare it with Responds.
// BodyType is nil because Body is already bytes, with no Go type behind it
// worth naming in a schema.
func (r RawResponse) Spec() ResponseSpec {
	return ResponseSpec{
		Status:      r.resolvedStatus(),
		ContentType: r.ContentType,
		BodyType:    nil,
	}
}

// WriteResponse writes Body as given; there is nothing to marshal, so unlike
// Response's this call cannot fail before it has written anything, only
// while writing, and only when the client has gone away.
func (r RawResponse) WriteResponse(w http.ResponseWriter, _ *http.Request) error {
	return writeBody(w, r.resolvedStatus(), r.ContentType, r.Headers, r.Body)
}

// FileResponse is a body read from an io.Reader rather than held in memory
// at once, with a filename that becomes a download's suggested name rather
// than a field in a JSON object.
//
//	func downloadInvoice(ctx context.Context, in InvoiceInput) (tork.FileResponse, error) {
//	    f, size := openInvoice(in.InvoiceID)
//	    return tork.File(in.InvoiceID+".pdf", f).WithSize(size), nil
//	}
type FileResponse struct {
	// Reader is drained, in order, as the response body.
	Reader io.Reader
	// Filename becomes the suggested name in Content-Disposition.
	Filename string
	// ContentType is the response's Content-Type. Left empty, it is guessed
	// from Filename's extension, falling back to application/octet-stream
	// when nothing recognises it.
	ContentType string
	// Size, when known, becomes Content-Length so a client can show
	// progress; left unset, the response is written without one.
	Size Optional[int64]
	// Headers are written on the response in addition to the ones above.
	Headers http.Header
}

// File builds a FileResponse for r, suggested to be saved as name.
func File(name string, r io.Reader) FileResponse {
	return FileResponse{Reader: r, Filename: name}
}

// WithContentType replaces the guessed content type and returns the
// response.
func (f FileResponse) WithContentType(contentType string) FileResponse {
	f.ContentType = contentType
	return f
}

// WithSize sets Content-Length and returns the response.
func (f FileResponse) WithSize(size int64) FileResponse {
	f.Size = Some(size)
	return f
}

// WithHeader adds one header and returns the response. See Response's
// WithHeader for why the header map is cloned rather than written into in
// place.
func (f FileResponse) WithHeader(key, value string) FileResponse {
	f.Headers = cloneHeader(f.Headers)
	f.Headers.Set(key, value)
	return f
}

// resolvedContentType is what FileResponse actually answers with: what
// ContentType says, what Filename's extension guesses, or the one content
// type that always means "some bytes, meaning unknown" when neither says
// anything.
func (f FileResponse) resolvedContentType() string {
	if f.ContentType != "" {
		return f.ContentType
	}
	if guessed := mime.TypeByExtension(filepath.Ext(f.Filename)); guessed != "" {
		return guessed
	}
	return "application/octet-stream"
}

// Spec reports the default status, 200. ContentType and BodyType are left
// unset for the same reason RawResponse's are: Filename and ContentType
// live on the instance, and Spec is only ever asked about a zero one.
func (f FileResponse) Spec() ResponseSpec {
	return ResponseSpec{Status: http.StatusOK}
}

// WriteResponse writes the headers — including Content-Disposition, and
// Content-Length when Size is known — before draining Reader into the
// response body.
//
// Unlike Response's or RawResponse's, this call can fail after it has
// already written something: there is no way to marshal a reader ahead of
// time the way a value already in memory can be, so a reader that fails
// partway leaves a response already begun. serve answers that the same way
// it answers any Responder that fails after starting — by logging it, since
// the status line is already on the wire.
func (f FileResponse) WriteResponse(w http.ResponseWriter, _ *http.Request) error {
	h := w.Header()
	maps.Copy(h, f.Headers)
	h.Set("Content-Type", f.resolvedContentType())
	h.Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": f.Filename}))
	if size, ok := f.Size.Get(); ok {
		h.Set("Content-Length", strconv.FormatInt(size, 10))
	}
	w.WriteHeader(http.StatusOK)
	_, err := io.Copy(w, f.Reader)
	return err
}

// Stream is a body produced by a callback and sent as it is written, rather
// than assembled first the way every other response here is — the shape for
// a body too large to hold in memory at once, or one whose next chunk is not
// ready until the last one has already gone out.
//
//	func tailLog(ctx context.Context, in TailInput) (tork.Stream, error) {
//	    return tork.Streamed("text/plain", func(w io.Writer) error {
//	        return followLog(ctx, in.Path, w)
//	    }), nil
//	}
type Stream struct {
	// ContentType is the response's Content-Type.
	ContentType string
	// Status is the response's status code. Zero resolves to 200.
	Status int
	// Write produces the body. It is called once, given a writer that
	// flushes after every write the underlying connection can flush at all,
	// so the body reaches the client as it is produced instead of only once
	// Write returns.
	Write func(w io.Writer) error
}

// Streamed builds a Stream under the given content type.
func Streamed(contentType string, write func(w io.Writer) error) Stream {
	return Stream{ContentType: contentType, Write: write}
}

// WithStatus replaces the status and returns the response.
func (s Stream) WithStatus(status int) Stream {
	s.Status = status
	return s
}

// resolvedStatus is the status Stream actually answers with. See
// Response.resolvedStatus for why Spec and WriteResponse share it.
func (s Stream) resolvedStatus() int {
	if s.Status == 0 {
		return http.StatusOK
	}
	return s.Status
}

// Spec reports the default status, 200, for the same reason Response's
// does. ContentType is left unset for the same reason RawResponse's is:
// Spec is only ever asked about a zero-valued Stream, and there is no
// default content type to fall back to the way there is for status.
func (s Stream) Spec() ResponseSpec {
	return ResponseSpec{Status: s.resolvedStatus()}
}

// WriteResponse writes the status and content type once, then calls Write
// with a writer that flushes after every call when the connection
// underneath it can — a plain passthrough when it cannot, which is a
// fallback rather than a failure: a body that cannot be flushed early is
// still a complete body once Write returns.
func (s Stream) WriteResponse(w http.ResponseWriter, _ *http.Request) error {
	if s.ContentType != "" {
		w.Header().Set("Content-Type", s.ContentType)
	}
	w.WriteHeader(s.resolvedStatus())
	flusher, _ := w.(http.Flusher)
	return s.Write(flushingWriter{w: w, flusher: flusher})
}

// flushingWriter flushes after every write when it can, which is what makes
// a Stream arrive as it is produced instead of buffered behind the
// handler's return.
type flushingWriter struct {
	w       io.Writer
	flusher http.Flusher
}

func (f flushingWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if f.flusher != nil {
		f.flusher.Flush()
	}
	return n, err
}

// redirectStatuses is the one thing about a Redirect that is not per
// instance: the set of statuses HTTP gives a Location header a meaning for.
// Anything else is refused when the response is written, since nothing
// about an arbitrary int says which redirect semantics the caller meant.
var redirectStatuses = map[int]bool{
	http.StatusMovedPermanently:  true, // 301, permanent, method may change
	http.StatusFound:             true, // 302, temporary, method may change
	http.StatusSeeOther:          true, // 303, always fetch with GET
	http.StatusTemporaryRedirect: true, // 307, temporary, method preserved
	http.StatusPermanentRedirect: true, // 308, permanent, method preserved
}

// Redirect answers with a Location and no body of its own.
//
//	func legacyItem(ctx context.Context, in LegacyItemInput) (tork.Redirect, error) {
//	    return tork.PermanentRedirect("/items/" + in.ItemID), nil
//	}
//
// The named constructors below exist because 301, 302, 303, 307, and 308
// disagree about whether the redirect is permanent and whether a client may
// change its method, and a bare number says neither; RedirectTo is there
// for the rare case that already has a status in hand, such as one read
// back out of Responds.
type Redirect struct {
	// Status is one of 301, 302, 303, 307, or 308.
	Status int
	// URL becomes the Location header, absolute or relative.
	URL string
}

// RedirectTo builds a Redirect with an explicit status.
func RedirectTo(status int, url string) Redirect {
	return Redirect{Status: status, URL: url}
}

// MovedPermanently answers 301: the resource now lives at URL for good, and
// a client may switch a POST to a GET when it follows this.
func MovedPermanently(url string) Redirect {
	return Redirect{Status: http.StatusMovedPermanently, URL: url}
}

// Found answers 302: the resource is at URL for now, and a client may
// switch a POST to a GET when it follows this — the same method latitude as
// MovedPermanently, but without saying the move is permanent.
func Found(url string) Redirect {
	return Redirect{Status: http.StatusFound, URL: url}
}

// SeeOther answers 303: fetch URL with GET regardless of what this request
// was, which is the redirect a POST handler wants when it sends the client
// on to a result rather than answering with one directly.
func SeeOther(url string) Redirect {
	return Redirect{Status: http.StatusSeeOther, URL: url}
}

// TemporaryRedirect answers 307: the resource is at URL for now, and a
// client must repeat this request's method and body there rather than
// switching to GET.
func TemporaryRedirect(url string) Redirect {
	return Redirect{Status: http.StatusTemporaryRedirect, URL: url}
}

// PermanentRedirect answers 308: the resource now lives at URL for good,
// and a client must repeat this request's method and body there rather than
// switching to GET.
func PermanentRedirect(url string) Redirect {
	return Redirect{Status: http.StatusPermanentRedirect, URL: url}
}

// Spec reports nothing: Redirect's real status is chosen per instance from
// five genuinely different ones, and there is no default among them the way
// there is a natural 200 for Response. A route that answers with Redirect
// should declare the status or statuses it actually uses with Responds, so
// the OpenAPI document says something rather than nothing about it.
func (r Redirect) Spec() ResponseSpec {
	return ResponseSpec{}
}

// WriteResponse refuses a status outside the five Redirect means something
// for, rather than sending a Location header a client would not know how to
// interpret. This is the one check in this file that cannot happen at
// build: the status lives on the instance, which does not exist yet when
// compileResults runs.
func (r Redirect) WriteResponse(w http.ResponseWriter, req *http.Request) error {
	if !redirectStatuses[r.Status] {
		return fmt.Errorf("redirect status %d is not one of 301, 302, 303, 307, or 308", r.Status)
	}
	http.Redirect(w, req, r.URL, r.Status)
	return nil
}
