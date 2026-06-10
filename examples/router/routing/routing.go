// Package routing turns the router itself into a decorator. Annotating a
// handler with //@decorate routing.Route("GET", "/users") registers it — the
// classic Flask @app.route pattern, but resolved at compile time by deco.
//
// Because deco builds each decorator chain ONCE at package init, Route runs at
// startup (when the handler's package is imported), registering the handler.
// It returns the function unchanged, so the decorated name still works if
// called directly and any inner middleware stays in effect.
package routing

import (
	"net/http"
	"sort"
)

type route struct {
	method, path string
	h            http.HandlerFunc
}

var routes []route

// Route registers fn for method+path and returns it unchanged. fn is expected
// to be an http handler func; non-handlers are returned without registration.
func Route[F any](method, path string, fn F) F {
	if h, ok := any(fn).(func(http.ResponseWriter, *http.Request)); ok {
		routes = append(routes, route{method: method, path: path, h: h})
	}
	return fn
}

// Mux builds an http.ServeMux from every route registered via Route. Patterns
// use Go 1.22+ "METHOD /path" syntax. Routes are sorted for deterministic
// registration.
func Mux() *http.ServeMux {
	sort.Slice(routes, func(i, j int) bool {
		if routes[i].path != routes[j].path {
			return routes[i].path < routes[j].path
		}
		return routes[i].method < routes[j].method
	})
	mux := http.NewServeMux()
	for _, r := range routes {
		mux.HandleFunc(r.method+" "+r.path, r.h)
	}
	return mux
}
