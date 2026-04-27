// Package qparam normalizes URL query-parameter values before they hit the wire.
//
// Currently it covers date parameters: the public API accepts millisecond-epoch
// integers, but humans prefer to type ISO-8601 ("2026-04-01T00:00:00Z" or
// "2026-04-01"). MaybeDate detects parameters whose names imply a timestamp
// (suffixes "AtFrom", "AtTo", "Since", "Before", "After") and converts ISO-8601
// inputs to millisecond strings; numeric inputs pass through unchanged.
package qparam

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// dateSuffixes lists name suffixes that mark a param as a timestamp.
// Match is case-sensitive — public API uses lowerCamelCase consistently.
var dateSuffixes = []string{"AtFrom", "AtTo", "Since", "Before", "After"}

// looksLikeDate reports whether the parameter name carries a timestamp.
func looksLikeDate(name string) bool {
	for _, s := range dateSuffixes {
		if strings.HasSuffix(name, s) {
			return true
		}
	}
	return false
}

// MaybeDate coerces a value if the param name implies a timestamp.
//
//   - All-digit input is treated as already-epoch-millis and passed through.
//   - ISO-8601 ("2026-04-01T00:00:00Z", "2026-04-01T00:00:00.123Z", "2026-04-01")
//     is parsed and converted to millis.
//   - Anything else returns a wrapped error naming the offending param.
//
// Non-date params return the input unchanged.
func MaybeDate(name, value string) (string, error) {
	if value == "" || !looksLikeDate(name) {
		return value, nil
	}
	if isAllDigits(value) {
		return value, nil
	}
	if t, ok := parseISO(value); ok {
		return strconv.FormatInt(t.UnixMilli(), 10), nil
	}
	return "", fmt.Errorf("--%s: %q is not a valid date (expected ISO-8601 or epoch-millis)", name, value)
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func parseISO(s string) (time.Time, bool) {
	layouts := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02T15:04:05",
		"2006-01-02",
	}
	for _, l := range layouts {
		if t, err := time.Parse(l, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}
