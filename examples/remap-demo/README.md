# remap-demo — see source-map remapping

A tiny **separate module** (its own `go.mod`, so the parent's `go test ./...`
ignores it) with one decorated function whose test fails — so you can watch
`deco` report the failure at the right line in *this* source file, even though
it runs against the transpiled overlay.

`Div` is decorated, and a `//deco:wrapper` marker shifts its body down a line in
the overlay:

```go
//@decorate logged
func Div(a, b int) int {
	return a / b // line 12 — panics when b == 0
}
```

`calc_test.go` calls `Div(1, 0)`, which panics. Run it two ways:

```sh
go test .     # pristine:        .../examples/remap-demo/calc.go:12
deco test .   # overlay + remap: calc.go:12          ← same line, no temp path
```

`deco` compiled and ran the *transpiled* code (where `Div` is renamed and the
marker shifts the body to line 13), yet the panic stack still points at
**`calc.go:12`** — your real source line — not a `deco-overlay-*/0_calc.go:13`
shadow path. That rewrite is deco's source map at work (it remaps the child's
stderr *and* the test-failure/stack positions on stdout).

Both runs exit non-zero (the test is meant to fail); `deco` mirrors `go`'s exit
code exactly.

> Try `deco vet .` too — vet diagnostics in decorated functions are remapped the
> same way.
