// Package mail sends HTML email over SMTP, with settings from the environment.
package mail

import (
	"bytes"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"errors"
	"fmt"
	"log"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"os"
	"strings"
	"time"
)

// Mailer sends email. With Dev set, it writes each message to the log instead.
type Mailer struct {
	Host, Port, User, Pass string
	From                   mail.Address
	TLS                    bool // TLS from the first byte; otherwise STARTTLS is required
	Dev                    bool
	roots                  *x509.CertPool // tests trust their own server; nil means the system's
}

// FromEnv reads SMTP_HOST, SMTP_PORT (default 587), SMTP_USER, SMTP_PASS and MAIL_FROM
// ("Name <address>"). Without a host or sender it still returns a Mailer, whose Send fails
// outside dev, so a server without mail settings runs and only its emails fail.
func FromEnv(dev bool) (*Mailer, error) {
	m := &Mailer{Host: os.Getenv("SMTP_HOST"), Port: os.Getenv("SMTP_PORT"), User: os.Getenv("SMTP_USER"), Pass: os.Getenv("SMTP_PASS"), Dev: dev}
	if m.Port == "" {
		m.Port = "587"
	}
	m.TLS = m.Port == "465"
	if from := os.Getenv("MAIL_FROM"); from != "" {
		a, err := mail.ParseAddress(from)
		if err != nil {
			return nil, fmt.Errorf("MAIL_FROM: %w", err)
		}
		m.From = *a
	}
	return m, nil
}

// Send emails an HTML message to one address.
func (m *Mailer) Send(to, subject, html string) error {
	if strings.ContainsAny(to+subject, "\r\n") {
		return errors.New("mail: line break in address or subject")
	}
	if m.Dev {
		log.Printf("dev email to %s: %s\n%s", to, subject, html)
		return nil
	}
	if m.Host == "" || m.From.Address == "" {
		return errors.New("mail: SMTP_HOST and MAIL_FROM aren't set")
	}
	d := &net.Dialer{Timeout: 30 * time.Second}
	addr := net.JoinHostPort(m.Host, m.Port)
	tlsConf := &tls.Config{ServerName: m.Host, RootCAs: m.roots}
	var conn net.Conn
	var err error
	if m.TLS {
		conn, err = tls.DialWithDialer(d, "tcp", addr, tlsConf)
	} else {
		conn, err = d.Dial("tcp", addr)
	}
	if err != nil {
		return err
	}
	conn.SetDeadline(time.Now().Add(time.Minute)) // a stuck server can't hold the request
	c, err := smtp.NewClient(conn, m.Host)
	if err != nil {
		conn.Close()
		return err
	}
	defer c.Close()
	if !m.TLS {
		if err := c.StartTLS(tlsConf); err != nil { // never send the password in the clear
			return err
		}
	}
	if m.User != "" {
		if err := c.Auth(smtp.PlainAuth("", m.User, m.Pass, m.Host)); err != nil {
			return err
		}
	}
	if err := c.Mail(m.From.Address); err != nil {
		return err
	}
	if err := c.Rcpt(to); err != nil {
		return err
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	msg := m.message(to, subject, html)
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func (m *Mailer) message(to, subject, html string) []byte {
	id := make([]byte, 16)
	rand.Read(id)
	domain := m.From.Address[strings.LastIndexByte(m.From.Address, '@')+1:]
	var b bytes.Buffer
	for _, h := range [][2]string{
		{"From", m.From.String()},
		{"To", to},
		{"Subject", mime.QEncoding.Encode("utf-8", subject)},
		{"Date", time.Now().Format(time.RFC1123Z)},
		{"Message-ID", "<" + hex.EncodeToString(id) + "@" + domain + ">"},
		{"MIME-Version", "1.0"},
		{"Content-Type", `text/html; charset="utf-8"`},
		{"Content-Transfer-Encoding", "8bit"},
	} {
		b.WriteString(h[0] + ": " + h[1] + "\r\n")
	}
	b.WriteString("\r\n")
	b.WriteString(strings.ReplaceAll(strings.ReplaceAll(html, "\r\n", "\n"), "\n", "\r\n"))
	return b.Bytes()
}
