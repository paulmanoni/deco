// Command router is a multi-folder deco example: a tiny HTTP router whose
// handlers (package handlers) are decorated with middleware (package
// middleware) AND with the router itself (package routing).
//
// Note there is no manual route wiring here. Importing the handlers package is
// enough: each handler's //@decorate routing.Route("GET", "/path") runs at
// package init (deco builds each decorator chain once), registering the
// handler — the Flask @app.route pattern. main just asks routing for the mux.
//
// We drive a couple of in-process requests with httptest so the example runs
// without binding a port.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	_ "github.com/paulmanoni/deco/examples/router/handlers" // import for route registration
	"github.com/paulmanoni/deco/examples/router/routing"
)

func main() {
	mux := routing.Mux() // every //@decorate routing.Route(...) is already registered

	get := func(path, role string) {
		if role == "" {
			fmt.Printf("GET %s\n", path)
		} else {
			fmt.Printf("GET %s  (X-Role: %s)\n", path, role)
		}
		req := httptest.NewRequest(http.MethodGet, path, nil)
		if role != "" {
			req.Header.Set("X-Role", role)
		}
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		fmt.Printf("  -> %d %s", rec.Code, rec.Body.String())
	}

	get("/health", "")        // no auth required
	get("/users", "")         // request-aware auth denies: no X-Role header
	get("/users", "admin")    // allowed: header matches RequireRole("admin")
}
