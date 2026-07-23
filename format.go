package tork

import (
	"encoding/base64"
	"encoding/json"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"unicode"
)

// The format checks the string rules are built from.
//
// They are ordinary predicates rather than one regular expression each,
// because most of these have an exact definition somewhere and a regular
// expression that looks right is the usual way to get it subtly wrong. Where a
// standard library package already knows the answer — an IP address, a URL,
// base64, JSON — it is asked rather than reimplemented.

// isEmail is deliberately loose: something, an @, something with a dot in it.
//
// Anything stricter rejects addresses that are perfectly valid — the grammar
// permits quoted strings, plus tags, and hosts most validators refuse — and
// the only real proof an address works is sending to it. This catches the
// typo, which is what it is for.
func isEmail(value string) bool {
	local, domain, found := strings.Cut(value, "@")
	if !found || local == "" || strings.Contains(local, "@") {
		return false
	}
	return strings.Contains(domain, ".") &&
		!strings.HasPrefix(domain, ".") && !strings.HasSuffix(domain, ".") &&
		!strings.Contains(domain, "..") && !strings.ContainsAny(domain, " \t")
}

// isUUID accepts the canonical 8-4-4-4-12 hex form, in either case.
func isUUID(value string) bool {
	if len(value) != 36 {
		return false
	}
	for i, r := range value {
		switch i {
		case 8, 13, 18, 23:
			if r != '-' {
				return false
			}
		default:
			if !isHexDigit(r) {
				return false
			}
		}
	}
	return true
}

// isURL requires a scheme and a host, so that a bare path is not mistaken for
// something a client could follow.
func isURL(value string) bool {
	parsed, err := url.Parse(value)
	return err == nil && parsed.Scheme != "" && parsed.Host != ""
}

// isIP accepts either family; isIPv4 and isIPv6 insist on one.
func isIP(value string) bool {
	_, err := netip.ParseAddr(value)
	return err == nil
}

func isIPv4(value string) bool {
	address, err := netip.ParseAddr(value)
	return err == nil && address.Is4()
}

func isIPv6(value string) bool {
	address, err := netip.ParseAddr(value)
	return err == nil && address.Is6() && !address.Is4In6()
}

// isCIDR accepts an address with a prefix length, as a route or a subnet is
// written.
func isCIDR(value string) bool {
	_, err := netip.ParsePrefix(value)
	return err == nil
}

// isHostname follows RFC 1123: dot-separated labels of letters, digits, and
// hyphens, none of which begins or ends with a hyphen.
func isHostname(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(strings.TrimSuffix(value, "."), ".") {
		if !isHostnameLabel(label) {
			return false
		}
	}
	return true
}

func isHostnameLabel(label string) bool {
	if label == "" || len(label) > 63 ||
		strings.HasPrefix(label, "-") || strings.HasSuffix(label, "-") {
		return false
	}
	for _, r := range label {
		if !isASCIILetter(r) && !isDigit(r) && r != '-' {
			return false
		}
	}
	return true
}

// semverPattern is the expression the semver specification itself publishes.
var semverPattern = regexp.MustCompile(
	`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)` +
		`(?:-((?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*)(?:\.(?:0|[1-9]\d*|\d*[a-zA-Z-][0-9a-zA-Z-]*))*))?` +
		`(?:\+([0-9a-zA-Z-]+(?:\.[0-9a-zA-Z-]+)*))?$`)

func isSemver(value string) bool { return semverPattern.MatchString(value) }

// isSlug is the form a URL segment is usually reduced to: lower-case letters,
// digits, and single hyphens between them.
func isSlug(value string) bool {
	if value == "" || strings.HasPrefix(value, "-") || strings.HasSuffix(value, "-") ||
		strings.Contains(value, "--") {
		return false
	}
	for _, r := range value {
		if !isLowerLetter(r) && !isDigit(r) && r != '-' {
			return false
		}
	}
	return true
}

// isCreditCard applies the Luhn check, which is what a card number's last
// digit exists for. It says the number is well formed, not that it is real.
func isCreditCard(value string) bool {
	digits := 0
	sum := 0
	doubling := false

	for i := len(value) - 1; i >= 0; i-- {
		r := rune(value[i])
		if r == ' ' || r == '-' {
			continue
		}
		if !isDigit(r) {
			return false
		}

		digit := int(r - '0')
		if doubling {
			if digit *= 2; digit > 9 {
				digit -= 9
			}
		}
		sum += digit
		doubling = !doubling
		digits++
	}

	return digits >= 12 && digits <= 19 && sum%10 == 0
}

func isBase64(value string) bool {
	_, err := base64.StdEncoding.DecodeString(value)
	return err == nil
}

func isJSON(value string) bool { return json.Valid([]byte(value)) }

// isAlpha, isAlphanumeric, and isNumeric are about letters and digits in the
// Unicode sense, so a name in any script passes.
func isAlpha(value string) bool {
	return value != "" && allRunes(value, unicode.IsLetter)
}

func isAlphanumeric(value string) bool {
	return value != "" && allRunes(value, func(r rune) bool {
		return unicode.IsLetter(r) || unicode.IsDigit(r)
	})
}

func isNumeric(value string) bool {
	return value != "" && allRunes(value, unicode.IsDigit)
}

func isASCII(value string) bool {
	return allRunes(value, func(r rune) bool { return r <= unicode.MaxASCII })
}

func isHex(value string) bool {
	return value != "" && allRunes(value, isHexDigit)
}

// isLower and isUpper ask whether the value is already in that case, which is
// not the same as having no letters at all: a value of only digits is both.
func isLower(value string) bool { return value == strings.ToLower(value) }

func isUpper(value string) bool { return value == strings.ToUpper(value) }

// isNotBlank refuses a value that is only whitespace, which a length rule
// alone would let through.
func isNotBlank(value string) bool { return strings.TrimSpace(value) != "" }

func allRunes(value string, ok func(rune) bool) bool {
	for _, r := range value {
		if !ok(r) {
			return false
		}
	}
	return true
}

func isHexDigit(r rune) bool {
	return isDigit(r) || (r >= 'a' && r <= 'f') || (r >= 'A' && r <= 'F')
}

func isDigit(r rune) bool { return r >= '0' && r <= '9' }

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

func isLowerLetter(r rune) bool { return r >= 'a' && r <= 'z' }
