package main

import "fmt"

// Greet is a func(string) with NO return value — exercising the zero-result
// path of the transpiler.
//
//@decorate logged
func Greet(name string) {
	fmt.Printf("Hello, %s!\n", name)
}
