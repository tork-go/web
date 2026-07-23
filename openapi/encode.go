package openapi

import (
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// node is the ordered tree both emitters walk.
//
// The document model turns itself into one of these before anything is
// written, which is what makes JSON and YAML two renderings of the same thing
// rather than two independent traversals that can disagree. It also settles
// determinism once: a mapping carries its pairs in a slice, in the order the
// model chose, so nothing downstream ranges over a map and no emitter has to
// remember to sort.
type node struct {
	kind  nodeKind
	text  string // a scalar's rendering, already escaped for JSON
	pairs []pair // a mapping, in emission order
	items []*node
}

type nodeKind uint8

const (
	scalarNode nodeKind = iota
	stringNode
	mappingNode
	sequenceNode
)

type pair struct {
	key   string
	value *node
}

// The scalar constructors. Strings are kept apart from the other scalars
// because the two formats disagree about them and about nothing else: JSON
// quotes every string, YAML quotes one only when leaving it bare would change
// what it means.
func stringValue(s string) *node { return &node{kind: stringNode, text: s} }
func intValue(i int) *node       { return &node{kind: scalarNode, text: strconv.Itoa(i)} }
func boolValue(b bool) *node     { return &node{kind: scalarNode, text: strconv.FormatBool(b)} }
func floatValue(f float64) *node { return &node{kind: scalarNode, text: formatFloat(f)} }
func rawValue(text string) *node { return &node{kind: scalarNode, text: text} }
func nullValue() *node           { return &node{kind: scalarNode, text: "null"} }
func mapping() *node             { return &node{kind: mappingNode} }
func sequence(items ...*node) *node {
	return &node{kind: sequenceNode, items: items}
}

// formatFloat writes a number the way both formats read it back unchanged: a
// whole number keeps no decimal point, so a minimum of 1 is "1" rather than
// "1e+00", which a reader would not recognise as the bound that was written.
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// valueNode renders an arbitrary Go value — a default, an example, an entry in
// an enum — as a node.
//
// It goes through reflection rather than a type switch because these values
// arrive from the application's own field model, where a default is whatever
// type the field is, not one of a handful the document happens to anticipate.
// A value it cannot represent becomes its printed form, which is a truthful
// example even when it is not a faithful one.
func valueNode(v any) *node {
	if v == nil {
		return nullValue()
	}
	return reflectValueNode(reflect.ValueOf(v))
}

func reflectValueNode(v reflect.Value) *node {
	switch v.Kind() {
	case reflect.Bool:
		return boolValue(v.Bool())
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rawValue(strconv.FormatInt(v.Int(), 10))
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return rawValue(strconv.FormatUint(v.Uint(), 10))
	case reflect.Float32, reflect.Float64:
		return floatValue(v.Float())
	case reflect.String:
		return stringValue(v.String())
	case reflect.Pointer, reflect.Interface:
		if v.IsNil() {
			return nullValue()
		}
		return reflectValueNode(v.Elem())
	case reflect.Slice, reflect.Array:
		items := make([]*node, v.Len())
		for i := range v.Len() {
			items[i] = reflectValueNode(v.Index(i))
		}
		return sequence(items...)
	case reflect.Map:
		keys := make([]string, 0, v.Len())
		byKey := map[string]reflect.Value{}
		for _, k := range v.MapKeys() {
			key := fmt.Sprint(k.Interface())
			keys = append(keys, key)
			byKey[key] = v.MapIndex(k)
		}
		sort.Strings(keys)
		m := mapping()
		for _, k := range keys {
			m.set(k, reflectValueNode(byKey[k]))
		}
		return m
	default:
		return stringValue(fmt.Sprint(v.Interface()))
	}
}

// set adds a pair. Every optional field goes through one of the setIf helpers
// instead, so an unset field is absent from the document rather than present
// and empty — the difference between "this operation has no tags" and "this
// operation has an empty list of tags", which a reader should not have to
// guess at.
func (n *node) set(key string, value *node) {
	n.pairs = append(n.pairs, pair{key: key, value: value})
}

func (n *node) setString(key, value string) {
	if value != "" {
		n.set(key, stringValue(value))
	}
}

func (n *node) setBool(key string, value bool) {
	if value {
		n.set(key, boolValue(value))
	}
}

func (n *node) setInt(key string, value *int) {
	if value != nil {
		n.set(key, intValue(*value))
	}
}

func (n *node) setFloat(key string, value *float64) {
	if value != nil {
		n.set(key, floatValue(*value))
	}
}

func (n *node) setStrings(key string, values []string) {
	if len(values) == 0 {
		return
	}
	items := make([]*node, len(values))
	for i, v := range values {
		items[i] = stringValue(v)
	}
	n.set(key, sequence(items...))
}

// empty reports whether a mapping gained nothing, so a container that turned
// out to have no content is left out instead of written as {}.
func (n *node) empty() bool { return len(n.pairs) == 0 }

// JSON renders the tree as indented JSON.
//
// Indented rather than compact because a specification document is read by
// people and reviewed as a diff; the few hundred bytes of whitespace cost
// less than a one-line document nobody can review.
func (n *node) JSON() []byte {
	var b strings.Builder
	n.writeJSON(&b, 0)
	b.WriteByte('\n')
	return []byte(b.String())
}

func (n *node) writeJSON(b *strings.Builder, depth int) {
	switch n.kind {
	case stringNode:
		b.WriteString(quoteJSON(n.text))
	case scalarNode:
		b.WriteString(n.text)
	case sequenceNode:
		if len(n.items) == 0 {
			b.WriteString("[]")
			return
		}
		b.WriteString("[\n")
		for i, item := range n.items {
			writeIndent(b, depth+1)
			item.writeJSON(b, depth+1)
			if i < len(n.items)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		writeIndent(b, depth)
		b.WriteByte(']')
	case mappingNode:
		if len(n.pairs) == 0 {
			b.WriteString("{}")
			return
		}
		b.WriteString("{\n")
		for i, p := range n.pairs {
			writeIndent(b, depth+1)
			b.WriteString(quoteJSON(p.key))
			b.WriteString(": ")
			p.value.writeJSON(b, depth+1)
			if i < len(n.pairs)-1 {
				b.WriteByte(',')
			}
			b.WriteByte('\n')
		}
		writeIndent(b, depth)
		b.WriteByte('}')
	}
}

func writeIndent(b *strings.Builder, depth int) {
	for range depth {
		b.WriteString("  ")
	}
}

// YAML renders the tree as YAML.
//
// The subset a specification document needs is small — mappings, sequences,
// strings, numbers, booleans — which is why this is hand-rolled rather than a
// dependency: the whole emitter is shorter than the code that would be needed
// to configure a general one, and the runtime keeps its promise of importing
// nothing outside the standard library.
func (n *node) YAML() []byte {
	var b strings.Builder
	n.writeYAML(&b, 0)
	return []byte(b.String())
}

func (n *node) writeYAML(b *strings.Builder, depth int) {
	switch n.kind {
	case mappingNode:
		for _, p := range n.pairs {
			writeYAMLIndent(b, depth)
			b.WriteString(quoteYAMLKey(p.key))
			b.WriteByte(':')
			n.writeYAMLChild(b, depth, p.value)
		}
	case sequenceNode:
		for _, item := range n.items {
			writeYAMLIndent(b, depth)
			b.WriteByte('-')
			n.writeYAMLChild(b, depth, item)
		}
	}
}

// writeYAMLChild writes whatever follows a "key:" or a "-": a scalar on the
// same line, a nested block on the lines under it, and the explicit empty
// forms when a collection has nothing in it — since a bare "key:" would read
// back as null rather than as an empty collection.
func (n *node) writeYAMLChild(b *strings.Builder, depth int, value *node) {
	switch value.kind {
	case stringNode:
		b.WriteByte(' ')
		b.WriteString(quoteYAML(value.text))
		b.WriteByte('\n')
	case scalarNode:
		b.WriteByte(' ')
		b.WriteString(value.text)
		b.WriteByte('\n')
	case mappingNode:
		if value.empty() {
			b.WriteString(" {}\n")
			return
		}
		b.WriteByte('\n')
		value.writeYAML(b, depth+1)
	case sequenceNode:
		if len(value.items) == 0 {
			b.WriteString(" []\n")
			return
		}
		b.WriteByte('\n')
		value.writeYAML(b, depth+1)
	}
}

func writeYAMLIndent(b *strings.Builder, depth int) {
	for range depth {
		b.WriteString("  ")
	}
}

// quoteJSON escapes a string the way JSON requires.
//
// It is written out rather than delegated to encoding/json because that
// escapes <, >, and & as well, which is right for a string being embedded in
// HTML and wrong for a document meant to be read: a description containing a
// "<" should say so.
func quoteJSON(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 {
				b.WriteString(`\u`)
				b.WriteString(strconv.FormatInt(int64(r), 16))
				continue
			}
			b.WriteRune(r)
		}
	}
	b.WriteByte('"')
	return b.String()
}

// quoteYAML writes a string bare when that reads back as the same string, and
// double-quoted when it would not.
//
// Quoting only when it changes the meaning is what keeps a document readable;
// the cases that force quotes are the ones where YAML would otherwise see a
// number, a boolean, null, a comment, or the start of a structure.
func quoteYAML(s string) string {
	if needsYAMLQuotes(s) {
		return quoteJSON(s)
	}
	return s
}

// quoteYAMLKey is quoteYAML for the left of a colon. A key is held to the same
// rule; the document's own keys never need quoting, but a schema's property
// names come from the application and can be anything.
func quoteYAMLKey(s string) string { return quoteYAML(s) }

// needsYAMLQuotes reports whether leaving s bare would change what it means.
func needsYAMLQuotes(s string) bool {
	if s == "" {
		return true
	}
	switch strings.ToLower(s) {
	case "true", "false", "null", "yes", "no", "on", "off", "~":
		return true
	}
	if looksNumeric(s) {
		return true
	}
	// A leading indicator character starts a structure, a comment, an anchor,
	// or a quoted scalar, and surrounding space is not preserved bare.
	if strings.ContainsRune("-?:,[]{}#&*!|>'\"%@`", rune(s[0])) {
		return true
	}
	if s != strings.TrimSpace(s) {
		return true
	}
	// ": " opens a mapping and " #" opens a comment wherever they appear.
	if strings.Contains(s, ": ") || strings.Contains(s, " #") || strings.HasSuffix(s, ":") {
		return true
	}
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' || (r < 0x20) {
			return true
		}
		if r == utf8.RuneError || !unicode.IsPrint(r) {
			return true
		}
	}
	return false
}

// looksNumeric reports whether YAML would read s as a number rather than as
// the string it was written as.
func looksNumeric(s string) bool {
	if _, err := strconv.ParseFloat(s, 64); err == nil {
		return true
	}
	_, err := strconv.ParseInt(s, 0, 64)
	return err == nil
}
