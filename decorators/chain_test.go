package decorators

import (
	"slices"
	"testing"
)

// mark returns a middleware that records entry/exit around proceed.
func mark(log *[]string, name string) Middleware {
	return func(proceed func()) {
		*log = append(*log, name+">")
		proceed()
		*log = append(*log, "<"+name)
	}
}

func TestChainOrder(t *testing.T) {
	var log []string
	fn := Chain(func(x int) int {
		log = append(log, "fn")
		return x * 2
	}, mark(&log, "a"), mark(&log, "b"))

	if got := fn(21); got != 42 {
		t.Errorf("fn(21) = %d, want 42", got)
	}
	want := []string{"a>", "b>", "fn", "<b", "<a"} // mws[0] outermost
	if !slices.Equal(log, want) {
		t.Errorf("order = %v, want %v", log, want)
	}
}

func TestChainShortCircuit(t *testing.T) {
	ran := false
	fn := Chain(func() (int, string) {
		ran = true
		return 7, "x"
	}, func(proceed func()) { /* never proceeds */ })

	a, b := fn()
	if ran || a != 0 || b != "" {
		t.Errorf("short-circuit: ran=%v results=(%d,%q), want zero values and no run", ran, a, b)
	}
}

func TestChainVariadic(t *testing.T) {
	fn := Chain(func(base int, ns ...int) int {
		for _, n := range ns {
			base += n
		}
		return base
	}, func(p func()) { p() })
	if got := fn(1, 2, 3, 4); got != 10 {
		t.Errorf("variadic through Chain = %d, want 10", got)
	}
}

func TestChainEmptyReturnsFn(t *testing.T) {
	calls := 0
	orig := func() { calls++ }
	if got := Chain(orig); got == nil {
		t.Fatal("Chain with no middleware returned nil")
	} else {
		got()
	}
	if calls != 1 {
		t.Errorf("Chain() must return fn unchanged; calls = %d", calls)
	}
}

// TestRunRetryReplaysDownstream: a middleware calling proceed twice must
// re-run everything BELOW it — including the call — both times.
func TestRunRetryReplaysDownstream(t *testing.T) {
	var log []string
	retry := func(proceed func()) {
		proceed()
		proceed()
	}
	Run([]Middleware{mark(&log, "outer"), retry, mark(&log, "inner")}, func() {
		log = append(log, "call")
	})
	want := []string{
		"outer>",
		"inner>", "call", "<inner",
		"inner>", "call", "<inner",
		"<outer",
	}
	if !slices.Equal(log, want) {
		t.Errorf("retry order = %v, want %v", log, want)
	}
}

// TestRunRetryAfterPanic: the chain position must be restored even when a
// downstream middleware panics and an upstream one recovers and retries.
func TestRunRetryAfterPanic(t *testing.T) {
	var log []string
	attempts := 0
	guard := func(proceed func()) {
		func() {
			defer func() { recover() }()
			proceed()
		}()
		proceed() // second attempt after the recovered panic
	}
	flaky := func(proceed func()) {
		attempts++
		log = append(log, "flaky")
		if attempts == 1 {
			panic("boom")
		}
		proceed()
	}
	Run([]Middleware{guard, flaky}, func() { log = append(log, "call") })
	want := []string{"flaky", "flaky", "call"}
	if !slices.Equal(log, want) {
		t.Errorf("panic-retry order = %v, want %v", log, want)
	}
}

func TestRunEmpty(t *testing.T) {
	ran := false
	Run(nil, func() { ran = true })
	if !ran {
		t.Error("Run with no middleware must still call")
	}
}
