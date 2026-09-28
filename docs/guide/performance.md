# Performance

deco generates plain Go and calls whatever decorator you name — reflection is
a property of *how the decorator is written*, not of deco. Indicative numbers
(Apple M-series, `go test -bench . ./decorators/`):

| decorator | ns/op | allocs/op |
|-----------|------:|----------:|
| raw function (none) | ~2 | 0 |
| concrete, typed decorator | ~2 | 0 |
| **three factories, fused wrapper (`Run`)** | **~52** | **4** |
| one `Func` layer | ~285 | 7 |
| three middleware in one `Chain` layer | ~320 | 9 |
| `FuncValues` (args/results boxed) | ~385 | 10 |
| three stacked `Func` layers | ~900 | 21 |

For I/O-bound work (HTTP handlers, database calls) any of these is noise.

## Fusion: what the generated code looks like

Stacked wrap-style decorators each pay a reflective boxing:

```go
// three layers, three boxings per call
var addImplDecorated = logged(traced(timing("slow", addImpl)))
```

When every decorator is a
[middleware factory](./writing-decorators#middleware-factories), deco emits a
reflection-free wrapper instead — the stack is one slice built at init, run
over a typed call:

```go
var addImplMWs = []decorators.Middleware{logged(), traced(), timing("slow")}

func Add(a, b int) (r0 int) {
	decorators.Run(addImplMWs, func() { r0 = addImpl(a, b) })
	return
}
```

Depth is essentially free: ten factories cost about the same as one. A mixed
stack fuses each run of consecutive factories into a single
`decorators.Chain` layer, with wrap-style decorators nesting around it.

## Practical levers

1. **Write factories** — it's the same middleware body, minus the `Func` call.
2. For the last nanoseconds, write a decorator typed to the exact signature
   (~2 ns, zero allocs).
3. Decorate coarse entry points. A recursive decorated function re-enters the
   chain on every self-call (`Fact(20)`: ~16 ns → ~6.7 µs undecorated vs
   reflective chain), so don't decorate a hot recursive helper.
