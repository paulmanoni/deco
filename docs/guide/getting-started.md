# Getting started

## Install

```sh
go install github.com/paulmanoni/deco@latest
```

Or pin it per-module as a Go 1.24+ tool:

```sh
go get -tool github.com/paulmanoni/deco@latest
go tool deco run .
```

## First decorator

Create a decorator and annotate a function:

```go
package main

import (
	"fmt"

	"github.com/paulmanoni/deco/decorators"
)

func logged() decorators.Middleware {
	return func(proceed func()) {
		fmt.Println("-> start")
		proceed()
		fmt.Println("<- done")
	}
}

//deco:wrap logged
func Add(a, b int) int { return a + b }

func main() { fmt.Println("Add(2, 3) =", Add(2, 3)) }
```

Run it through deco instead of go:

```sh
deco run .
# -> start
# <- done
# Add(2, 3) = 5
```

That's the whole model. `deco build`, `deco test`, `deco vet` work the same
way — the real go toolchain with the decorators applied through an overlay,
your source untouched.

## Committing the generated code

The overlay is ephemeral. When you want a bare `go build` (CI without deco,
gopls, linters) to see the registrations, materialise them:

```sh
deco generate .        # writes <file>_gen.go next to your sources
deco generate -l .     # CI drift gate: lists files that would change
deco generate -d .     # show diffs without writing
```

Or wire it into `go generate` with no installed binary:

```go
//go:generate go run github.com/paulmanoni/deco generate .
```

## Next steps

- [Using decorators](./using-decorators) — stacking, other packages, external
  libraries.
- [Writing decorators](./writing-decorators) — the three decorator shapes.
- [Methods](./methods) — decorating methods.
