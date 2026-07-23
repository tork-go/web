package tork

import "strings"

// lowerCamel is the wire name a field gets when its tag names none.
//
// The field name is split into words and rejoined lower-camel: PageSize
// becomes pageSize, ItemID becomes itemId, HTTPServer becomes httpServer. The
// initialism is deliberately not preserved — itemID and itemId would both be
// defensible, and picking one that never varies is worth more than picking the
// one a given reader prefers, because the answer is a wire contract.
// It is only ever called with a struct field's name, which is never empty, so
// there is no empty case to guard.
func lowerCamel(name string) string {
	words := splitWords(name)

	var b strings.Builder
	b.WriteString(strings.ToLower(words[0]))
	for _, word := range words[1:] {
		b.WriteString(strings.ToUpper(word[:1]))
		b.WriteString(strings.ToLower(word[1:]))
	}
	return b.String()
}

// splitWords breaks a Go identifier at its case boundaries.
//
// There are two boundaries. One is between a lower-case or digit and an
// upper-case letter, which is where PageSize divides. The other is inside a
// run of capitals, before the last one, when a lower-case letter follows it:
// that is what keeps HTTPServer from becoming one word, since the S belongs to
// Server and the HTTP before it does not.
func splitWords(name string) []string {
	runes := []rune(name)

	var words []string
	start := 0
	for i := 1; i < len(runes); i++ {
		switch {
		case !isUpperRune(runes[i]):
			continue
		case !isUpperRune(runes[i-1]):
			// lower-then-upper: "e|S" in PageSize.
		case i+1 < len(runes) && !isUpperRune(runes[i+1]):
			// upper-then-upper-then-lower: "P|Se" in HTTPServer.
		default:
			continue
		}
		words = append(words, string(runes[start:i]))
		start = i
	}
	if start < len(runes) {
		words = append(words, string(runes[start:]))
	}
	return words
}

func isUpperRune(r rune) bool { return r >= 'A' && r <= 'Z' }
