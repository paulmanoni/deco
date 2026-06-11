// Package remapdemo demonstrates deco's source-map remapping.
//
// Both functions are decorated (see //@decorate), so `deco vet` / `deco test`
// run against deco's transpiled overlay — where each function is renamed and a
// //deco:wrapper marker shifts its body down a line. Yet the positions deco
// reports point back at THIS file at the correct line, not at a temp overlay
// path or a shifted line.
package remapdemo

import "fmt"

func logged[F any](fn F) F { return fn }

// @decorate logged
func Div(a, b int) int {
	return a / b // panics when b == 0 — a `deco test` failure points HERE (stdout)
}

// @decorate logged
func Warn(code int) {
	fmt.Printf("%s\n", code) // %s with an int — `deco vet` flags THIS line (stderr)
}
