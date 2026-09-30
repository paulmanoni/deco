# Library API

deco ships two importable packages. Full reference on
[pkg.go.dev](https://pkg.go.dev/github.com/paulmanoni/deco).

## `…/decorators` — helpers for writing decorators

| function | what it does |
|----------|--------------|
| `Middleware` (`func(proceed func())`) | the fused decorator shape — return it from a factory to enable [fusion](../guide/performance) |
| `Chain[F any](fn F, mws ...Middleware) F` | run a whole middleware stack inside ONE wrapper layer |
| `Run(mws []Middleware, call func())` | drive a middleware stack over a typed call — no reflection (what fused wrappers use) |
| `Func[F any](fn F, mw func(proceed func())) F` | wrap one call as middleware — call `proceed()` to run it |
| `FuncValues[F any](fn F, mw func(args []any, proceed func([]any) []any) []any) F` | request-aware: read or modify the arguments and results |
| `Logged[F any](fn F) F` | example decorator — logs entry and exit |
| `Timing[F any](label string, fn F) F` | example decorator — measures duration |

All are generic and signature-preserving; `Func`/`FuncValues` do the
reflection for you so your decorators contain none.

## `…/transpiler` — run the transpiler from Go

The same engine the CLI uses, exported for your own tooling:

| function | what it does |
|----------|--------------|
| `Generate(dir string, opts ...Option) error` | rename originals and write `<file>_gen.go` across the package tree |
| `Transform(dir string, opts ...Option) ([]Output, error)` | the same generation, returned in memory — no writes |
| `Overlay(dir string, opts ...Option) (path string, cleanup func(), err error)` | write a `go build -overlay` JSON; source left untouched |
| `OverlayWithSourceMap(dir, opts...)` | `Overlay` plus a `SourceMap` for remapping diagnostics |
| `Scan(dir string, keywords ...string) ([]Hit, error)` | read-only: return every `//@<keyword>` directive — on functions and on package doc comments (`Hit.PackageLevel`) — as data, for downstream code generators |
| `NewScanCache() *ScanCache` | an incremental front-end for `Scan`: `cache.Scan(dir, keywords...)` re-parses only files whose (mtime, size) changed since the last call |
| `WithAnnotation(keyword string) Option` | custom alias keyword (`//deco:wrap` always works) |

```go
import "github.com/paulmanoni/deco/transpiler"

if err := transpiler.Generate("./mypkg"); err != nil { ... }
```

Or wire it into `go generate` without installing the binary:

```go
//go:generate go run github.com/paulmanoni/deco generate .
```

`Scan` is the front-end for tools that generate their own code from
annotations (a web framework turning `//@rest GET /users` into a route
registration, or a package-doc `//@module billing` into the registration
group): it surfaces each directive as a `Hit{Pkg, File, Func, Keyword, Args,
Pos, PackageLevel}` without modifying anything. See
[Using deco in your own library](../guide/embedding#building-your-own-annotations-with-scan).

A dev loop that scans on every save shouldn't re-parse the whole tree each
time. `ScanCache` makes repeated scans incremental — per-file results are
cached keyed on the file's (mtime, size), so a rescan pays roughly one file's
parse instead of the tree's:

```go
var cache = transpiler.NewScanCache()   // one per process

func onSave() {
    hits, err := cache.Scan(root, "rest", "query", "provide")
    ...
}
```

One cache serves any keyword set (it stores unfiltered hits and filters per
call), it is safe for concurrent use, and deleted files drop out on the next
walk. Hits returned from a cached scan are shared — treat them as read-only.
The zero value is not usable; call `NewScanCache`.
