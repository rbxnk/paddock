package mail

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"math/big"
	"net"
	"net/mail"
	"net/textproto"
	"strings"
	"testing"
	"time"
)

// TestSend sends through a fake SMTP server that requires STARTTLS, and checks what it got.
func TestSend(t *testing.T) {
	cert, roots := selfSigned(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []string, 1)
	go func() {
		conn, err := ln.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		got <- fakeSMTP(conn, cert)
	}()

	host, port, _ := net.SplitHostPort(ln.Addr().String())
	m := &Mailer{Host: host, Port: port, User: "u", Pass: "p", From: mail.Address{Name: "Paddock Place", Address: "noreply@example.com"}, roots: roots}
	if err := m.Send("a@example.com", "Set your password ✓", "<p>hi</p>\n<p>there</p>"); err != nil {
		t.Fatal(err)
	}
	cmds := <-got
	want := []string{"EHLO", "STARTTLS", "EHLO", "AUTH PLAIN AHUAcA==", "MAIL FROM:<noreply@example.com>", "RCPT TO:<a@example.com>", "DATA"}
	for i, w := range want {
		if i >= len(cmds) || !strings.HasPrefix(cmds[i], w) {
			t.Fatalf("commands %q, want %q", cmds, want)
		}
	}
	data := cmds[len(want)]
	for _, w := range []string{"From: \"Paddock Place\" <noreply@example.com>\r\n", "To: a@example.com\r\n", "Subject: =?utf-8?q?Set_your_password_=E2=9C=93?=\r\n", "Content-Type: text/html", "\r\n\r\n<p>hi</p>\r\n<p>there</p>"} {
		if !strings.Contains(data, w) {
			t.Errorf("message lacks %q:\n%s", w, data)
		}
	}
}

func TestSendRejectsLineBreaks(t *testing.T) {
	m := &Mailer{Dev: true}
	if err := m.Send("a@example.com\r\nBcc: b@example.com", "s", ""); err == nil {
		t.Error("a line break in the address is sent")
	}
}

// fakeSMTP answers one client and returns its commands, with the message as the last one.
func fakeSMTP(conn net.Conn, cert tls.Certificate) []string {
	var cmds []string
	tp := textproto.NewConn(conn)
	tp.PrintfLine("220 fake")
	for {
		line, err := tp.ReadLine()
		if err != nil {
			return cmds
		}
		cmds = append(cmds, line)
		switch verb, _, _ := strings.Cut(line, " "); verb {
		case "EHLO":
			tp.PrintfLine("250-fake\r\n250-STARTTLS\r\n250 AUTH PLAIN")
		case "STARTTLS":
			tp.PrintfLine("220 go ahead")
			tc := tls.Server(conn, &tls.Config{Certificates: []tls.Certificate{cert}})
			tp = textproto.NewConn(tc)
		case "AUTH":
			tp.PrintfLine("235 ok")
		case "DATA":
			tp.PrintfLine("354 go")
			var b strings.Builder
			r := bufio.NewReader(tp.DotReader())
			r.WriteTo(&b)
			cmds = append(cmds, strings.ReplaceAll(b.String(), "\n", "\r\n"))
			tp.PrintfLine("250 ok")
		case "QUIT":
			tp.PrintfLine("221 bye")
			return cmds
		default:
			tp.PrintfLine("250 ok")
		}
	}
}

func selfSigned(t *testing.T) (tls.Certificate, *x509.CertPool) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), IPAddresses: []net.IP{net.ParseIP("127.0.0.1")}}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	c, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(c)
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, roots
}
