// Package handlers holds standard net/http handlers, each decorated with
// middleware from a DIFFERENT package via qualified names. This exercises
// cross-folder decoration: the //deco:import directive tells deco where the
// `middleware.*` decorators live, and deco injects that import into the
// generated handlers_gen.go.
package handlers

import (
	"fmt"
	"net/http"
)

//deco:import "github.com/paulmanoni/deco/examples/router/middleware"

// Users is a func(http.ResponseWriter, *http.Request) — the standard handler
// shape — decorated with two stacked, cross-package middleware. Bottom-up:
// Logged (topmost) wraps RequireRole wraps the impl.
//
//@decorate middleware.Logged
//@decorate middleware.RequireRole("admin")
func Users(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "users: alice, bob")
}

// Health is decorated with a single middleware.
//
//@decorate middleware.Logged
func Health(w http.ResponseWriter, r *http.Request) {
	fmt.Fprintln(w, "ok")
}
