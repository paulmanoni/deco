// Command router is a multi-folder deco example: a tiny HTTP router whose
// handlers (package handlers) are decorated with middleware (package
// middleware) from another folder.
//
// main registers the handlers by their ORIGINAL names on an http.ServeMux —
// after deco runs, those names resolve to the generated wrappers, so every
// request transparently flows through the decorator chain. We drive a couple of
// in-process requests with httptest so the example runs without binding a port.
package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"

	"github.com/paulmanoni/deco/examples/router/handlers"
)

func main() {
	mux := http.NewServeMux()
	// handlers.Users / handlers.Health are the decorated wrappers post-deco.
	mux.HandleFunc("/users", handlers.Users)
	mux.HandleFunc("/health", handlers.Health)

	for _, path := range []string{"/health", "/users"} {
		fmt.Printf("GET %s\n", path)
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		fmt.Printf("  -> %d %s", rec.Code, rec.Body.String())
	}
}
