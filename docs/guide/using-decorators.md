# Using decorators

Annotate a function or method. Decorators **stack bottom-up**: the topmost
annotation is the outermost wrapper.

```go
//deco:wrap logged          // outermost
//deco:wrap timing("slow")  // innermost
func Add(a, b int) int { return a + b }
```

Then `deco run .` (or `build` / `test` / `generate`). Every caller of `Add`
now goes through the chain.

## Same package

A **bare name** (`//deco:wrap logged`) resolves to a function in the same
package. deco arity-checks it at transpile time: a decorator takes its leading
annotation arguments plus the wrapped function — or exactly the arguments, if
it is a [middleware factory](./writing-decorators#middleware-factories).

## Other packages and libraries

A **qualified name** (`//deco:wrap mw.Logged`) uses a decorator from another
package — one of your module's packages, or an **external library**, where
`go get` alone is enough:

```go
//deco:wrap mw.Logged
//deco:wrap mw.RequireRole("admin")
func Handler(w http.ResponseWriter, r *http.Request) { ... }
```

No import statement and no directive needed: deco matches `mw` against your
module's packages, then against your go.mod's direct dependencies, and injects
the import into the generated file only. It also reads the package's source,
so wrong-arity references fail at transpile time with a `file:line` error, and
middleware factories from libraries fuse just like local ones.

Add a `//deco:import` directive only when auto-resolution can't decide —
several packages share the name, or the decorator lives in an indirect
dependency:

```go
//deco:import "github.com/you/mw"           // or: //deco:import alias "github.com/you/mw"
```

## Alias syntax

`//@decorate name` works everywhere `//deco:wrap name` does, and the alias
keyword is configurable per run: `deco -annotation @wrap test ./...`. Both
forms may appear in one doc comment and stack together.

## Semantics to know

- Chains are built **once at package init**, so a decorator with
  construction-time side effects (Flask-style route registration) runs at
  startup. See the [router example](https://github.com/paulmanoni/deco/tree/main/examples/router).
- Only the declaration is renamed: a recursive function's self-call keeps the
  public name and re-enters the chain, matching Python.
- Generation is idempotent — re-running `deco generate` changes nothing.
