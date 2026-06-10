// Package middleware provides decorator-style HTTP middleware built on the
// decorators toolkit (no hand-written reflection here): Logged uses Func, while
// RequireRole uses FuncValues so it can read the request.
package middleware

import (
	"fmt"
	"net/http"

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

// RequireRole is a REQUEST-AWARE decorator built on decorators.FuncValues: it
// reads the *http.Request from the handler's arguments and checks the X-Role
// header, denying the request (and short-circuiting the handler) when it does
// not match. role is the leading annotation argument, e.g.
// //@decorate middleware.RequireRole("admin").
func RequireRole[F any](role string, fn F) F {
	return decorators.FuncValues(fn, func(args []any, proceed func([]any) []any) []any {
		var w http.ResponseWriter
		var r *http.Request
		for _, a := range args {
			switch v := a.(type) {
			case http.ResponseWriter:
				w = v
			case *http.Request:
				r = v
			}
		}
		if r == nil || r.Header.Get("X-Role") != role {
			fmt.Printf("  [mw] auth: DENY (need role %q)\n", role)
			if w != nil {
				w.WriteHeader(http.StatusForbidden)
				fmt.Fprintf(w, "forbidden: need role %q\n", role)
			}
			return nil // short-circuit: handler never runs
		}
		fmt.Printf("  [mw] auth: allow role %q\n", role)
		return proceed(args)
	})
}
