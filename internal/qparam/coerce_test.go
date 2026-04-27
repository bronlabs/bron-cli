package qparam

import (
	"strconv"
	"testing"
	"time"
)

func TestMaybeDate(t *testing.T) {
	iso := func(s string) string {
		t.Helper()
		layouts := []string{time.RFC3339, "2006-01-02"}
		for _, l := range layouts {
			if v, err := time.Parse(l, s); err == nil {
				return strconv.FormatInt(v.UnixMilli(), 10)
			}
		}
		t.Fatalf("test setup: cannot parse %q", s)
		return ""
	}

	cases := []struct {
		name, value, want string
		wantErr           bool
	}{
		{"limit", "100", "100", false},
		{"createdAtFrom", "1777311599505", "1777311599505", false},
		{"createdAtFrom", "2026-04-01T00:00:00Z", iso("2026-04-01T00:00:00Z"), false},
		{"createdAtTo", "2026-04-27T12:00:00Z", iso("2026-04-27T12:00:00Z"), false},
		{"updatedSince", "2026-04-01", iso("2026-04-01"), false},
		{"terminatedAtFrom", "not-a-date", "", true},
		{"createdAtFrom", "", "", false},
		{"limit", "not-a-date", "not-a-date", false},
	}
	for _, c := range cases {
		got, err := MaybeDate(c.name, c.value)
		if (err != nil) != c.wantErr {
			t.Fatalf("%s=%q: err=%v wantErr=%v", c.name, c.value, err, c.wantErr)
		}
		if !c.wantErr && got != c.want {
			t.Fatalf("%s=%q: got %q want %q", c.name, c.value, got, c.want)
		}
	}
}
