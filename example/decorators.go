package main

import "github.com/paulmanoni/deco/decorators"

// These thin, same-package aliases let annotations reference decorators by a
// bare name (//@decorate logged) while the real, reflection-based
// implementations live in the reusable deco/decorators library.
//
// Because the generated wrappers live in this same package, they can call
// `logged` / `timing` directly with no extra imports — which keeps the
// generated files dead simple. The type parameter F flows straight through to
// the library, so the wrappers stay fully type-safe for any signature.

func logged[F any](fn F) F { return decorators.Logged(fn) }

func timing[F any](label string, fn F) F { return decorators.Timing(label, fn) }
