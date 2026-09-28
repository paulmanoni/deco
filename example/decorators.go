package main

import (
	"fmt"

	"github.com/paulmanoni/deco/decorators"
)

// audited is a CUSTOM decorator in the middleware-FACTORY shape: it returns
// its behaviour instead of wrapping a function. Referenced by its bare,
// same-package name (//deco:wrap audited), and because it is a factory, a
// stack of such decorators fuses — the generated wrapper runs them over a
// typed call with no reflection at all.
func audited() decorators.Middleware {
	return func(proceed func()) {
		fmt.Println("[audit] start")
		proceed()
		fmt.Println("[audit] done")
	}
}
