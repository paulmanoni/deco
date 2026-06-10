package main

// MinMax is variadic (...int) AND returns MULTIPLE values (int, int),
// exercising both the variadic-forwarding and multi-return paths at once.
//
//@decorate logged
//@decorate timing("minmax")
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
