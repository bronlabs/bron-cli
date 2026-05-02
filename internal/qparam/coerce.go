// Package qparam normalizes URL query-parameter values before they hit the wire.
//
// Covers `@EpochMillis` timestamp fields: the public API accepts millisecond-
// epoch integers (Long on the JVM side), but humans and agents prefer ISO-8601
// ("2026-04-01T00:00:00Z" or "2026-04-01"). MaybeDate detects parameters and
// body fields that the spec marks with `format: "date-time-millis"` and
// converts ISO-8601 inputs to millisecond strings; numeric inputs pass through
// unchanged.
//
// The set of recognized date-keyed names is loaded at startup from the
// embedded OpenAPI spec via SetDateKeys — there is no hardcoded suffix list,
// so any new `@EpochMillis` field added on the backend automatically picks
// up coercion as soon as `make spec` regenerates the SDK.
package qparam

import (
	"fmt"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// dateKeys holds the registered date-shaped field/parameter names. Stored as
// an atomic.Pointer so SetDateKeys / IsDateParam are safe to call concurrently
// (initialization happens once at startup, but tools may run goroutines).
var dateKeys atomic.Pointer[map[string]bool]

// SetDateKeys registers the set of field/parameter names whose values should
// be coerced from ISO-8601 to epoch-millis. Idempotent — call once at startup
// with the union of body-property names + query-parameter names extracted
// from the spec.
//
// Pass an empty/nil map to disable coercion entirely (mainly useful in tests
// that exercise the raw passthrough path).
func SetDateKeys(set map[string]bool) {
	if set == nil {
		set = map[string]bool{}
	}
	dateKeys.Store(&set)
}

// IsDateParam reports whether the parameter / field name carries a timestamp,
// per the spec-driven registry populated by SetDateKeys.
func IsDateParam(name string) bool {
	if name == "" {
		return false
	}
	keys := dateKeys.Load()
	if keys == nil {
		return false
	}
	return (*keys)[name]
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
	if value == "" || !IsDateParam(name) {
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

// CoerceBodyDates walks a JSON-shaped value tree and rewrites every
// timestamp-shaped field (per IsDateParam) from ISO-8601 to epoch-millis as a
// string. Mirrors what compactQuery does for URL query params; lets the
// CLI-generated write path (cligen-emitted body.Compose calls) accept ISO-8601
// in `--params.expiresAt` flags and the MCP path accept it in JSON body
// values, both transparently.
//
// Recurses into nested maps and arrays; mutates in place. No-op on nil or
// non-object roots — safe to call after Compose returned a typed Go scalar.
func CoerceBodyDates(payload interface{}) error {
	if payload == nil {
		return nil
	}
	m, ok := payload.(map[string]interface{})
	if !ok {
		return nil
	}
	return coerceMap(m)
}

func coerceMap(m map[string]interface{}) error {
	for k, v := range m {
		if s, ok := v.(string); ok && IsDateParam(k) {
			coerced, err := MaybeDate(k, s)
			if err != nil {
				return err
			}
			m[k] = coerced
			continue
		}
		if nested, ok := v.(map[string]interface{}); ok {
			if err := coerceMap(nested); err != nil {
				return err
			}
			continue
		}
		if arr, ok := v.([]interface{}); ok {
			for _, item := range arr {
				if mm, ok := item.(map[string]interface{}); ok {
					if err := coerceMap(mm); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// ValidateEnum rejects raw values that aren't members of allowed.
//
// `repeat` true means the flag accepts a comma-separated list (the OpenAPI
// array shape, e.g. `--statuses signing-required,waiting-approval`); each
// element is checked individually. False means a single scalar.
//
// Empty value is always valid — the flag is unset and the param won't be
// sent. Unknown elements produce a single error with the offending value
// and the full allowed list, so the user sees the fix in one read.
func ValidateEnum(name, raw string, allowed []string, repeat bool) error {
	if raw == "" {
		return nil
	}
	values := []string{raw}
	if repeat {
		values = strings.Split(raw, ",")
	}
	for _, v := range values {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		ok := false
		for _, a := range allowed {
			if v == a {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("--%s: invalid value %q (allowed: %s)", name, v, strings.Join(allowed, ", "))
		}
	}
	return nil
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
