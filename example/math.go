package main

// Tell deco where the qualified `decorators.*` decorators come from. This
// directive is package-wide; one is enough for the whole example.
//deco:import "github.com/paulmanoni/deco/decorators"

// Add is a func(int, int) int decorated with LIBRARY decorators referenced by
// their qualified names — no same-package alias needed. Bottom-up: Logged
// (topmost) is outermost, i.e. decorators.Logged(decorators.Timing("slow", …)).
//
// @decorate decorators.Logged
// @decorate decorators.Timing("slow")
func Add(a, b int) int {
	return a + b
}
