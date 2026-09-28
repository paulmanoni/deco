package main

// Counter shows METHOD decoration: the chain is built over the method
// expression ((*Counter).addImpl — a plain func whose first argument is the
// receiver), so the same decorators that wrap functions wrap methods too.
// Note decorators.Logged needs no import here — the wrapper file imports it.
type Counter struct {
	total int
}

//deco:wrap audited
func (c *Counter) Add(n int) int {
	c.total += n
	return c.total
}

//deco:wrap decorators.Logged
func (c Counter) Total() int {
	return c.total
}
