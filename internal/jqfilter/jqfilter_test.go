package jqfilter

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"
)

func mustJSON(t *testing.T, v interface{}) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func sample() interface{} {
	return map[string]interface{}{
		"transactions": []interface{}{
			map[string]interface{}{"transactionId": "t1", "status": "completed", "params": map[string]interface{}{"amount": "0.5"}},
			map[string]interface{}{"transactionId": "t2", "status": "pending", "params": map[string]interface{}{"amount": "1.5"}},
		},
	}
}

func TestReshapeAndFilter(t *testing.T) {
	tests := []struct {
		name, prog, want string
	}{
		// gojq emits plain maps; encoding/json sorts their keys, so the
		// expected order is alphabetical (amt, id) regardless of jq's {…} order.
		{"project+rename", `[.transactions[] | {id: .transactionId, amt: .params.amount}]`,
			`[{"amt":"0.5","id":"t1"},{"amt":"1.5","id":"t2"}]`},
		{"select rows", `[.transactions[] | select(.status=="pending") | .transactionId]`,
			`["t2"]`},
		{"length aggregate", `.transactions | length`, `2`},
		{"single value unwrapped", `.transactions[0].transactionId`, `"t1"`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := Run(tt.prog, sample())
			if err != nil {
				t.Fatalf("Run err: %v", err)
			}
			if g := mustJSON(t, got); g != tt.want {
				t.Fatalf("got %s want %s", g, tt.want)
			}
		})
	}
}

func TestEnvIsLocked(t *testing.T) {
	os.Setenv("BRON_API_KEY", "SECRET-should-never-leak")
	defer os.Unsetenv("BRON_API_KEY")

	for _, prog := range []string{`$ENV.BRON_API_KEY`, `env.BRON_API_KEY`, `$ENV`, `env`} {
		got, err := Run(prog, sample())
		if err != nil {
			continue // a rejected program is also acceptable
		}
		if strings.Contains(mustJSON(t, got), "SECRET") {
			t.Fatalf("env leaked via %q: %s", prog, mustJSON(t, got))
		}
	}
}

func TestStdinIsRejected(t *testing.T) {
	for _, prog := range []string{`input`, `inputs`, `[inputs]`} {
		if _, err := Run(prog, sample()); err == nil {
			t.Fatalf("expected %q to be rejected", prog)
		}
	}
}

func TestTimeoutBitesInfiniteProgram(t *testing.T) {
	start := time.Now()
	_, err := RunWithLimits(`repeat(.)`, 1, 200*time.Millisecond, DefaultMaxResults)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("timeout did not bite promptly: %s", d)
	}
	if !strings.Contains(err.Error(), "timed out") && !strings.Contains(err.Error(), "too many") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestOutputCapBitesExplodingProgram(t *testing.T) {
	_, err := RunWithLimits(`range(100000)`, 1, 2*time.Second, 100)
	if err == nil {
		t.Fatal("expected too-many-results error")
	}
	if !strings.Contains(err.Error(), "too many") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInvalidProgramSurfacesError(t *testing.T) {
	_, err := Run(`.foo |`, sample())
	if err == nil || !strings.Contains(err.Error(), "invalid jq") {
		t.Fatalf("expected invalid jq error, got %v", err)
	}
}

func TestEmptyOutputIsNil(t *testing.T) {
	got, err := Run(`.transactions[] | select(.status=="nope")`, sample())
	if err != nil {
		t.Fatal(err)
	}
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}
