# What is deco?

deco brings Python-style decorators to Go, via code generation. You annotate a
plain function or method with a doc comment:

```go
//deco:wrap logged
//deco:wrap timing("slow")
func Add(a, b int) int { return a + b }
```

and every caller of `Add` transparently flows through the decorator chain —
exactly like Python's `fn = logged(timing("slow", fn))`.

## How it works

deco is a **transpiler**, not a runtime framework:

1. Your function is renamed to an unexported `addImpl` (a precise text edit —
   comments and formatting stay byte-for-byte).
2. A type-safe wrapper with the original name and an identical signature is
   generated, building the decorator chain once at package init.
3. The `deco` CLI hands off to the real `go` toolchain with a
   [build overlay](https://pkg.go.dev/cmd/go#hdr-Build_and_test_caching), so
   **your source files are never modified** — the decoration exists only for
   the duration of the build.

There is no reflection in the wiring: the generated wrapper calls whatever
decorator you name. Decorators themselves can be fully typed (zero overhead),
built on the reflective `decorators.Func` helper (works for any signature), or
written as [middleware factories](./writing-decorators#middleware-factories)
that fuse a whole stack into one reflection-free call.

## Syntax

`//deco:wrap name` is the primary form — Go's own `//tool:directive` comment
shape (like `//go:embed`), which gofmt never rewrites. `//@decorate name` is
accepted as an alias, and the keyword is configurable (`-annotation @wrap`).

## Design constraints

- Decorators apply **once, at package init** — construction-time side effects
  (route registration, metric setup) run at startup.
- Only generic **receivers** are excluded (`func (b *Box[T]) …`): a type
  parameter prevents the package-level method expression the chain is built
  over. Everything else — variadics, multiple returns, unnamed params, pointer
  and value receivers — works.
- deco never reimplements go behaviour. `deco test -race ./...` **is**
  `go test -race ./...` plus the overlay.
