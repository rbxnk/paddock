package web

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type appEvent struct {
	*Event
	trail []string
}

func TestRouter(t *testing.T) {
	mux := http.NewServeMux()
	mark := func(name string) Middleware[*appEvent] {
		return func(e *appEvent, next Handler[*appEvent]) error { e.trail = append(e.trail, name); return next(e) }
	}
	r := &Router[*appEvent]{Mux: mux, MW: []Middleware[*appEvent]{RecoverPanic[*appEvent], HTMXErrors[*appEvent], mark("a")}, New: func(e *Event) *appEvent { return &appEvent{Event: e} }}
	g := r.Group("/g", mark("b"))
	g.GET("/ok", func(e *appEvent) error {
		return e.Stream(200, "text/plain", strings.NewReader(strings.Join(e.trail, ",")))
	}, mark("c"))
	g.GET("/panic", func(e *appEvent) error { panic("boom") })
	g.GET("/fail", func(e *appEvent) error { return NewError(http.StatusConflict, "changed meanwhile", errors.New("x")) })

	get := func(path string, htmx bool) *httptest.ResponseRecorder {
		req := httptest.NewRequest("GET", path, nil)
		if htmx {
			req.Header.Set("HX-Request", "true")
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, req)
		return w
	}
	if w := get("/g/ok", false); w.Body.String() != "a,b,c" {
		t.Errorf("middleware order: %q", w.Body.String())
	}
	if w := get("/g/panic", false); w.Code != 400 { // AsError: unknown errors are 400s
		t.Errorf("panic: %d", w.Code)
	}
	if w := get("/g/fail", true); w.Code != 409 || w.Header().Get("HX-Retarget") != "next .error-tgt" || !strings.Contains(w.Body.String(), "Changed meanwhile.") {
		t.Errorf("htmx error: %d %v %q", w.Code, w.Header(), w.Body.String())
	}
	if w := get("/g/fail", false); w.Code != 409 || !strings.Contains(w.Body.String(), `"status":409`) {
		t.Errorf("plain error: %d %q", w.Code, w.Body.String())
	}
}
