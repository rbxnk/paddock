package web

import (
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// Errors go out as the JSON body, with sentence-cased messages, and a missing row as 404.
func TestWriteError(t *testing.T) {
	for _, c := range []struct {
		err    error
		status int
		body   string
	}{
		{(&Event{}).NotFoundError("link not found", nil), 404, `{"data":{},"message":"Link not found.","status":404}`},
		{(&Event{}).BadRequestError("", nil), 400, `{"data":{},"message":"Something went wrong while processing your request.","status":400}`},
		{fmt.Errorf("load: %w", sql.ErrNoRows), 404, `{"data":{},"message":"The requested resource wasn't found.","status":404}`},
		{errors.New("boom"), 400, `{"data":{},"message":"Something went wrong while processing your request.","status":400}`},
	} {
		w := httptest.NewRecorder()
		e := New(w, httptest.NewRequest("GET", "/", nil))
		e.WriteError(c.err)
		if w.Code != c.status || w.Body.String() != c.body || w.Header().Get("Content-Type") != "application/json" {
			t.Errorf("%v: %d %s", c.err, w.Code, w.Body)
		}
		if e.Status() != c.status {
			t.Errorf("%v: status %d", c.err, e.Status())
		}
	}

	// nothing is added after a response that's already out
	w := httptest.NewRecorder()
	e := New(w, httptest.NewRequest("GET", "/", nil))
	e.NoContent(http.StatusNoContent)
	e.WriteError(errors.New("late"))
	if w.Code != http.StatusNoContent || w.Body.Len() != 0 {
		t.Errorf("error after a response: %d %q", w.Code, w.Body)
	}
}

func TestStatic(t *testing.T) {
	h := Static(fstest.MapFS{"dist/app.js": {Data: []byte("js")}})
	for path, want := range map[string]int{"/dist/app.js": 200, "/dist/": 404, "/dist": 404, "/": 404, "/nope.js": 404} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != want {
			t.Errorf("%s: %d, want %d", path, w.Code, want)
		}
	}
	fsys := fstest.MapFS{"dist/app.js": {Data: []byte("js")}}
	url, err := AssetURL(fsys, "/static/", "dist/app.js")
	if err != nil || url != "/static/dist/app.js?v=16cedf80" {
		t.Fatalf("asset url %q %v", url, err)
	}
	if _, err := AssetURL(fsys, "/static/", "nope.js"); err == nil {
		t.Error("a missing asset has a url")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/dist/app.js?v=16cedf80", nil))
	if cc := w.Header().Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Errorf("versioned: Cache-Control %q", cc)
	}
	w = httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/dist/app.js", nil))
	if cc := w.Header().Get("Cache-Control"); cc != "" {
		t.Errorf("unversioned: Cache-Control %q", cc)
	}
}

func TestRealIP(t *testing.T) {
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "10.0.0.1:1234"
	r.Header.Set("Fly-Client-IP", "203.0.113.7")
	e := New(httptest.NewRecorder(), r)
	if ip := e.RealIP(); ip != "10.0.0.1" {
		t.Errorf("without a trusted header: %s", ip)
	}
	ClientIPHeader = "Fly-Client-IP"
	defer func() { ClientIPHeader = "" }()
	if ip := e.RealIP(); ip != "203.0.113.7" {
		t.Errorf("with Fly-Client-IP trusted: %s", ip)
	}
}
