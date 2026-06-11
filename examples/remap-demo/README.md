# remap-demo — see source-map remapping

A tiny **separate module** (its own `go.mod`, so the parent's `go test ./...`
ignores it) with two decorated functions, so you can watch `deco` report
positions at the right line in *this* source file even though it runs against
the transpiled overlay (where a `//deco:wrapper` marker shifts each body down a
line).

Because it's a separate module, **run the commands from inside this directory**
(`deco vet ./examples/remap-demo` from the repo root fails — that's plain `go`
behavior for a nested module):

```sh
cd examples/remap-demo
```

### vet finding → remapped on stderr

`Warn` has a `Printf` bug:

```go
//@decorate logged
func Warn(code int) {
	fmt.Printf("%s\n", code) // %s with an int — flagged on this line
}
```

```sh
deco vet .    # reports calc.go:21 — the real line, not deco-overlay-*/0_calc.go:22
```

### test failure / panic → remapped on stdout

`Div` panics on divide-by-zero, and `calc_test.go` calls `Div(1, 0)`. The vet
bug above would otherwise stop `go test` before the test runs, so skip vet:

```sh
deco test -vet=off .    # the panic stack points at calc.go:16, not the overlay path
```

### compare with bare go

```sh
go vet .                # .../calc.go:21
deco vet .              # calc.go:21        (same line, no temp path)

go test -vet=off .      # .../calc.go:16 in the stack
deco test -vet=off .    # calc.go:16        (same line, no temp path)
```

deco compiled and ran the *transpiled* code, yet every reported position maps
back to your source. (Positions in fully generated wrappers, `*_gen.go`, have no
source equivalent — deco shows the logical `*_gen.go` path and prints a note.)
Exit codes mirror `go` exactly.
