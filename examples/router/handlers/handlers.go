// Package handlers holds standard net/http handlers, each decorated with
// middleware AND with the router itself — from DIFFERENT packages, referenced
// by qualified names with NO //deco:import directive. deco auto-resolves
// middleware and routing to their module import paths via `go list`.
package handlers

import (
	"fmt"
	"net/http"
)

// Users stacks three decorators across two packages. Bottom-up: routing.Route
// (topmost) is outermost, so it registers the fully-decorated handler.
//
//@decorate routing.Route("GET", "/users")
//@decorate middleware.Logged
//@decorate middleware.RequireRole("admin")
func Users(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "users: alice, bob")
}

// Health registers itself with the router and is logged.
//
//@decorate routing.Route("GET", "/health")
//@decorate middleware.Logged
func Health(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ok")
}
