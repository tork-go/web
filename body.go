package tork

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/url"
	"reflect"
	"strings"
)

// defaultMaxBodyBytes is how much of a request body is read before it is
// refused. A megabyte is far more than a JSON document describing one resource
// needs and far less than an upload; an API that wants uploads says so with
// MaxBodyBytes rather than having every endpoint pay for the possibility.
const defaultMaxBodyBytes = 1 << 20

// multipartMemory is how much of a multipart form is held in memory before the
// rest goes to temporary files. It is not a limit on the request — MaxBodyBytes
// is — only on how much of one is buffered.
const multipartMemory = 10 << 20

// MaxBodyBytes limits how large a request body may be. Exceeding it answers
// 413 without reading the rest.
func MaxBodyBytes(limit int64) Option {
	return newOption("MaxBodyBytes", scopeApp, func(m *meta) error {
		if limit <= 0 {
			return fmt.Errorf("limit must be positive")
		}
		m.maxBodyBytes = limit
		return nil
	})
}

// StrictBodies rejects a JSON body carrying a field nothing declared.
//
// The default is to ignore them, because a client sending a field the server
// has not learned about yet is how APIs are extended without breaking anyone.
// Turn it on for an API where an unrecognised field more likely means a
// misspelled one.
func StrictBodies() Option {
	return newOption("StrictBodies", scopeApp, func(m *meta) error {
		m.strictBodies = true
		return nil
	})
}

// decodeBody reads the request body into dst.
//
// It separates the two ways a body can be wrong. A body that is the wrong
// media type or too large is a failure of the request itself and comes back as
// an error that ends the request; a body that is readable JSON of the wrong
// shape is a field problem, and joins whatever the parameters found so it can
// all be reported at once.
func (ex *exchange) decodeBody(dst reflect.Value) (fieldErrors, error) {
	if err := requireJSON(ex.request); err != nil {
		return nil, err
	}

	body, err := ex.readBody()
	if err != nil {
		return nil, err
	}
	if len(bytes.TrimSpace(body)) == 0 {
		return fieldErrors{{
			Field:   "body",
			Issue:   IssueBodyRequired,
			Message: "A request body is required.",
		}}, nil
	}

	target := dst.Addr().Interface()
	if ex.srv.strictBodies {
		decoder := json.NewDecoder(bytes.NewReader(body))
		decoder.DisallowUnknownFields()
		err = decoder.Decode(target)
	} else {
		err = json.Unmarshal(body, target)
	}
	if err != nil {
		return fieldErrors{bodyFieldError(err)}, nil
	}
	return nil, nil
}

// requireJSON refuses a body that does not claim to be JSON.
//
// A missing Content-Type is allowed: plenty of clients omit it, and the body
// still has to parse as JSON to get any further, so refusing it here would
// reject requests that are otherwise perfectly good.
func requireJSON(r *http.Request) error {
	declared := r.Header.Get("Content-Type")
	if declared == "" {
		return nil
	}

	mediaType, _, err := mime.ParseMediaType(declared)
	if err != nil || !isJSONMediaType(mediaType) {
		return NewError(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE",
			fmt.Sprintf("This endpoint reads application/json, not %q.", declared))
	}
	return nil
}

// isJSONMediaType accepts application/json and the +json structured suffix, so
// a client sending application/merge-patch+json is not turned away over a
// spelling this API does not care about.
func isJSONMediaType(mediaType string) bool {
	return mediaType == "application/json" || strings.HasSuffix(mediaType, "+json")
}

// readBody reads the whole body, refusing one that runs past the limit.
func (ex *exchange) readBody() ([]byte, error) {
	limited := http.MaxBytesReader(ex.writer, ex.request.Body, ex.srv.maxBodyBytes)
	body, err := io.ReadAll(limited)
	if err == nil {
		return body, nil
	}

	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return nil, NewError(http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
			fmt.Sprintf("The request body may be at most %d bytes.", ex.srv.maxBodyBytes))
	}
	return nil, BadRequest("The request body could not be read.")
}

// bodyFieldError turns what encoding/json refused into something a client can
// act on.
//
// A type mismatch is worth the most care: encoding/json already knows which
// field it was and what it wanted, so the answer can name the field rather
// than saying the body was bad and leaving the client to find out which part.
func bodyFieldError(err error) FieldError {
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) {
		field := mismatch.Field
		if field == "" {
			field = "body"
		}
		return FieldError{
			Field:   field,
			Issue:   IssueInvalidType,
			Message: fmt.Sprintf("%s must be %s.", field, jsonTypeName(mismatch.Type)),
		}
	}

	if name, ok := unknownFieldName(err); ok {
		return FieldError{
			Field:   name,
			Issue:   IssueUnknownField,
			Message: fmt.Sprintf("%s is not a field this endpoint accepts.", name),
		}
	}

	return FieldError{
		Field:   "body",
		Issue:   IssueInvalidJSON,
		Message: "The request body is not valid JSON.",
	}
}

// unknownFieldName recovers the field from the only error encoding/json
// reports as prose. There is no typed error for it, so the message is the
// only place the name exists.
func unknownFieldName(err error) (string, bool) {
	const prefix = "json: unknown field "
	message := err.Error()
	if !strings.HasPrefix(message, prefix) {
		return "", false
	}
	return strings.Trim(strings.TrimPrefix(message, prefix), `"`), true
}

// jsonTypeName says what a Go type is called in JSON, since that is the
// vocabulary the client is working in.
func jsonTypeName(t reflect.Type) string {
	switch t.Kind() {
	case reflect.String:
		return "a string"
	case reflect.Bool:
		return "true or false"
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return "a whole number"
	case reflect.Float32, reflect.Float64:
		return "a number"
	case reflect.Slice, reflect.Array:
		return "an array"
	default:
		return "an object"
	}
}

// formValues parses the request as a form and returns its fields.
//
// The result is cached, including the failure, because several fields read
// from one form and the body can only be read once.
func (ex *exchange) formValues() (url.Values, error) {
	ex.parseForm()
	return ex.form, ex.formErr
}

// formFiles is formValues for the uploaded files.
func (ex *exchange) formFiles() (map[string][]*multipart.FileHeader, error) {
	ex.parseForm()
	return ex.files, ex.formErr
}

// parseForm reads the body as a form, once.
func (ex *exchange) parseForm() {
	if ex.formParsed {
		return
	}
	ex.formParsed = true

	mediaType, _, err := mime.ParseMediaType(ex.request.Header.Get("Content-Type"))
	if err != nil {
		ex.formErr = NewError(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE",
			"This endpoint reads a form, and the request did not say it was sending one.")
		return
	}

	ex.request.Body = http.MaxBytesReader(ex.writer, ex.request.Body, ex.srv.maxBodyBytes)

	switch mediaType {
	case "multipart/form-data":
		err = ex.request.ParseMultipartForm(multipartMemory)
	case "application/x-www-form-urlencoded":
		err = ex.request.ParseForm()
	default:
		ex.formErr = NewError(http.StatusUnsupportedMediaType, "UNSUPPORTED_MEDIA_TYPE",
			fmt.Sprintf("This endpoint reads a form, not %q.", mediaType))
		return
	}
	if err != nil {
		ex.formErr = formParseError(err, ex.srv.maxBodyBytes)
		return
	}

	// PostForm holds a urlencoded body; a multipart one lands in
	// MultipartForm, and merging them here is what lets a binder read either
	// without knowing which arrived.
	ex.form = ex.request.PostForm
	if ex.request.MultipartForm != nil {
		ex.form = url.Values(ex.request.MultipartForm.Value)
		ex.files = ex.request.MultipartForm.File
	}
}

// formParseError separates a form too large to read from one that is merely
// malformed.
func formParseError(err error, limit int64) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return NewError(http.StatusRequestEntityTooLarge, "PAYLOAD_TOO_LARGE",
			fmt.Sprintf("The request body may be at most %d bytes.", limit))
	}
	return BadRequest("The form could not be read.")
}
