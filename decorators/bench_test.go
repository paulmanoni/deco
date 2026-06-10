package decorators

import "testing"

// add is the trivial function under test; the decorators wrap it so the
// benchmarks measure pure wrapper overhead, not the work.
func add(a, b int) int { return a + b }

var sink int // keep results live so the compiler can't elide the calls

func BenchmarkRaw(b *testing.B) {
	f := add
	for b.Loop() {
		sink = f(1, 2)
	}
}

// BenchmarkFunc measures one decorators.Func layer (a reflect.MakeFunc wrapper
// with a pass-through proceed()).
func BenchmarkFunc(b *testing.B) {
	f := Func(add, func(proceed func()) { proceed() })
	for b.Loop() {
		sink = f(1, 2)
	}
}

// BenchmarkFuncChain3 measures three stacked Func layers — the cost grows
// linearly with chain depth.
func BenchmarkFuncChain3(b *testing.B) {
	noop := func(fn func(int, int) int) func(int, int) int {
		return Func(fn, func(p func()) { p() })
	}
	f := noop(noop(noop(add)))
	for b.Loop() {
		sink = f(1, 2)
	}
}

// BenchmarkFuncValues measures the args/results-exposing variant, which also
// boxes each argument and result into an interface.
func BenchmarkFuncValues(b *testing.B) {
	f := FuncValues(add, func(args []any, proceed func([]any) []any) []any {
		return proceed(args)
	})
	for b.Loop() {
		sink = f(1, 2)
	}
}

// loggedConcrete is a reflection-free decorator specialised to ONE signature.
// deco happily uses decorators like this — they're the zero-overhead escape
// hatch for hot paths, at the cost of generality (one per signature shape).
func loggedConcrete(fn func(int, int) int) func(int, int) int {
	return func(a, b int) int {
		return fn(a, b) // a real one would log/time around this call
	}
}

// BenchmarkConcrete shows a hand-written typed decorator costs about the same
// as a raw call — no reflection, no allocations.
func BenchmarkConcrete(b *testing.B) {
	f := loggedConcrete(add)
	for b.Loop() {
		sink = f(1, 2)
	}
}

// --- Recursion: re-entering the decorator chain on each self-call ---
//
// This mirrors exactly what the transpiler generates: the impl calls the
// wrapper by the public name, so every recursive step goes back through the
// decorator. The pair below quantifies that overhead versus plain recursion.

func factPlain(n int) int {
	if n <= 1 {
		return 1
	}
	return n * factPlain(n-1)
}

var factDecorated func(int) int

func factImplRec(n int) int {
	if n <= 1 {
		return 1
	}
	return n * factDecorated(n-1) // self-call hits the wrapper, like the generated code
}

func init() { factDecorated = Func(factImplRec, func(p func()) { p() }) }

func BenchmarkRecursivePlain(b *testing.B) {
	for b.Loop() {
		sink = factPlain(20)
	}
}

func BenchmarkRecursiveDecorated(b *testing.B) {
	for b.Loop() {
		sink = factDecorated(20)
	}
}
