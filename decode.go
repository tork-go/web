package tork

import (
	"encoding"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"time"
)

var (
	timeType        = reflect.TypeFor[time.Time]()
	durationType    = reflect.TypeFor[time.Duration]()
	textUnmarshaler = reflect.TypeFor[encoding.TextUnmarshaler]()
)

// decodeFunc fills one field from every value a source produced for its name.
//
// It takes the whole slice rather than one string so that a repeated parameter
// filling a []string and a single parameter filling a string are the same kind
// of thing, decided once at startup instead of branched on per request. It is
// only ever called with at least one value: an absent parameter is settled
// before this, by the default or by leaving the field zero.
type decodeFunc func(dst reflect.Value, raw []string) *decodeError

// scalarDecoder fills one field from one string.
type scalarDecoder func(dst reflect.Value, raw string) *decodeError

// decodeError is a value the client sent that could not be read as the field's
// type. It carries the issue code and the predicate a message is built from,
// so that every "must be a whole number" in the API is spelled identically and
// the field's name is attached by whoever knows it.
type decodeError struct {
	issue     string
	predicate string
}

func (e *decodeError) Error() string { return e.predicate }

// cannot is the one way this package reports an unreadable value.
func cannot(issue, predicate string) *decodeError {
	return &decodeError{issue: issue, predicate: predicate}
}

// decoderFor builds the decoder for a field's type, unwrapping the three
// containers a parameter may be written as before reaching the value itself.
func decoderFor(t reflect.Type) (decodeFunc, error) {
	if reflect.PointerTo(t).Implements(reflect.TypeFor[optionalTarget]()) {
		return optionalDecoder(t)
	}

	switch t.Kind() {
	case reflect.Pointer:
		return pointerDecoder(t)
	case reflect.Slice:
		// A []byte is a string that happens to be spelled as a slice, not a
		// repeated parameter, and treating it as one would be surprising.
		if t.Elem().Kind() != reflect.Uint8 {
			return sliceDecoder(t)
		}
	}

	scalar, err := scalarDecoderFor(t)
	if err != nil {
		return nil, err
	}
	// Only the last value is kept when a parameter arrives more than once.
	// Something has to be chosen, and last-wins is what a client overriding
	// an earlier value would expect.
	return func(dst reflect.Value, raw []string) *decodeError {
		return scalar(dst, raw[len(raw)-1])
	}, nil
}

// optionalDecoder fills an Optional, recording that the value was present.
//
// The element type and the addresses to write through both come from the
// unexported target method, which is what lets this work without knowing the
// Optional's type parameter.
func optionalDecoder(t reflect.Type) (decodeFunc, error) {
	value, _, _ := reflect.New(t).Interface().(optionalTarget).target()
	inner, err := decoderFor(reflect.TypeOf(value).Elem())
	if err != nil {
		return nil, err
	}

	return func(dst reflect.Value, raw []string) *decodeError {
		value, set, _ := dst.Addr().Interface().(optionalTarget).target()
		if err := inner(reflect.ValueOf(value).Elem(), raw); err != nil {
			return err
		}
		*set = true
		return nil
	}, nil
}

// pointerDecoder allocates and fills what the pointer points at, which is how
// a field says "absent is a state I care about" without an Optional.
func pointerDecoder(t reflect.Type) (decodeFunc, error) {
	inner, err := decoderFor(t.Elem())
	if err != nil {
		return nil, err
	}

	return func(dst reflect.Value, raw []string) *decodeError {
		allocated := reflect.New(t.Elem())
		if err := inner(allocated.Elem(), raw); err != nil {
			return err
		}
		dst.Set(allocated)
		return nil
	}, nil
}

// sliceDecoder fills one element per value, which is what a repeated parameter
// produces and what the csv modifier turns a single value into.
func sliceDecoder(t reflect.Type) (decodeFunc, error) {
	scalar, err := scalarDecoderFor(t.Elem())
	if err != nil {
		return nil, err
	}

	return func(dst reflect.Value, raw []string) *decodeError {
		values := reflect.MakeSlice(t, len(raw), len(raw))
		for i, one := range raw {
			if err := scalar(values.Index(i), one); err != nil {
				return err
			}
		}
		dst.Set(values)
		return nil
	}, nil
}

// scalarDecoderFor picks how one string becomes one value.
//
// The two time types come before the general text case although both satisfy
// it, because "must be an RFC 3339 timestamp" is worth saying and "must be in
// a format this type understands" is not.
func scalarDecoderFor(t reflect.Type) (scalarDecoder, error) {
	switch t {
	case timeType:
		return decodeTime, nil
	case durationType:
		return decodeDuration, nil
	}

	if reflect.PointerTo(t).Implements(textUnmarshaler) {
		return decodeText, nil
	}

	switch t.Kind() {
	case reflect.String:
		return func(dst reflect.Value, raw string) *decodeError {
			dst.SetString(raw)
			return nil
		}, nil
	case reflect.Bool:
		return decodeBool, nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return decodeInt(t), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return decodeUint(t), nil
	case reflect.Float32, reflect.Float64:
		return decodeFloat(t), nil
	}

	return nil, fmt.Errorf("no parameter can be read into a %s", t)
}

func decodeTime(dst reflect.Value, raw string) *decodeError {
	parsed, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return cannot(IssueInvalidDateTime, "must be an RFC 3339 timestamp")
	}
	dst.Set(reflect.ValueOf(parsed))
	return nil
}

func decodeDuration(dst reflect.Value, raw string) *decodeError {
	parsed, err := time.ParseDuration(raw)
	if err != nil {
		return cannot(IssueInvalidDuration, "must be a duration such as 1h30m")
	}
	dst.SetInt(int64(parsed))
	return nil
}

// decodeText hands the value to the type itself, which is how a domain type
// with its own textual form — an ID, an enum, a currency — binds without this
// package knowing anything about it.
func decodeText(dst reflect.Value, raw string) *decodeError {
	if err := dst.Addr().Interface().(encoding.TextUnmarshaler).UnmarshalText([]byte(raw)); err != nil {
		return cannot(IssueInvalidFormat, "is not in a form this field accepts")
	}
	return nil
}

// decodeBool accepts what a query string actually carries. strconv.ParseBool
// already takes 1, t, T, true, and their negatives; "yes" and "on" are added
// because HTML forms send them and a checkbox is the commonest boolean there
// is.
func decodeBool(dst reflect.Value, raw string) *decodeError {
	switch strings.ToLower(raw) {
	case "yes", "on":
		dst.SetBool(true)
		return nil
	case "no", "off":
		dst.SetBool(false)
		return nil
	}

	parsed, err := strconv.ParseBool(raw)
	if err != nil {
		return cannot(IssueInvalidBoolean, "must be true or false")
	}
	dst.SetBool(parsed)
	return nil
}

// decodeInt parses within the field's own width, so a value that would not fit
// an int8 is rejected rather than silently truncated.
func decodeInt(t reflect.Type) scalarDecoder {
	bits := t.Bits()
	return func(dst reflect.Value, raw string) *decodeError {
		parsed, err := strconv.ParseInt(raw, 10, bits)
		if err != nil {
			return cannot(IssueInvalidInteger, "must be a whole number")
		}
		dst.SetInt(parsed)
		return nil
	}
}

func decodeUint(t reflect.Type) scalarDecoder {
	bits := t.Bits()
	return func(dst reflect.Value, raw string) *decodeError {
		parsed, err := strconv.ParseUint(raw, 10, bits)
		if err != nil {
			return cannot(IssueInvalidInteger, "must be a whole number that is not negative")
		}
		dst.SetUint(parsed)
		return nil
	}
}

func decodeFloat(t reflect.Type) scalarDecoder {
	bits := t.Bits()
	return func(dst reflect.Value, raw string) *decodeError {
		parsed, err := strconv.ParseFloat(raw, bits)
		if err != nil {
			return cannot(IssueInvalidNumber, "must be a number")
		}
		dst.SetFloat(parsed)
		return nil
	}
}
