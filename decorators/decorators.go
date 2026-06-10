// Package decorators provides a tiny toolkit for writing deco decorators.
//
// A decorator is a generic function that takes the wrapped function (plus any
// optional leading arguments) and returns a function of the *same* signature:
//
//	func Logged[F any](fn F) F { ... }
//	func Timing[F any](label string, fn F) F { ... }
//
// You normally do NOT write reflection yourself. Instead, build your decorator
// on top of Func, which does the reflection once and lets you express the
// behaviour as ordinary middleware:
//
//	func myDecorator[F any](fn F) F {
//		return decorators.Func(fn, func(proceed func()) {
//			// ...before...
//			proceed()
//			// ...after...
//		})
//	}
//
// This is the only place in the project that imports reflect.
package decorators

import (
	"fmt"
	"reflect"
	"time"
)

// Func turns ordinary middleware into a signature-preserving decorator, so you
// never have to touch reflect. It returns a function with the exact same type
// as fn; calling it runs mw, which is handed a proceed() thunk that invokes the
// wrapped function. Call proceed:
//
//   - around your own logic — for logging, timing, tracing, auth checks;
//   - more than once — for retries;
//   - inside a recover — to swallow panics;
//   - not at all — to short-circuit (the decorated call then returns the
//     zero value for each of its result types).
//
// Func works for ANY signature, including variadic functions and any number of
// results.
func Func[F any](fn F, mw func(proceed func())) F {
	v := reflect.ValueOf(fn)
	t := v.Type()
	if t.Kind() != reflect.Func {
		// A decorator only makes sense on a function value; fail loudly rather
		// than silently mis-behaving.
		panic(fmt.Sprintf("deco: decorator applied to non-function %s", t))
	}
	wrapped := reflect.MakeFunc(t, func(in []reflect.Value) []reflect.Value {
		var out []reflect.Value
		mw(func() {
			// reflect.MakeFunc hands a variadic function its final argument as a
			// slice, so forward it with CallSlice; otherwise a plain Call.
			if t.IsVariadic() {
				out = v.CallSlice(in)
			} else {
				out = v.Call(in)
			}
		})
		if out == nil {
			// mw never proceeded: return a correctly-typed zero value per result
			// so the synthesized function still satisfies its signature.
			out = make([]reflect.Value, t.NumOut())
			for i := range out {
				out[i] = reflect.Zero(t.Out(i))
			}
		}
		return out
	})
	return wrapped.Interface().(F)
}

// FuncValues is like Func, but exposes the call's arguments and return values
// as []any so a decorator can inspect or replace them — for example,
// request-aware HTTP middleware that reads the *http.Request from args.
//
// mw receives the incoming arguments and a proceed callback that runs the
// wrapped function (optionally with different arguments) and returns its
// results. Whatever mw returns becomes the decorated call's results; return
// fewer or nil values and the missing results default to their zero value. Skip
// calling proceed to short-circuit entirely.
//
// Each returned value must be assignable to the corresponding result type. For
// a variadic function, the final element of args is the variadic slice.
func FuncValues[F any](fn F, mw func(args []any, proceed func(args []any) []any) []any) F {
	v := reflect.ValueOf(fn)
	t := v.Type()
	if t.Kind() != reflect.Func {
		panic(fmt.Sprintf("deco: decorator applied to non-function %s", t))
	}
	wrapped := reflect.MakeFunc(t, func(in []reflect.Value) []reflect.Value {
		proceed := func(args []any) []any {
			return toAny(callThrough(v, toValues(t, args)))
		}
		return toResults(t, mw(toAny(in), proceed))
	})
	return wrapped.Interface().(F)
}

// callThrough invokes fn with the given reflected arguments, forwarding the
// variadic slice with CallSlice when needed.
func callThrough(fn reflect.Value, args []reflect.Value) []reflect.Value {
	if fn.Type().IsVariadic() {
		return fn.CallSlice(args)
	}
	return fn.Call(args)
}

// toAny converts reflected values to plain interface values.
func toAny(vs []reflect.Value) []any {
	out := make([]any, len(vs))
	for i, v := range vs {
		out[i] = v.Interface()
	}
	return out
}

// toValues converts argument interfaces back to reflected values matching fn's
// parameter types, using a typed zero value for nil entries.
func toValues(t reflect.Type, args []any) []reflect.Value {
	out := make([]reflect.Value, len(args))
	for i, a := range args {
		if a == nil {
			out[i] = reflect.Zero(paramType(t, i))
		} else {
			out[i] = reflect.ValueOf(a)
		}
	}
	return out
}

// paramType returns fn's i-th parameter type (for a variadic function the final
// declared parameter type is the slice).
func paramType(t reflect.Type, i int) reflect.Type {
	if i < t.NumIn() {
		return t.In(i)
	}
	return reflect.TypeFor[any]()
}

// toResults converts the decorator's returned interfaces to reflected values of
// fn's result types, defaulting missing or nil entries to the zero value.
func toResults(t reflect.Type, results []any) []reflect.Value {
	out := make([]reflect.Value, t.NumOut())
	for i := range out {
		if i < len(results) && results[i] != nil {
			out[i] = reflect.ValueOf(results[i])
		} else {
			out[i] = reflect.Zero(t.Out(i))
		}
	}
	return out
}

// Logged returns a wrapper that prints a line when the function is entered and
// another when it returns. It works for any function signature.
func Logged[F any](fn F) F {
	name := reflect.TypeOf(fn).String()
	return Func(fn, func(proceed func()) {
		fmt.Printf("[log] -> calling %s\n", name)
		proceed()
		fmt.Printf("[log] <- returned from %s\n", name)
	})
}

// Timing returns a wrapper that measures and prints how long the wrapped
// function took, tagged with label. label is the leading argument supplied in
// the annotation, e.g. //@decorate timing("slow").
func Timing[F any](label string, fn F) F {
	return Func(fn, func(proceed func()) {
		start := time.Now()
		proceed()
		fmt.Printf("[time] %s took %s\n", label, time.Since(start))
	})
}
