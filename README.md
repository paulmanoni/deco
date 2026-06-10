# deco

Python-style decorators for Go, via code generation. Annotate any function with
a doc comment and `deco` wraps it — every caller of the original name
transparently flows through your decorators.

```go
//@decorate logged
//@decorate timing("slow")
func Add(a, b int) int { return a + b }
```

```sh
deco run .
# [log] -> calling func(int, int) int
# [time] slow took 21µs
# [log] <- returned from func(int, int) int
# Add(2, 3) = 5
```

## Install

```sh
go install github.com/paulmanoni/deco@latest
```

## Commands

```sh
deco run [dir]        # run the program with decorators applied (source untouched)
deco build [dir]      # build it
deco generate [dir]   # write the generated wrappers to disk instead
```

`dir` defaults to `.` and may also be a `.go` file. `run` and `build` use Go's
`-overlay`, so your source files are never modified; `generate` writes
`<file>_gen.go` files next to your code.

## Creating a custom decorator

A decorator is a generic function that takes the wrapped function and returns
one of the **same type**. Build it with `decorators.Func` — you write plain
middleware, no reflection:

```go
import "github.com/paulmanoni/deco/decorators"

func logged[F any](fn F) F {
	return decorators.Func(fn, func(proceed func()) {
		fmt.Println("-> start")
		proceed()          // runs the wrapped function
		fmt.Println("<- done")
	})
}
```

Call `proceed()` where you like:

```go
// timing — a decorator that takes an argument (passed BEFORE the function)
func timing[F any](label string, fn F) F {
	return decorators.Func(fn, func(proceed func()) {
		start := time.Now()
		proceed()
		fmt.Printf("%s took %s\n", label, time.Since(start))
	})
}

// retry — call proceed() more than once
func retry[F any](n int, fn F) F {
	return decorators.Func(fn, func(proceed func()) {
		for i := 0; i < n; i++ {
			ok := func() (ok bool) { defer func() { ok = recover() == nil }(); proceed(); return }()
			if ok { return }
		}
	})
}

// guard — don't call proceed() to short-circuit (returns the zero value)
func guard[F any](allowed bool, fn F) F {
	return decorators.Func(fn, func(proceed func()) {
		if allowed { proceed() }
	})
}
```

`decorators.Func` works for **any** signature — multiple returns, no returns,
variadics — and runs the reflection once.

## Using decorators

Annotate a function. Decorators **stack bottom-up**: the topmost annotation is
the outermost wrapper.

```go
//@decorate logged          // outermost
//@decorate timing("slow")  // innermost
func Add(a, b int) int { return a + b }
```

- **Bare name** (`//@decorate logged`) — resolves to a decorator in the same
  package.
- **Qualified name** (`//@decorate mw.Logged`) — a decorator from another
  package. deco finds the package automatically (it matches `mw` against your
  module's packages via `go list`), so this usually just works:

  ```go
  //@decorate mw.Logged
  //@decorate mw.RequireRole("admin")
  func Handler(w http.ResponseWriter, r *http.Request) { ... }
  ```

  Add a `//deco:import` directive only when auto-resolution can't decide — an
  ambiguous package name, or a decorator in an external module that nothing in
  your code imports:

  ```go
  //deco:import "github.com/you/mw"           // or: //deco:import alias "github.com/you/mw"
  ```

Then `deco run .` (or `build` / `generate`). That's it — callers of `Add` or
`Handler` now go through the decorators.

## Examples

```sh
deco run ./example          # three different signatures, each decorated
deco run ./examples/router  # multi-package HTTP router; the router itself is a decorator
```

`./examples/router` shows the Flask `@app.route` pattern — annotating a handler
with `//@decorate routing.Route("GET", "/users")` registers it.

## Notes

- Decorators are applied once, at package init (like Python's `fn = a(b(fn))`).
- Methods (functions with receivers) are not supported in v1.
- Decorators built with `decorators.Func` wrap the call but don't expose the
  arguments/return values; for those, write the `reflect`-based form directly.
