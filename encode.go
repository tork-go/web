package tork

import (
	"encoding/json"
	"maps"
	"net/http"
)

// contentTypeJSON is what every JSON response says it is. The charset
// parameter is omitted deliberately: JSON is UTF-8 by definition, and saying
// so again is noise every response would carry.
const contentTypeJSON = "application/json"

// writeJSON serializes v and writes it as the whole response.
//
// The value is marshalled before anything is written, which is the reason
// this is a function rather than a json.Encoder aimed at the
// ResponseWriter: an encoder that fails halfway has already sent a status
// and half a body, and there is no way to take either back. Marshalling
// first means a value that cannot be encoded is still a clean failure.
func writeJSON(w http.ResponseWriter, status int, v any) error {
	body, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return writeBody(w, status, contentTypeJSON, nil, body)
}

// writeBody writes a status, a content type, any extra headers, and a body
// already reduced to bytes.
//
// It exists so that every response type with a body it can hold in memory —
// writeJSON's plain T, Response's JSON body, RawResponse's bytes — writes
// the same way instead of each repeating the header-then-status-then-body
// order by hand.
func writeBody(w http.ResponseWriter, status int, contentType string, headers http.Header, body []byte) error {
	h := w.Header()
	maps.Copy(h, headers)
	if contentType != "" {
		h.Set("Content-Type", contentType)
	}
	w.WriteHeader(status)
	_, err := w.Write(body)
	return err
}
