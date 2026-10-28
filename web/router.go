package web

import (
	"bytes"
	"errors"
	"fmt"
	"log"
	"net/http"
	"runtime/debug"
	"slices"

	"github.com/a-h/templ"
	"github.com/rbxnk/paddock/ui"
)

// Eventer is an app's event type: a struct embedding *Event, with the app's own fields.
type Eventer interface{ Base() *Event }

type Handler[E Eventer] func(E) error

// Middleware runs around a handler; it calls next to go on.
type Middleware[E Eventer] func(e E, next Handler[E]) error

// Router registers routes on Mux under Prefix, each wrapped in MW. New makes the app's event
// for a request.
type Router[E Eventer] struct {
	Mux    *http.ServeMux
	Prefix string
	MW     []Middleware[E]
	New    func(*Event) E
}

func (r *Router[E]) Group(prefix string, mw ...Middleware[E]) *Router[E] {
	return &Router[E]{r.Mux, r.Prefix + prefix, append(slices.Clone(r.MW), mw...), r.New}
}

func (r *Router[E]) GET(path string, h Handler[E], mw ...Middleware[E]) {
	r.Handle("GET "+r.Prefix+path, h, mw)
}

func (r *Router[E]) POST(path string, h Handler[E], mw ...Middleware[E]) {
	r.Handle("POST "+r.Prefix+path, h, mw)
}

func (r *Router[E]) Handle(pattern string, h Handler[E], mw []Middleware[E]) {
	all := append(slices.Clone(r.MW), mw...)
	for i := len(all) - 1; i >= 0; i-- {
		m, next := all[i], h
		h = func(e E) error { return m(e, next) }
	}
	r.Mux.HandleFunc(pattern, func(w http.ResponseWriter, req *http.Request) {
		e := r.New(New(w, req))
		if err := h(e); err != nil {
			e.Base().WriteError(err)
		}
	})
}

// RecoverPanic turns a handler's panic into an error, with its stack, for WriteError and the log.
func RecoverPanic[E Eventer](e E, next Handler[E]) (err error) {
	defer func() {
		if p := recover(); p != nil {
			err = fmt.Errorf("panic: %v\n%s", p, debug.Stack())
		}
	}()
	return next(e)
}

// LimitBody caps request bodies at 32 MiB.
func LimitBody[E Eventer](e E, next Handler[E]) error {
	b := e.Base()
	b.Request.Body = http.MaxBytesReader(b.Response, b.Request.Body, 32<<20)
	return next(e)
}

// HTMXErrors answers htmx requests that fail with an alert in the page's error target,
// instead of an error body htmx wouldn't show.
func HTMXErrors[E Eventer](e E, next Handler[E]) error {
	err := next(e)
	b := e.Base()
	if err == nil || b.Request.Header.Get("HX-Request") == "" || b.Status() != 0 {
		return err
	}
	status, msg := http.StatusInternalServerError, "server error"
	if apiError, ok := errors.AsType[*Error](err); ok {
		status, msg = apiError.Status, apiError.Message
	}
	log.Println(err)

	h := b.Response.Header()
	h.Set("HX-Retarget", "next .error-tgt")
	h.Set("HX-Reswap", "innerHTML")
	h.Set("HX-Reselect", ".alert") // the trigger may hx-select something else
	return b.Render(status, ui.Alert(msg))
}

// Render sends content as HTML.
func (e *Event) Render(status int, content templ.Component) error {
	buf := new(bytes.Buffer)
	if err := content.Render(e.Request.Context(), buf); err != nil {
		return e.InternalServerError("render failed", err)
	}
	return e.Stream(status, "text/html; charset=utf-8", buf)
}
