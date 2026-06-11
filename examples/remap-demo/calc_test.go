package remapdemo

import "testing"

func TestDiv(t *testing.T) {
	if got := Div(6, 2); got != 3 {
		t.Fatalf("Div(6, 2) = %d, want 3", got)
	}
	_ = Div(1, 0) // intentional divide-by-zero panic to demonstrate stack remapping
}
