package main

import (
	"context"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/rbxnk/paddock/account"
	"github.com/rbxnk/paddock/auth"
)

// TestSignIn signs in with a password and sees the home page as that user.
func TestSignIn(t *testing.T) {
	ctx := context.Background()
	a, err := newApp(t.TempDir()+"/t.db", true)
	if err != nil {
		t.Fatal(err)
	}
	defer a.db.Close()
	if err := migrateUp(ctx, a.db.W); err != nil {
		t.Fatal(err)
	}
	hash, _ := auth.HashPassword("hunter2hunter2")
	if err := a.users.Create(ctx, &account.User{Email: "ada@example.com"}, hash); err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(a.routes())
	defer srv.Close()
	jar, _ := cookiejar.New(nil)
	c := &http.Client{Jar: jar}
	get := func() string {
		res, err := c.Get(srv.URL + "/")
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		b, _ := io.ReadAll(res.Body)
		return string(b)
	}
	if body := get(); !strings.Contains(body, "Log in") {
		t.Errorf("visitor home: %s", body)
	}
	res, err := c.PostForm(srv.URL+"/login", url.Values{"email": {"ada@example.com"}, "password": {"hunter2hunter2"}})
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != 200 || res.Header.Get("HX-Redirect") != "/" {
		t.Fatalf("login: %d %v", res.StatusCode, res.Header)
	}
	if body := get(); !strings.Contains(body, "Signed in as ada@example.com") {
		t.Errorf("signed-in home: %s", body)
	}
}
