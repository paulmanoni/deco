// Package middleware provides decorator-style HTTP middleware. Each is a
// generic decorator built on decorators.Func (no reflection here), so it can
// wrap a handler of any signature while preserving its type.
package middleware

import (
	"fmt"

	"github.com/paulmanoni/deco/decorators"
)

// Logged announces every request the wrapped handler serves.
func Logged[F any](fn F) F {
	return decorators.Func(fn, func(proceed func()) {
		fmt.Println("  [mw] log: handling request")
		proceed()
		fmt.Println("  [mw] log: done")
	})
}

// RequireRole is a parameterised decorator: the role is the leading argument
// supplied in the annotation, e.g. //@decorate middleware.RequireRole("admin").
func RequireRole[F any](role string, fn F) F {
	return decorators.Func(fn, func(proceed func()) {
		fmt.Printf("  [mw] auth: require role %q — ok\n", role)
		proceed()
	})
}
