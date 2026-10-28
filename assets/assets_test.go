package assets

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHandler(t *testing.T) {
	u, err := URL("htmx.min.js")
	if err != nil || !strings.HasPrefix(u, "/static/kit/htmx.min.js?v=") {
		t.Fatal(u, err)
	}
	mux := http.NewServeMux()
	mux.Handle("GET "+Prefix, Handler())
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, httptest.NewRequest("GET", u, nil))
	if w.Code != 200 || !strings.Contains(w.Header().Get("Cache-Control"), "immutable") || w.Body.Len() < 1000 {
		t.Errorf("%d %v %d bytes", w.Code, w.Header(), w.Body.Len())
	}
	for _, path := range []string{"/static/kit/daisyui.mjs", "/static/kit/"} {
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != 404 {
			t.Errorf("%s: %d", path, w.Code)
		}
	}
}
