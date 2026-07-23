package tork

import (
	"encoding/json"
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
	w.Header().Set("Content-Type", contentTypeJSON)
	w.WriteHeader(status)
	_, err = w.Write(body)
	return err
}
