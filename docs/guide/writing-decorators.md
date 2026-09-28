# Writing decorators

A decorator is an ordinary Go function. deco supports three shapes, from most
convenient to most specialised.

## Middleware factories (recommended) {#middleware-factories}

Return the behaviour instead of wrapping a function:

```go
import "github.com/paulmanoni/deco/decorators"

func logged() decorators.Middleware {
	return func(proceed func()) {
		fmt.Println("-> start")
		proceed()
		fmt.Println("<- done")
	}
}

func timing(label string) decorators.Middleware {
	return func(proceed func()) {
		start := time.Now()
		proceed()
		fmt.Printf("%s took %s\n", label, time.Since(start))
	}
}
```

deco detects the shape from the signature (exactly the leading args, one
`decorators.Middleware` result) and **fuses** the stack: an all-factory stack
compiles to a reflection-free wrapper (~52 ns for three deep, at any depth),
and a mixed stack collapses each run of factories into one layer. See
[Performance](./performance).

`proceed` contract: call it **zero** times to short-circuit (the call returns
zero values), **once** to run, **more than once** to retry — a repeated
`proceed()` re-runs everything downstream, correctly even across a recovered
panic.

```go
func retry(n int) decorators.Middleware {
	return func(proceed func()) {
		for range n {
			ok := func() (ok bool) { defer func() { ok = recover() == nil }(); proceed(); return }()
			if ok {
				return
			}
		}
	}
}

func guard(allowed bool) decorators.Middleware {
	return func(proceed func()) {
		if allowed {
			proceed()
		}
	}
}
```

## Wrap-style with `decorators.Func`

The classic shape — a generic function that takes the wrapped function (plus
optional leading args) and returns one of the same type. Build it on
`decorators.Func` so you never touch reflection yourself:

```go
func logged[F any](fn F) F {
	return decorators.Func(fn, func(proceed func()) {
		fmt.Println("-> start")
		proceed()
		fmt.Println("<- done")
	})
}
```

`Func` works for **any** signature — multiple returns, no returns, variadics —
and each such decorator is one reflective layer (~285 ns).

## Request-aware: reading args and results

When the decorator needs to inspect or replace the arguments or return values
— auth middleware that reads the `*http.Request` — use `decorators.FuncValues`,
which exposes them as `[]any`:

```go
func RequireRole[F any](role string, fn F) F {
	return decorators.FuncValues(fn, func(args []any, proceed func([]any) []any) []any {
		var w http.ResponseWriter
		var r *http.Request
		for _, a := range args {
			switch v := a.(type) {
			case http.ResponseWriter:
				w = v
			case *http.Request:
				r = v
			}
		}
		if r == nil || r.Header.Get("X-Role") != role {
			if w != nil {
				w.WriteHeader(http.StatusForbidden)
			}
			return nil // short-circuit: the handler never runs
		}
		return proceed(args) // authorised → run the handler
	})
}
```

`proceed(args)` runs the wrapped function (pass modified args to rewrite
them); returning your own values replaces the results; not calling it
short-circuits. This is exactly the middleware in the
[router example](https://github.com/paulmanoni/deco/tree/main/examples/router).

## Concrete typed decorators (zero overhead)

deco calls whatever you name, so a decorator specialised to one signature has
no reflection at all — ~2 ns, same as a raw call:

```go
func logged(fn func(int, int) int) func(int, int) int {
	return func(a, b int) int { log.Print("call"); return fn(a, b) }
}
```

The trade-off is generality: one signature shape per decorator.
