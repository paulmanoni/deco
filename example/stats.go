package main

// MinMax is variadic AND returns multiple values, decorated with qualified
// library decorators (resolved via the //deco:import directive in math.go).
//
//deco:wrap decorators.Logged
//deco:wrap decorators.Timing("minmax")
func MinMax(nums ...int) (int, int) {
	if len(nums) == 0 {
		return 0, 0
	}
	lo, hi := nums[0], nums[0]
	for _, n := range nums[1:] {
		if n < lo {
			lo = n
		}
		if n > hi {
			hi = n
		}
	}
	return lo, hi
}
