package main

import (
	"fmt"

	"github.com/paulmanoni/deco/decorators"
)

// audited is a CUSTOM decorator, written with decorators.Func — no reflection.
// It is referenced by its bare, same-package name: //@decorate audited.
func audited[F any](fn F) F {
	return decorators.Func(fn, func(proceed func()) {
		fmt.Println("[audit] start")
		proceed()
		fmt.Println("[audit] done")
	})
}
