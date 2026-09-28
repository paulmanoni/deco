# Changelog

All notable changes to **deco** are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/), and this project adheres to
[Semantic Versioning](https://semver.org/).

## [0.18.0] - 2026-09-28

### Added

- `transpiler.Scan` also surfaces directives on a file's PACKAGE doc comment
  (`Hit.PackageLevel`, `Func` empty) — package-scoped metadata such as a
  module name or route prefix for downstream code generators. Purely
  additive: function hits are unchanged, and the keyword filter applies to
  package-level hits equally.

## [0.17.1] - 2026-09-28

### Added

- `deco version` now prints deco's OWN version (from build info) above go's —
  the direct diagnostic for a stale installed binary, which silently ignores
  annotation syntax it predates and runs the program undecorated.
- A `.go` file argument to a compile/run subcommand is refused with a clear
  run-the-package hint when its package is decorated: go compiles only the
  listed files, so the generated wrappers can never be present and the run
  failed with baffling `undefined` errors. Files in undecorated packages
  forward as before (go-parity).

## [0.17.0] - 2026-09-28

### Added

- Documentation site at
  [paulmanoni.github.io/deco](https://paulmanoni.github.io/deco/) — guide
  (getting started, using and writing decorators, methods, performance) and
  reference (CLI, library API) — deployed to GitHub Pages on push to main.

### Changed

- The README is trimmed to the essentials (install, one example, highlights)
  and points at the documentation site.

## [0.16.0] - 2026-09-28

### Added

- **First-class decorators from other libraries.** A qualified decorator
  (`pkg.Name` — another package in your module OR an external dependency) now
  gets the same treatment as a same-package one: deco resolves the defining
  package's source (module index, or `go list` into the module cache) and
  reads its signatures, so
  - a qualified **middleware factory fuses** — `//deco:wrap mw.Traced` from a
    shared middleware library compiles to the reflection-free wrapper;
  - a **wrong-arity** qualified reference is a clear transpile-time
    `file:line` error instead of a cryptic compile error inside `*_gen.go`.
  The check is best-effort and permissive where it must be: a package deco
  cannot read, a package-level `var` decorator, or declarations that disagree
  across build tags fall back to the previous unchecked wrap-style behaviour,
  so nothing that worked before stops working.
- **Seamless external-library imports.** Selector auto-resolution now also
  covers the go.mod's DIRECT dependencies (enumerated lazily via
  `go mod edit -json` + `go list`, cached): a decorator library needs only a
  `go get` — no import statement in any source file and no `//deco:import`
  directive. The directive remains for ambiguous names and indirect
  dependencies.

## [0.15.0] - 2026-09-28

### Added

- **Fused middleware chains.** A decorator can now be a middleware FACTORY —
  it returns `decorators.Middleware` instead of wrapping the function
  (`func logged() decorators.Middleware`). Annotations are unchanged; deco
  classifies the shape from the signature (exactly the leading args, one
  `decorators.Middleware` result, resolved to deco's package — a same-named
  local type never fuses). Then:
  - an **all-factory stack** compiles to a reflection-free wrapper: a
    `[]decorators.Middleware` built once at init, run over a typed call via
    `decorators.Run` — ~52 ns / 4 allocs for a 3-deep stack, versus ~900 ns /
    21 allocs for three nested `Func` layers (~17×), and depth is ~free;
  - a **mixed stack** fuses each run of consecutive factories into one
    reflective layer via `decorators.Chain`, with wrap-style decorators
    nesting around it as before.
- `decorators.Middleware`, `decorators.Chain` (whole stack in one wrapper
  layer) and `decorators.Run` (reflection-free driver, exported for
  hand-written typed wrappers). `proceed` semantics match `Func`: skip to
  short-circuit (zero values), call again to retry — a repeated `proceed`
  re-runs everything downstream, correctly even across a recovered panic.
- Benchmarks (`BenchmarkChain3`, `BenchmarkRunTyped3`) and fusion tests,
  including a compile check of fused output against the real decorators
  package; the `./example` `audited` decorator is now a factory, so
  `deco run ./example` exercises the fused path for a function and a method.

## [0.14.0] - 2026-09-28

### Added

- **Method decorators.** `//deco:wrap` now works on methods — pointer or value
  receiver, exported or not. The method is renamed in place (receiver kept) and
  the decorator chain is built over the method expression
  (`logged((*Counter).addImpl)`), a plain function value whose first parameter
  is the receiver — so every existing function decorator, including
  `decorators.Func`/`FuncValues`, wraps methods unchanged (`FuncValues` sees
  the receiver at `args[0]`). The generated wrapper is a real method with the
  original signature, preserving interface satisfaction. Chain vars are
  qualified by receiver type, so same-named methods on different types coexist.
- `./example` gained a decorated-methods `Counter` (pointer and value
  receivers, bare and qualified decorators).

### Limitations

- Generic receivers (`func (b *Box[T]) …`) cannot be decorated — a type
  parameter prevents the package-level method expression — and are rejected
  with a clear `file:line` error.

## [0.13.0] - 2026-09-28

### Added

- `//deco:wrap name` — the decorator annotation in Go's own `//tool:directive`
  comment form (like `//go:embed`), now the primary, always-recognised syntax.
  `//@decorate` (and any `WithAnnotation`/`-annotation` keyword) remains a fully
  supported alias; both forms stack together in one doc comment. Keyword
  matching is now word-boundary precise (`//deco:wrapper` markers and
  `//@decorated` can never be misread as annotations).
- gofmt-style flags on `deco generate`: `-l` lists files whose generated
  content differs from disk, `-d` prints unified diffs; either suppresses
  writing unless `-w` is also given (no flags = write, as before).
  `test -z "$(deco generate -l .)"` is the CI drift gate.

### Changed

- **Zero dependencies**: the CLI is now built on the stdlib `flag` package;
  cobra (and its transitive deps) are gone from go.mod. Flags are single-dash
  in the go style (`-annotation`; `--annotation` still accepted). The cobra
  `completion` subcommand is gone; `deco help` / `deco` print the usage text.
- Examples and docs now use the `//deco:wrap` directive form.
- go directive bumped to 1.27.1.

## [0.12.0] - 2026-06-22

### Added

- `transpiler.Scan` — a read-only structured annotation front-end: returns
  every `//@<keyword>` directive on a top-level function's doc comment as
  data (`Hit`), so downstream tools can generate their own code from the
  annotations. deco itself stays oblivious to what the keywords mean.

## [0.11.0] - 2026-06-11

### Added

- Source-map remapping now covers **stdout** for `deco test`, not just stderr:
  test failure messages and panic stack frames in decorated code report the
  real source position instead of an overlay shadow path or shifted line. The
  matcher handles positions anywhere in a line (indented failures, tab-indented
  stack frames); `-json` output is passed through verbatim.
- `examples/remap-demo`: a runnable nested-module demo (decorated `Div` + a
  failing test) showing `deco test .` report `calc.go:12` (source) rather than
  an overlay temp path.

## [0.10.0] - 2026-06-11

### Added

- Source-map remapping of toolchain diagnostics. `deco vet`/`build`/`test` now
  rewrite positions reported against the transpiled overlay back to the user's
  source — a finding inside a decorated function reports the same `file:line` as
  bare `go vet`, not an overlay temp path or a shifted line. Diagnostics in
  generated wrappers (`*_gen.go`, which have no source equivalent) show the
  logical path and a note.
- `transpiler.OverlayWithSourceMap` returns a `SourceMap` (line shifts +
  shadow→logical paths) alongside the overlay; `Overlay` is unchanged.
  `transpiler.NewSourceMap` and `SourceMap.Remap` are exported for tooling.

### Known limitations

- Only the child's stderr is remapped; positions that test failures print to
  stdout are not.

## [0.9.0] - 2026-06-11

### Added

- Go toolchain pass-through: `deco <subcommand>` runs `go <subcommand>` with
  deco's transpile overlay applied first. Subcommand-agnostic — `build`, `run`,
  `test`, `vet`, `install`, `list` and any future go subcommand are forwarded
  (the compile/run ones get `-overlay`; others are forwarded untouched). User
  args, environment, working directory and stdio are forwarded faithfully, and
  deco mirrors the child's exact exit code.
- A stderr filter that warns when a diagnostic references transpiled output
  (`*_gen.go`) — the seam for a future source-map position-remapping pass.
- Tests for arg forwarding, exit-code fidelity, overlay injection, cleanup on
  failure, and arbitrary-subcommand forwarding.

### Changed

- deco's own flags (`--annotation`) now go before the subcommand for
  pass-through commands, e.g. `deco --annotation "@wrap" test ./...`.

### Known limitations

- `vet`/`test`/`build` diagnostics report positions in the transpiled output,
  not your source. Position remapping is deferred.

## [0.8.0] - 2026-06-10

### Added

- Configurable annotation keyword. Library: `transpiler.WithAnnotation("@wrap")`
  passed to `Generate`/`Transform`/`Overlay`. CLI: a `--annotation` flag on every
  command (default `@decorate`). The internal `//deco:wrapper` / `//deco:import`
  directives are unchanged.
- `TestCustomAnnotation`.

## [0.7.1] - 2026-06-10

### Added

- `BenchmarkConcrete` demonstrating that a reflection-free, signature-specific
  decorator runs at ~2 ns/op with zero allocations (versus ~310 ns / 7 allocs
  for a reflective `Func` layer).

### Docs

- Rewrote the **Performance** section: deco calls whatever decorator you name,
  so hot paths can use a typed, reflection-free decorator; reserve
  `Func`/`FuncValues` for generality and cold paths.

## [0.7.0] - 2026-06-10

### Changed

- Moved the transpiler from `internal/transpiler` to a public `transpiler`
  package so other modules can import it.

### Added

- Public library API: `Transform(dir)` returns the generated content in memory
  (`[]Output`) without writing; documented `//go:generate` usage.
- `TestRecursiveCallReentersWrapper`: only the declaration is renamed, so a
  recursive self-call keeps the public name and re-enters the wrapper (Python
  semantics), never the impl.
- Benchmarks for `Func`, `FuncValues`, stacked chains, and recursive re-entry.

## [0.6.0] - 2026-06-10

### Added

- `decorators.FuncValues`: request-aware decorators that can read or modify the
  call's arguments and return values (e.g. auth middleware that inspects the
  `*http.Request`).
- Router example: `RequireRole` is now request-aware — it checks the `X-Role`
  header and denies with a 403, short-circuiting the handler.

## [0.5.1] - 2026-06-10

### Added

- Transpiler test suite: rename precision (only the declaration, never a
  same-spelled identifier / string / comment / call site), idempotency, and
  signature forwarding (void, multi-return, variadic).

## [0.5.0] - 2026-06-10

### Added

- Auto-resolution of qualified decorator packages via `go list`: `//deco:import`
  is now optional, needed only to disambiguate a shared package name or to point
  at an external package that nothing in the module imports.

## [0.4.0] - 2026-06-10

### Changed

- Generated wrappers build their decorator chain **once at package init** (like
  Python's `fn = a(b(fn))`) instead of per call — more efficient, and it lets
  decorators run construction-time side effects at startup.

### Added

- Router-as-decorator: `routing.Route("GET", "/users")` registers a handler at
  init — the Flask `@app.route` pattern.

## [0.3.0] - 2026-06-10

### Added

- Qualified decorator names (`pkg.Name`) resolved via the `//deco:import`
  directive (`"path"` or `alias "path"`).
- Generated wrappers re-import the packages used by a reproduced signature
  (e.g. `net/http` for `http.ResponseWriter`).
- Recursive multi-package processing — deco transpiles the whole tree, like
  `go build ./...`.
- `./examples/router`: a multi-package HTTP router example.

## [0.2.0] - 2026-06-10

### Added

- `decorators.Func`: build a decorator from plain middleware (`proceed()` thunk)
  with no hand-written reflection.

### Changed

- Reimplemented `Logged` and `Timing` on top of `Func`.

## [0.1.0] - 2026-06-10

### Added

- Initial release — a comment-hosted decorator transpiler.
- `//@decorate` doc-comment annotations; the original function is renamed to an
  unexported `<name>Impl` and a type-matched wrapper with the original name is
  generated, so every caller transparently hits the decorator chain.
- Works for any signature: multiple/zero results, variadics, unnamed/grouped
  params. Decorators stack bottom-up (topmost = outermost).
- CLI (cobra): `generate` writes `<file>_gen.go` to disk; `build` and `run` use
  Go's `-overlay` so the source tree is never modified.
- Reflection-based example decorators `Logged` and `Timing`.
- Clear `file:line` errors for unknown / wrong-arity decorators and methods.
- Three-signature example; installable with `go install`.

[0.18.0]: https://github.com/paulmanoni/deco/releases/tag/v0.18.0
[0.17.1]: https://github.com/paulmanoni/deco/releases/tag/v0.17.1
[0.17.0]: https://github.com/paulmanoni/deco/releases/tag/v0.17.0
[0.16.0]: https://github.com/paulmanoni/deco/releases/tag/v0.16.0
[0.15.0]: https://github.com/paulmanoni/deco/releases/tag/v0.15.0
[0.14.0]: https://github.com/paulmanoni/deco/releases/tag/v0.14.0
[0.13.0]: https://github.com/paulmanoni/deco/releases/tag/v0.13.0
[0.12.0]: https://github.com/paulmanoni/deco/releases/tag/v0.12.0
[0.11.0]: https://github.com/paulmanoni/deco/releases/tag/v0.11.0
[0.10.0]: https://github.com/paulmanoni/deco/releases/tag/v0.10.0
[0.9.0]: https://github.com/paulmanoni/deco/releases/tag/v0.9.0
[0.8.0]: https://github.com/paulmanoni/deco/releases/tag/v0.8.0
[0.7.1]: https://github.com/paulmanoni/deco/releases/tag/v0.7.1
[0.7.0]: https://github.com/paulmanoni/deco/releases/tag/v0.7.0
[0.6.0]: https://github.com/paulmanoni/deco/releases/tag/v0.6.0
[0.5.1]: https://github.com/paulmanoni/deco/releases/tag/v0.5.1
[0.5.0]: https://github.com/paulmanoni/deco/releases/tag/v0.5.0
[0.4.0]: https://github.com/paulmanoni/deco/releases/tag/v0.4.0
[0.3.0]: https://github.com/paulmanoni/deco/releases/tag/v0.3.0
[0.2.0]: https://github.com/paulmanoni/deco/releases/tag/v0.2.0
[0.1.0]: https://github.com/paulmanoni/deco/releases/tag/v0.1.0
