package main

import "fmt"

// Greet is a func(string) with NO return value, decorated by a CUSTOM
// same-package decorator (audited) built with decorators.Func — no reflection.
//
//deco:wrap audited
func Greet(name string) {
	fmt.Printf("Hello, %s!\n", name)
}
