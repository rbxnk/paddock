// Package web is the app's HTTP plumbing on net/http: the request a handler gets, its
// errors and how they're sent (a JSON body: data, message, status), the router and
// middleware, and static files.
package web

import (
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/rbxnk/paddock/account"
)

// Event is one request, as a handler sees it.
type Event struct {
	Request  *http.Request
	Response http.ResponseWriter
	Auth     *account.User // the signed-in user (signin.Session), nil if none
	status   int
}

// Base is the Event itself; an app's own event type embeds *Event and so has it too (Router).
func (e *Event) Base() *Event { return e }

// New wraps a request; the response remembers its status for logging.
func New(w http.ResponseWriter, r *http.Request) *Event {
	e := &Event{Request: r}
	e.Response = &statusWriter{w, e}
	return e
}

type statusWriter struct {
	http.ResponseWriter
	e *Event
}

func (w *statusWriter) WriteHeader(status int) {
	if w.e.status == 0 {
		w.e.status = status
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *statusWriter) Write(b []byte) (int, error) {
	if w.e.status == 0 {
		w.e.status = http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}

func (w *statusWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// Status is the status sent so far, 0 before anything is.
func (e *Event) Status() int { return e.status }

// ClientIPHeader names the header the proxy in front sets to the client's address
// ("Fly-Client-IP"); empty means the connection's address is the client's.
var ClientIPHeader string

// RealIP is the client's address: ClientIPHeader's when set, else the connection's.
func (e *Event) RealIP() string {
	ip := ""
	if ClientIPHeader != "" {
		ip = strings.TrimSpace(e.Request.Header.Get(ClientIPHeader))
	}
	if ip == "" {
		ip, _, _ = net.SplitHostPort(e.Request.RemoteAddr)
	}
	parsed, err := netip.ParseAddr(ip)
	if err != nil {
		return ip
	}
	return parsed.StringExpanded()
}

func (e *Event) SetCookie(c *http.Cookie) { http.SetCookie(e.Response, c) }

func (e *Event) Stream(status int, contentType string, r io.Reader) error {
	e.Response.Header().Set("Content-Type", contentType)
	e.Response.WriteHeader(status)
	_, err := io.Copy(e.Response, r)
	return err
}

func (e *Event) NoContent(status int) error {
	e.Response.WriteHeader(status)
	return nil
}

// Redirect sends the browser to url; status is a 3xx.
func (e *Event) Redirect(status int, url string) error {
	if status < 300 || status > 399 {
		return errors.New("web: redirect status must be 3xx")
	}
	e.Response.Header().Set("Location", url)
	e.Response.WriteHeader(status)
	return nil
}

// Error is an error with the status and message the client sees. Err, if any, is only logged.
type Error struct {
	Status  int
	Message string
	Err     error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Message + ": " + e.Err.Error()
	}
	return e.Message
}

func (e *Error) Unwrap() error { return e.Err }

// NewError makes an Error. An empty message is status's text; the message is made a
// sentence.
func NewError(status int, message string, err error) *Error {
	if message == "" {
		message = http.StatusText(status)
	}
	return &Error{Status: status, Message: sentence(message), Err: err}
}

const somethingWrong = "Something went wrong while processing your request."

func (e *Event) Error(status int, message string, err error) *Error {
	return NewError(status, message, err)
}

func (e *Event) BadRequestError(message string, err error) *Error {
	return NewError(http.StatusBadRequest, orDefault(message, somethingWrong), err)
}

func (e *Event) NotFoundError(message string, err error) *Error {
	return NewError(http.StatusNotFound, orDefault(message, "The requested resource wasn't found."), err)
}

func (e *Event) ForbiddenError(message string, err error) *Error {
	return NewError(http.StatusForbidden, orDefault(message, "You are not allowed to perform this request."), err)
}

func (e *Event) InternalServerError(message string, err error) *Error {
	return NewError(http.StatusInternalServerError, orDefault(message, somethingWrong), err)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}

func sentence(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	r, n := utf8.DecodeRuneInString(s)
	s = string(unicode.ToUpper(r)) + s[n:]
	if !strings.HasSuffix(s, ".") && !strings.HasSuffix(s, "?") && !strings.HasSuffix(s, "!") {
		s += "."
	}
	return s
}

// AsError is err as the client sees it: an *Error as it is, a missing row or file as 404,
// anything else as 400.
func AsError(err error) *Error {
	if e, ok := errors.AsType[*Error](err); ok {
		return e
	}
	if errors.Is(err, sql.ErrNoRows) || errors.Is(err, fs.ErrNotExist) {
		return NewError(http.StatusNotFound, "The requested resource wasn't found.", err)
	}
	return NewError(http.StatusBadRequest, somethingWrong, err)
}

// WriteError sends err as the JSON error body, unless a response was already sent.
func (e *Event) WriteError(err error) {
	if e.status != 0 {
		return
	}
	a := AsError(err)
	if e.Response.Header().Get("Content-Type") == "" {
		e.Response.Header().Set("Content-Type", "application/json")
	}
	e.Response.WriteHeader(a.Status)
	if e.Request.Method != http.MethodHead {
		b, _ := json.Marshal(map[string]any{"data": map[string]any{}, "message": a.Message, "status": a.Status})
		e.Response.Write(b)
	}
}

// Static serves the files in fsys, without directory listings. A request with ?v= (an
// AssetURL) is cached for good: its URL changes with the file.
func Static(fsys fs.FS) http.Handler {
	files := http.FileServerFS(fsys)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/")
		if st, err := fs.Stat(fsys, name); err != nil || st.IsDir() {
			http.NotFound(w, r)
			return
		}
		if r.URL.Query().Has("v") {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

// AssetURL is prefix+name with ?v= a hash of the file's content, for Static to cache.
func AssetURL(fsys fs.FS, prefix, name string) (string, error) {
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(b)
	return prefix + name + "?v=" + hex.EncodeToString(sum[:4]), nil
}
