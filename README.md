<p align="center">
  <img src="assets/banner.svg" alt="deco — Python-style decorators for Go" width="820">
</p>

<p align="center">
  <a href="https://paulmanoni.github.io/deco/"><img src="https://img.shields.io/badge/docs-paulmanoni.github.io%2Fdeco-00add8" alt="Documentation"></a>
  <a href="https://pkg.go.dev/github.com/paulmanoni/deco"><img src="https://pkg.go.dev/badge/github.com/paulmanoni/deco.svg" alt="Go Reference"></a>
  <a href="LICENSE"><img src="https://img.shields.io/badge/license-MIT-blue.svg" alt="MIT License"></a>
</p>

# deco

Python-style decorators for Go, via code generation. Annotate any function or
method with a doc comment and every caller transparently flows through your
decorators — plain Go, generated code, **zero dependencies**.

```go
//deco:wrap logged
//deco:wrap timing("slow")
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

## Highlights

- **A go wrapper, not a new toolchain.** `deco build|run|test|vet|install` runs
  the real `go` command with a source overlay — flags, exit codes and output
  are exactly go's, and your files are never modified. Diagnostics are
  remapped back to your source, so a finding reports the same `file:line` as
  bare `go`.
- **Any function or method.** Multiple returns, variadics, pointer or value
  receivers — the generated wrapper is fully typed and preserves interface
  satisfaction.
- **Write decorators without reflection.** `decorators.Func` turns plain
  middleware into a decorator for any signature; `decorators.FuncValues` gives
  request-aware middleware access to arguments and results.
- **Fused middleware chains.** Write decorators as factories returning
  `decorators.Middleware` and a whole stack runs with no reflection at all:
  ~52 ns for a 3-deep chain instead of ~900 ns, at any depth.
- **Seamless with libraries.** `//deco:wrap mw.Logged` resolves from your
  module's packages or any `go get`-ed dependency — no import statement, no
  directive — and is arity-checked at transpile time.
- **gofmt-style codegen.** `deco generate` writes committed `<file>_gen.go`
  wrappers; `-l` and `-d` make the CI drift gate one line:
  `test -z "$(deco generate -l .)"`.

## Documentation

**[paulmanoni.github.io/deco](https://paulmanoni.github.io/deco/)** — guide,
CLI and library reference. API docs on
[pkg.go.dev](https://pkg.go.dev/github.com/paulmanoni/deco).

Runnable examples:

```sh
deco run ./example          # different signatures + decorated methods
deco run ./examples/router  # Flask-style routing + request-aware middleware
```

## Changelog

See [CHANGELOG.md](CHANGELOG.md) for the release history.

## License

[MIT](LICENSE) © Paul Manoni
