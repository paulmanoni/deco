package main

// Add is a func(int, int) int. Two stacked decorators: per the bottom-up rule,
// the topmost annotation (logged) becomes the OUTERMOST wrapper, so the
// effective chain is logged(timing("slow", Add)).
//
//@decorate logged
//@decorate timing("slow")
func Add(a, b int) int {
	return a + b
}
