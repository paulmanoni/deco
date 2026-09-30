# Using deco in your own library

deco is built to be embedded: the CLI is a thin shell over two importable
packages, so another library, framework or code generator can offer
decorator/annotation ergonomics without shipping the `deco` binary — and a
middleware library can publish decorators that any deco user consumes with a
one-line annotation.

## Shipping a decorator library

A decorator library is an ordinary Go package — no deco dependency is
required for wrap-style decorators, and only the tiny `decorators` package for
fusable ones. Publish middleware factories so consumers get the
reflection-free fused path:

```go
// Package mw — import "github.com/you/mw"
package mw

import "github.com/paulmanoni/deco/decorators"

func Traced() decorators.Middleware {
	return func(proceed func()) { /* span start */ proceed() /* span end */ }
}

func RateLimited(perMinute int) decorators.Middleware { ... }
```

Consumers need nothing but a `go get`:

```go
//deco:wrap mw.Traced
//deco:wrap mw.RateLimited(60)
func Handle(w http.ResponseWriter, r *http.Request) { ... }
```

deco resolves `mw` against the consumer's module and its go.mod's direct
dependencies (no import statement, no directive), reads your package's source
to arity-check the reference, and — because the factories return
`decorators.Middleware` — fuses the stack into one reflection-free wrapper.
What makes a function fusable, precisely:

- exactly the leading annotation arguments as parameters (no `fn`);
- a single result of type `decorators.Middleware` (any import alias works).

Anything else is treated as a classic wrap-style decorator
(`func Timing[F any](label string, fn F) F`), which also works cross-package —
it just keeps its own wrapper layer.

## Running the transpiler from Go

The whole engine is `github.com/paulmanoni/deco/transpiler`, so your tool can
apply decorators without the CLI:

```go
import "github.com/paulmanoni/deco/transpiler"

// Write <file>_gen.go wrappers across a package tree:
err := transpiler.Generate("./mypkg")

// The same generation in memory — inspect or post-process, no writes:
outs, err := transpiler.Transform("./mypkg")

// A `go build -overlay` file — decorate without touching the source tree:
overlay, cleanup, err := transpiler.Overlay("./mypkg")
defer cleanup()
cmd := exec.Command("go", "build", "-overlay", overlay, "./...")
```

`OverlayWithSourceMap` additionally returns a `SourceMap` whose `Remap`
translates positions the toolchain reports against the overlay back to the
user's source — use it to keep your tool's diagnostics pointing at real
`file:line`s, the way `deco vet`/`deco test` do.

Pick your own annotation vocabulary with `WithAnnotation`:

```go
transpiler.Generate("./mypkg", transpiler.WithAnnotation("@wrap"))
```

The `//deco:wrap` directive stays recognised alongside your keyword, and the
generated files carry the standard `// Code generated … DO NOT EDIT.` header.

## Building your own annotations with `Scan`

When your framework wants annotations that are *not* decorators — routes,
workers, DI providers — use `transpiler.Scan` as the front-end and generate
whatever you like from the hits. deco never modifies anything in this mode:

```go
hits, err := transpiler.Scan("./...", "rest", "query", "worker")
for _, h := range hits {
	// h.Pkg, h.File, h.Func, h.Keyword, h.Args, h.Pos
	fmt.Printf("%s:%d: //@%s %v on %s\n",
		h.Pos.Filename, h.Pos.Line, h.Keyword, h.Args, h.Func)
}
```

Each `Hit` is one `//@<keyword>` directive on a top-level function's doc
comment **or on a package clause's doc comment**, in deterministic order, with
the arguments pre-split and the exact source position for your own error
messages. Filter keywords by passing them (with or without the `@`), or pass
none to receive every directive. Generated (`*_gen.go`), test, vendored and
hidden files are skipped, and a function deco itself has renamed reports its
public name.

Scanning on every save? `transpiler.NewScanCache()` gives you the same `Scan`
with per-file incremental reuse — a rescan re-parses only what changed
([reference](../reference/library#scan)).

**Package-level hits** carry `PackageLevel: true` and an empty `Func` — the
front-end for package-scoped metadata like a registration group's name or a
route prefix:

```go
// Package billing handles invoicing.
//
//@module billing
//@path /billing
package billing
```

```go
for _, h := range hits {
	if h.PackageLevel {
		// h.Keyword == "module", h.Args == ["billing"], h.Func == ""
	}
}
```

The keyword filter applies to package-level hits equally, and a consumer that
keys on `h.Func != ""` never sees them, so adding package directives to an
existing pipeline is opt-in.

This is exactly how the [nexus](https://github.com/paulmanoni/nexus) framework
implements `//@rest GET /users` → route registration: `Scan` surfaces the
directives, nexus emits its own registration files, and both tools coexist on
the same doc comments — deco ignores keywords it doesn't own, so your
vocabulary and deco's `//deco:wrap` can sit on one function.

## Coexistence rules

- deco only acts on `//deco:wrap` and the configured alias keyword; every
  other `//@word` is left for tools like yours.
- `nexus generate`-style tools should return the favour: ignore directives
  they don't recognise (Scan's keyword filter does this for you).
- The internal `//deco:wrapper` and `//deco:import` directives are deco's own;
  treat them as reserved.
