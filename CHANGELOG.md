# Changelog

All notable changes to **deco** are documented here. The format is based on
[Keep a Changelog](https://keepachangelog.com/), and this project adheres to
[Semantic Versioning](https://semver.org/).

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
