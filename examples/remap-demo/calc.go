// Package remapdemo demonstrates deco's source-map remapping.
//
// Div is decorated (see //@decorate), so `deco test` / `deco vet` run against
// deco's transpiled overlay — yet the positions they report point back at THIS
// file at the correct line, not at a temp overlay path or a shifted line.
package remapdemo

func logged[F any](fn F) F { return fn }

// Div
// @decorate logged
func Div(a, b int) int {
	return a / b // <- this line panics when b == 0; a test failure should point here
}
