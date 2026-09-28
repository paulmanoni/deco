# CLI

`deco` is a thin wrapper around the go toolchain: run `deco <cmd>` anywhere
you'd run `go <cmd>`. It transpiles your decorators into a build overlay and
hands off to the real `go` — flags, exit codes and output are exactly go's,
and your source files are never modified.

```sh
deco build ./...            # = go build with decorators applied
deco run .                  # = go run .   (reads stdin, streams output)
deco test -race ./...       # = go test -race ./...
deco vet ./...              # = go vet ./...
deco install ./cmd/foo      # = go install ./cmd/foo
```

- The compile/run subcommands (`build run test vet install list`) get deco's
  `-overlay`.
- Any other subcommand (`env`, `mod`, …) is forwarded verbatim — including
  future go subcommands. `deco version` first prints deco's own version, then
  go's — check it when decorators mysteriously stop applying: a stale
  installed binary ignores syntax it predates.
- A `.go` FILE argument to a compile/run subcommand is refused when its
  package is decorated: go compiles only the listed files, so the generated
  wrappers can never be present. Run the package directory instead
  (`deco run ./example`, not `deco run ./example/main.go`).
- The child inherits your environment and working directory, and deco exits
  with the child's exact exit code, so a failing `deco test` fails CI.

## Position remapping

`vet`/`test`/`build` run against the transpiled overlay, but deco remaps
diagnostic positions back to your source — compiler and vet diagnostics,
`go test` failure messages, and panic stacks. A finding in a decorated
function reports the same `file:line` as bare `go`. Positions inside a
generated wrapper (`*_gen.go`) have no source equivalent; deco shows the
logical path and prints a note. Machine output (`-json`) passes through
verbatim.

## `deco generate`

The one non-wrapper command: it materialises the wrappers on disk
(`<file>_gen.go` next to your sources) so bare `go build`, gopls and linters
see the registrations. It speaks gofmt's flag vocabulary:

```sh
deco generate ./...        # write (the default)
deco generate -l ./...     # list files whose generated content differs
deco generate -d ./...     # print unified diffs, write nothing
deco generate -l -w ./...  # list AND write (flags combine)
```

CI drift gate, exactly like `gofmt -l`:

```sh
test -z "$(deco generate -l .)"
```

## Flags

deco's own flags go **before** the subcommand, single-dash in the go style:

```sh
deco -annotation "@wrap" test ./...   # accept //@wrap as the alias keyword
```

| flag | meaning |
|------|---------|
| `-annotation <keyword>` | alias keyword that marks a decorator (default `@decorate`); the `//deco:wrap` directive is always recognised |
| `generate -l` | list files whose generated output differs from disk |
| `generate -d` | print unified diffs instead of writing |
| `generate -w` | write, when combined with `-l`/`-d` |
