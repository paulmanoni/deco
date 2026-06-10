package main

import "fmt"

// main calls each decorated function by its ORIGINAL name. After running
// deco, those names resolve to the generated wrappers, so every call
// transparently flows through the decorator chain — watch for the [log] and
// [time] lines printed by the decorators around each result.
func main() {
	fmt.Println("== Add (func(int, int) int) ==")
	sum := Add(2, 3)
	fmt.Println("Add(2, 3) =", sum)

	fmt.Println("\n== Greet (func(string)) ==")
	Greet("world")

	fmt.Println("\n== MinMax (func(...int) (int, int)) ==")
	lo, hi := MinMax(3, 1, 4, 1, 5, 9, 2, 6)
	fmt.Printf("MinMax(3,1,4,1,5,9,2,6) = lo:%d hi:%d\n", lo, hi)
}
