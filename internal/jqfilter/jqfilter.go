// Package jqfilter runs a jq program over an already-decoded JSON value in a
// locked-down sandbox. It backs the MCP server's optional `jq` argument, which
// lets an agent reshape, filter and aggregate a tool's reply server-side so
// only the needed data enters its context.
//
// The host process holds the caller's BRON_API_KEY in its environment, so the
// sandbox is deliberately strict: no environment access (gojq's `env`/`$ENV`
// resolve to empty), no stdin (`input`/`inputs` are rejected at compile time),
// no module imports, a wall-clock timeout against runaway programs, and a cap
// on emitted values so a filter can't blow up the context it is meant to
// shrink.
package jqfilter

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/itchyny/gojq"
)

// Defaults applied by Run. Exposed as vars so callers/tests can tighten them.
var (
	// DefaultTimeout bounds a single jq evaluation. gojq honours context
	// cancellation between steps, so this bites infinite generators
	// (`repeat(.)`, `range(1e12)`).
	DefaultTimeout = 2 * time.Second

	// DefaultMaxResults caps how many top-level values the program may emit
	// before Run aborts. A jq filter can multiply output (`[range(1e6)]`);
	// the whole point here is to shrink context, so an exploding result is
	// treated as an error, not silently truncated.
	DefaultMaxResults = 10000
)

// ErrTooManyResults is returned when a program emits more than the cap.
var ErrTooManyResults = errors.New("jq produced too many results")

// lockedEnviron feeds gojq an empty environment so `env` / `$ENV` can never
// surface the host's secrets (BRON_API_KEY et al.).
func lockedEnviron() []string { return nil }

// Run compiles and evaluates program against input and returns the produced
// values. A program emitting exactly one value returns that value unwrapped
// (the common reshape/filter case); zero values returns nil; multiple values
// returns a []interface{} stream. Compile errors surface verbatim so a calling
// agent can correct its own filter.
func Run(program string, input interface{}) (interface{}, error) {
	return RunWithLimits(program, input, DefaultTimeout, DefaultMaxResults)
}

// RunWithLimits is Run with explicit limits — used by tests.
func RunWithLimits(program string, input interface{}, timeout time.Duration, maxResults int) (interface{}, error) {
	query, err := gojq.Parse(program)
	if err != nil {
		return nil, fmt.Errorf("invalid jq: %w", err)
	}

	code, err := gojq.Compile(query, gojq.WithEnvironLoader(lockedEnviron))
	if err != nil {
		return nil, fmt.Errorf("invalid jq: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	iter := code.RunWithContext(ctx, input)
	var out []interface{}
	for {
		v, ok := iter.Next()
		if !ok {
			break
		}
		if err, ok := v.(error); ok {
			if errors.Is(err, context.DeadlineExceeded) {
				return nil, fmt.Errorf("jq timed out after %s", timeout)
			}
			var halt *gojq.HaltError
			if errors.As(err, &halt) {
				return nil, fmt.Errorf("jq halted: %w", err)
			}
			return nil, fmt.Errorf("jq error: %w", err)
		}
		out = append(out, v)
		if len(out) > maxResults {
			return nil, ErrTooManyResults
		}
	}

	switch len(out) {
	case 0:
		return nil, nil
	case 1:
		return out[0], nil
	default:
		return out, nil
	}
}
