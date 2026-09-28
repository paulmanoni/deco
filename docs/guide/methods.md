# Methods

Methods decorate exactly like functions — pointer or value receiver, exported
or not:

```go
type Counter struct{ total int }

//deco:wrap logged
func (c *Counter) Add(n int) int {
	c.total += n
	return c.total
}
```

## How it works

The chain is built over the **method expression** —
`logged((*Counter).addImpl)` — a plain function value whose first parameter is
the receiver. Two consequences:

- **Every function decorator works on methods unchanged.** `decorators.Func`
  sees one extra leading argument (the receiver); a request-aware
  `decorators.FuncValues` middleware finds the receiver at `args[0]`.
- **Interface satisfaction is preserved.** The generated wrapper is a real
  method with the original signature.

Chain variables are qualified by receiver type (`counterAddDecorated`), so
same-named methods on different types coexist. With
[middleware factories](./writing-decorators#middleware-factories) the wrapper
is reflection-free, calling `c.addImpl(n)` directly inside the fused chain.

## The one exclusion

A **generic receiver** (`func (b *Box[T]) …`) cannot be decorated: a type
parameter prevents the package-level method expression the chain needs. deco
reports a clear `file:line` error. Generic *decorators* (the `[F any]` shape)
are the normal case and unaffected.
