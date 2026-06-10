// Package handlers holds standard net/http handlers, each decorated with
// middleware from a DIFFERENT package AND with the router itself. This exercises
// cross-folder decoration: the //deco:import directives tell deco where the
// `middleware.*` and `routing.*` decorators live, and deco injects those
// imports into the generated handlers_gen.go.
package handlers

import (
	"fmt"
	"net/http"
)

//deco:import "github.com/paulmanoni/deco/examples/router/middleware"
//deco:import "github.com/paulmanoni/deco/examples/router/routing"

// Users is decorated with three stacked decorators across two packages. Per the
// bottom-up rule the topmost (routing.Route) is OUTERMOST, so Route registers
// the fully-decorated handler — Route("GET","/users", Logged(RequireRole(impl))).
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
