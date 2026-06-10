// Package decorators ships a couple of reflection-based example decorators.
//
// A decorator is a generic function that takes the wrapped function (plus any
// optional leading arguments) and returns a function of the *same* signature:
//
//	func Logged[F any](fn F) F { ... }
//	func Timing[F any](label string, fn F) F { ... }
//
// Because they work purely through reflection on the underlying function value,
// these decorators are signature-agnostic: they wrap func(int, int) int just as
// happily as func(...string) (Foo, error). This is the only place in the whole
// project that uses the reflect package.
package decorators

import (
	"fmt"
	"reflect"
	"time"
)

// callThrough invokes the wrapped function value with the given (already
// reflected) arguments, transparently handling variadic functions. When
// reflect.MakeFunc hands us the arguments of a variadic function, the final
// element is the variadic slice, so we must forward it with CallSlice.
func callThrough(fn reflect.Value, args []reflect.Value) []reflect.Value {
	if fn.Type().IsVariadic() {
		return fn.CallSlice(args)
	}
	return fn.Call(args)
}

// wrap builds a new function value with the exact same type as fn, running
// before/after hooks around the underlying call. The before hook may return a
// closing func that runs once the call returns (handy for timing).
func wrap[F any](fn F, before func() (after func())) F {
	v := reflect.ValueOf(fn)
	t := v.Type()
	if t.Kind() != reflect.Func {
		// A decorator only makes sense on a function value; fail loudly rather
		// than silently mis-behaving.
		panic(fmt.Sprintf("deco: decorator applied to non-function %s", t))
	}
	wrapped := reflect.MakeFunc(t, func(args []reflect.Value) []reflect.Value {
		var after func()
		if before != nil {
			after = before()
		}
		out := callThrough(v, args)
		if after != nil {
			after()
		}
		return out
	})
	return wrapped.Interface().(F)
}

// Logged returns a wrapper that prints a line when the function is entered and
// another when it returns. It works for any function signature.
func Logged[F any](fn F) F {
	name := reflect.TypeOf(fn).String()
	return wrap(fn, func() func() {
		fmt.Printf("[log] -> calling %s\n", name)
		return func() {
			fmt.Printf("[log] <- returned from %s\n", name)
		}
	})
}

// Timing returns a wrapper that measures and prints how long the wrapped
// function took, tagged with label. label is the leading argument supplied in
// the annotation, e.g. //@decorate timing("slow").
func Timing[F any](label string, fn F) F {
	return wrap(fn, func() func() {
		start := time.Now()
		return func() {
			fmt.Printf("[time] %s took %s\n", label, time.Since(start))
		}
	})
}