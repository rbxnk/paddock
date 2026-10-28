// Package signin is the sign-in flow: log in with a password or an emailed link, forgot and
// set password, log out, and /login_as in dev. Its pages (kit/ui) render inside the app's
// layout through Render.
package signin

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/a-h/templ"
	"github.com/rbxnk/paddock/account"
	"github.com/rbxnk/paddock/auth"
	"github.com/rbxnk/paddock/mail"
	"github.com/rbxnk/paddock/store"
	"github.com/rbxnk/paddock/ui"
	"github.com/rbxnk/paddock/web"
)

const (
	SessionTTL = 30 * 24 * time.Hour
	ResetTTL   = 24 * time.Hour // reset links, and the app's invites
	SigninTTL  = 15 * time.Minute
)

// Signin holds what the flow needs from the app.
type Signin struct {
	DB     *store.Store
	Users  account.Store
	Cookie string // the session cookie's name
	Dev    bool   // /login_as, the dev login list, cookies over plain http

	Mailer func() (*mail.Mailer, error)
	App    func() (name, url string) // for emails
	// Render shows a page in the app's layout; crumb is "" for an htmx swap's fragment.
	Render func(e *web.Event, status int, content templ.Component, crumb string) error

	Limits auth.Limiter // login failures and emailed links, per IP address and per account
}

func (s *Signin) auth(ctx context.Context) auth.Store {
	r, w := s.DB.Conn(ctx)
	return auth.Store{R: r, W: w}
}

// Mount adds the routes to r.
func Mount[E web.Eventer](r *web.Router[E], s *Signin) {
	h := func(f func(*web.Event) error) web.Handler[E] { return func(e E) error { return f(e.Base()) } }
	r.GET("/login", h(s.getLogin))
	r.POST("/login", h(s.postLogin))
	r.GET("/forgot", h(s.getForgot))
	r.POST("/forgot", h(s.postForgot))
	r.POST("/login/link", h(s.postLoginLink))
	r.GET("/signin", h(s.getSignin))
	r.POST("/signin", h(s.postSignin))
	r.GET("/reset", h(s.getReset))
	r.POST("/reset", h(s.postReset))
	if s.Dev {
		r.POST("/login_as", h(s.postLoginAs))
	}
	r.POST("/logout", h(s.postLogout), SignedIn[E])
}

var cop = http.NewCrossOriginProtection()

// Session blocks cross-origin writes, then finds the signed-in user (e.Auth) and tags the
// request's saves with them for the audit log.
func Session[E web.Eventer](s *Signin) web.Middleware[E] {
	return func(e E, next web.Handler[E]) error {
		b := e.Base()
		if err := cop.Check(b.Request); err != nil {
			return b.ForbiddenError("cross-origin request blocked", err)
		}
		if b.Auth = s.sessionUser(b); b.Auth != nil {
			b.Request = b.Request.WithContext(store.WithActor(b.Request.Context(), "users:"+b.Auth.ID))
		}
		return next(e)
	}
}

// SignedIn sends visitors to the login page, then on to where they were going.
func SignedIn[E web.Eventer](e E, next web.Handler[E]) error {
	b := e.Base()
	if b.Auth == nil {
		return b.Redirect(http.StatusSeeOther, "/login?next="+url.QueryEscape(b.Request.URL.RequestURI()))
	}
	return next(e)
}

// sessionUser is the user signed in with the request's session cookie, or nil.
func (s *Signin) sessionUser(e *web.Event) *account.User {
	c, err := e.Request.Cookie(s.Cookie)
	if err != nil || c.Value == "" {
		return nil
	}
	userId, err := s.auth(e.Request.Context()).Session(e.Request.Context(), c.Value)
	if err != nil {
		if !errors.Is(err, auth.ErrInvalid) {
			log.Println("session:", err)
		}
		return nil
	}
	user, err := s.Users.Get(e.Request.Context(), userId)
	if err != nil {
		return nil
	}
	return &user
}

// EnsureLocal is path if it stays on this site, else "/".
func EnsureLocal(path string) string {
	if len(path) <= 1 {
		return "/"
	}

	u, err := url.Parse(path)
	if err != nil {
		return "/"
	}
	if u.Scheme != "" || u.Host != "" {
		return "/"
	}

	return path
}

func (s *Signin) getLogin(e *web.Event) error {
	return s.renderLogin(e, e.Request.URL.Query().Get("next"), "", "")
}

func (s *Signin) renderLogin(e *web.Event, next, email, msg string) error {
	var users []account.User
	if s.Dev {
		var err error
		if users, err = s.Users.All(e.Request.Context()); err != nil {
			return err
		}
	}
	return s.Render(e, http.StatusOK, ui.Login(next, email, msg, false, users), "log in")
}

// postLogin checks email and password; both mistakes get the same message, so the page
// doesn't tell who has an account.
// Failed tries are limited per IP address and per email; successful ones aren't counted.
func (s *Signin) postLogin(e *web.Event) error {
	email := strings.TrimSpace(e.Request.FormValue("email"))
	next := e.Request.FormValue("next")
	ipKey, emailKey := "login-ip:"+e.RealIP(), "login-email:"+strings.ToLower(email)
	if s.Limits.Over(ipKey, 10, time.Minute) || s.Limits.Over(emailKey, 5, time.Minute) {
		return s.renderLogin(e, next, email, "Too many tries. Wait a minute and try again.")
	}
	// shortcut: a missing account answers faster than a wrong password (no bcrypt), so timing can
	// tell them apart; the failure limits keep that slow to exploit
	user, err := s.Users.CheckPassword(e.Request.Context(), email, e.Request.FormValue("password"))
	if errors.Is(err, account.ErrNotFound) {
		s.Limits.Add(ipKey)
		s.Limits.Add(emailKey)
		return s.renderLogin(e, next, email, "Wrong email or password.")
	} else if err != nil {
		return err
	}
	return s.SignIn(e, user.ID, next)
}

// postLoginLink emails a sign-in link if the address has an account, and answers the
// same either way.
func (s *Signin) postLoginLink(e *web.Event) error {
	email := strings.TrimSpace(e.Request.FormValue("email"))
	if user, err := s.Users.ByEmail(e.Request.Context(), email); err == nil && s.Limits.Allow("link-ip:"+e.RealIP(), 20, time.Hour) {
		if err := s.SendLink(user, "signin"); err != nil {
			log.Println("sign-in email:", err) // don't tell the visitor: it would confirm the account
		}
	}
	return s.Render(e, http.StatusOK, ui.Login(e.Request.FormValue("next"), email, "", true, nil), "log in")
}

// getSignin is where a sign-in email lands. Signing in takes a click, so a mail scanner that
// opens the link doesn't use it up.
func (s *Signin) getSignin(e *web.Event) error {
	token := e.Request.URL.Query().Get("t")
	_, err := s.auth(e.Request.Context()).LinkUser(e.Request.Context(), token, "signin")
	return s.Render(e, http.StatusOK, ui.SignInLink(token, err == nil), "log in")
}

func (s *Signin) postSignin(e *web.Event) error {
	token := e.Request.FormValue("t")
	userId, err := s.auth(e.Request.Context()).UseLink(e.Request.Context(), token, "signin")
	if errors.Is(err, auth.ErrInvalid) {
		return s.Render(e, http.StatusOK, ui.SignInLink(token, false), "")
	} else if err != nil {
		return err
	}
	return s.SignIn(e, userId, "/")
}

// SignIn sets the session cookie and sends the browser on to next.
func (s *Signin) SignIn(e *web.Event, userId string, next string) error {
	token, err := s.auth(e.Request.Context()).NewSession(e.Request.Context(), userId, SessionTTL)
	if err != nil {
		return err
	}
	e.SetCookie(&http.Cookie{
		Name:     s.Cookie,
		Value:    token,
		Path:     "/",
		MaxAge:   int(SessionTTL.Seconds()),
		Secure:   !s.Dev,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	e.Response.Header().Set("HX-Redirect", EnsureLocal(next))
	return e.NoContent(http.StatusOK)
}

func (s *Signin) postLoginAs(e *web.Event) error {
	reqBody := ui.LoginPostBody{Next: e.Request.FormValue("Next"), DevLoginAsId: e.Request.FormValue("DevLoginAsId")}
	if strings.HasPrefix(e.Request.Header.Get("Content-Type"), "application/json") { // the request tests post JSON
		if err := json.NewDecoder(e.Request.Body).Decode(&reqBody); err != nil {
			return e.BadRequestError("", err)
		}
	}

	log.Print("dev login as requested, id ", reqBody.DevLoginAsId)

	user, err := s.Users.Get(e.Request.Context(), reqBody.DevLoginAsId)
	if err != nil {
		return err
	}

	return s.SignIn(e, user.ID, reqBody.Next)
}

func (s *Signin) postLogout(e *web.Event) error {
	if c, err := e.Request.Cookie(s.Cookie); err == nil {
		if err := s.auth(e.Request.Context()).EndSession(e.Request.Context(), c.Value); err != nil {
			return err
		}
	}
	e.SetCookie(&http.Cookie{
		Name:     s.Cookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   !s.Dev,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	e.Response.Header().Set("HX-Redirect", "/")
	e.Response.WriteHeader(200)
	e.Response.Write([]byte("ok"))
	return nil
}

func (s *Signin) getForgot(e *web.Event) error {
	return s.Render(e, http.StatusOK, ui.Forgot(false), "forgot password")
}

// postForgot emails a set-password link if the address has an account, and answers the
// same either way.
func (s *Signin) postForgot(e *web.Event) error {
	if user, err := s.Users.ByEmail(e.Request.Context(), strings.TrimSpace(e.Request.FormValue("email"))); err == nil && s.Limits.Allow("link-ip:"+e.RealIP(), 20, time.Hour) {
		if err := s.SendLink(user, "reset"); err != nil {
			log.Println("set-password email:", err) // don't tell the visitor: it would confirm the account
		}
	}
	return s.Render(e, http.StatusOK, ui.Forgot(true), "forgot password")
}

// SendLink emails the user a set-password link ("reset": reset, invite) or a sign-in link
// ("signin"), at most one a minute and five an hour per user, so a form can't be used to
// flood someone's inbox.
func (s *Signin) SendLink(user account.User, kind string) error {
	minKey, hourKey := "link-min:"+user.ID, "link-hour:"+user.ID
	if s.Limits.Over(minKey, 1, time.Minute) || s.Limits.Over(hourKey, 5, time.Hour) {
		return nil
	}
	s.Limits.Add(minKey)
	s.Limits.Add(hourKey)
	return s.MailLink(user, kind)
}

// MailLink emails the link without SendLink's limits; superusers use it directly.
func (s *Signin) MailLink(user account.User, kind string) error {
	m, err := s.Mailer()
	if err != nil {
		return err
	}
	name, appURL := s.App()
	subject, path, text, button, ttl := "Set your "+name+" password", "/reset", "set your "+html.EscapeString(name)+" password. It works for 24 hours.", "Set password", ResetTTL
	if kind == "signin" {
		subject, path, text, button, ttl = "Sign in to "+name, "/signin", "sign in to "+html.EscapeString(name)+". It works for 15 minutes, once.", "Sign in", SigninTTL
	}
	token, err := s.auth(context.Background()).NewLink(context.Background(), user.ID, kind, ttl)
	if err != nil {
		return err
	}
	return m.Send(user.Email, subject, `<p>Hello,</p>
<p>Use the link below to `+text+`</p>
<p><a href="`+html.EscapeString(appURL+path+"?t="+token)+`">`+button+`</a></p>
<p><i>If you weren't expecting this, you can ignore it.</i></p>`)
}

// getReset is where set-password emails land.
func (s *Signin) getReset(e *web.Event) error {
	token := e.Request.URL.Query().Get("t")
	ok := false
	if userId, err := s.auth(e.Request.Context()).LinkUser(e.Request.Context(), token, "reset"); err == nil {
		_, err = s.Users.Get(e.Request.Context(), userId)
		ok = err == nil
	}
	return s.Render(e, http.StatusOK, ui.SetPassword(token, ok, ""), "set password")
}

// postReset sets the password from an emailed link and signs the user in, ending their other
// sessions. The link is used up in the same transaction as the save, so a rejected form (a
// short password) leaves it working.
func (s *Signin) postReset(e *web.Event) error {
	ctx, token := e.Request.Context(), e.Request.FormValue("t")
	expired := func() error { return s.Render(e, http.StatusOK, ui.SetPassword(token, false, ""), "") }
	userId, err := s.auth(ctx).LinkUser(ctx, token, "reset")
	if errors.Is(err, auth.ErrInvalid) {
		return expired()
	} else if err != nil {
		return err
	}
	user, err := s.Users.Get(ctx, userId)
	if err != nil {
		return expired()
	}
	pw := e.Request.FormValue("password")
	if len(pw) < 8 || len(pw) > 72 { // 72 bytes is bcrypt's limit
		return s.Render(e, http.StatusOK, ui.SetPassword(token, true, "Use 8 to 72 characters."), "")
	}
	err = s.DB.Tx(ctx, func(ctx context.Context) error {
		as := s.auth(ctx)
		if _, err := as.UseLink(ctx, token, "reset"); err != nil {
			return err
		}
		if err := s.Users.SetPassword(ctx, user.ID, pw); err != nil {
			return err
		}
		if err := as.EndLinks(ctx, user.ID, "reset"); err != nil {
			return err
		}
		return as.EndSessions(ctx, user.ID)
	})
	switch {
	case errors.Is(err, auth.ErrInvalid):
		return expired()
	case err != nil:
		return err
	}
	return s.SignIn(e, user.ID, "/")
}
