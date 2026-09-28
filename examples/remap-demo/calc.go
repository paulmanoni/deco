// Package remapdemo demonstrates deco's source-map remapping.
//
// Both functions are decorated (see //deco:wrap), so `deco vet` / `deco test`
// run against deco's transpiled overlay — where each function is renamed and a
// //deco:wrapper marker shifts its body down a line. Yet the positions deco
// reports point back at THIS file at the correct line, not at a temp overlay
// path or a shifted line.
package remapdemo

import "fmt"

func logged[F any](fn F) F { return fn }

//deco:wrap logged
func Div(a, b int) int {
	return a / b // panics when b == 0 — a `deco test` failure points HERE (stdout)
}

//deco:wrap logged
func Warn(code int) {
	fmt.Printf("%s\n", code) // %s with an int — `deco vet` flags THIS line (stderr)
}
