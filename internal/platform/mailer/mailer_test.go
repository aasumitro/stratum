package mailer

import (
	"bufio"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/aasumitro/stratum/internal/platform/config"
)

func TestSend_Unconfigured_ReturnsNil(t *testing.T) {
	m := &Mailer{} // host = "" → IsConfigured = false
	if err := m.Send(Message{To: "to@test.com", Subject: "Hi", HTML: "<p>Hi</p>"}); err != nil {
		t.Errorf("unconfigured Send: want nil, got %v", err)
	}
}

// TestNew_BuildsMailerFromConfig covers the constructor used by the
// bootstrap composition root to wire the SMTP config from env vars into a
// Mailer — currently untested despite being the only place a Mailer is
// ever created outside test files.
func TestNew_BuildsMailerFromConfig(t *testing.T) {
	cfg := config.SMTPConfig{
		Host:     "smtp.example.com",
		Port:     587,
		Username: "notifications@example.com",
		Password: "secret",
		FromName: "Stratum",
	}
	m := New(cfg)

	if !m.IsConfigured() {
		t.Fatal("want IsConfigured true when host is set")
	}
	if m.host != cfg.Host {
		t.Errorf("host: want %q, got %q", cfg.Host, m.host)
	}
	if m.port != cfg.Port {
		t.Errorf("port: want %d, got %d", cfg.Port, m.port)
	}
	if m.username != cfg.Username {
		t.Errorf("username: want %q, got %q", cfg.Username, m.username)
	}
	if m.password != cfg.Password {
		t.Errorf("password: want %q, got %q", cfg.Password, m.password)
	}
	if m.fromName != cfg.FromName {
		t.Errorf("fromName: want %q, got %q", cfg.FromName, m.fromName)
	}
	// fromAddr always mirrors the authenticated username — there is no
	// separate "from address" field in config, so New must derive it.
	if m.fromAddr != cfg.Username {
		t.Errorf("fromAddr: want %q (mirrors username), got %q", cfg.Username, m.fromAddr)
	}
}

func TestNew_EmptyHost_NotConfigured(t *testing.T) {
	m := New(config.SMTPConfig{})
	if m.IsConfigured() {
		t.Error("want IsConfigured false when SMTP host is unset (no-op mailer)")
	}
}

// selfSignedCert builds a throwaway, untrusted (self-signed, not in any
// trust store) certificate for host — enough to stand up a TLS listener
// without touching the filesystem or the OS trust store.
func selfSignedCert(t *testing.T, host string) tls.Certificate {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		IPAddresses:  []net.IP{net.ParseIP(host)},
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
}

// TestSend_ImplicitTLS_UntrustedCertificate_ReturnsWrappedError covers the
// port-465 implicit-TLS branch (sendTLS), otherwise untested: a misconfigured
// or expired certificate on the SMTP relay must fail fast with a wrapped
// error rather than hang or panic.
func TestSend_ImplicitTLS_UntrustedCertificate_ReturnsWrappedError(t *testing.T) {
	cert := selfSignedCert(t, "127.0.0.1")

	ln, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer ln.Close()

	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer conn.Close()
		// The client aborts during the handshake (untrusted cert) before
		// sending any SMTP command — just drain so Accept doesn't hang.
		io.Copy(io.Discard, conn) //nolint:errcheck
	}()

	// sendTLS is called directly (bypassing Send's port==465 dispatch) since
	// binding a test listener to the real privileged port 465 isn't portable.
	m := &Mailer{host: "127.0.0.1", fromAddr: "sender@test.com"}
	auth := smtp.PlainAuth("", "user@test.com", "secret", m.host)

	err = m.sendTLS(ln.Addr().String(), auth, m.fromAddr, "recipient@test.com", []byte("body"))
	if err == nil {
		t.Fatal("want an error dialing a server with an untrusted self-signed certificate")
	}
	if !strings.Contains(err.Error(), "tls dial") {
		t.Errorf("want error wrapped with %q context, got %v", "tls dial", err)
	}
}

func TestSend_SMTP_DeliversMessage(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	port := ln.Addr().(*net.TCPAddr).Port

	recv := make(chan string, 1)
	go func() {
		conn, connErr := ln.Accept()
		if connErr != nil {
			return
		}
		defer conn.Close()

		var sb strings.Builder
		rdr := bufio.NewReader(conn)
		conn.Write([]byte("220 localhost ESMTP\r\n")) //nolint:errcheck
		for {
			line, rdErr := rdr.ReadString('\n')
			if rdErr != nil {
				break
			}
			sb.WriteString(line)
			cmd := strings.ToUpper(strings.TrimSpace(line))
			switch {
			case strings.HasPrefix(cmd, "EHLO"):
				conn.Write([]byte("250-localhost\r\n250 AUTH PLAIN\r\n")) //nolint:errcheck
			case strings.HasPrefix(cmd, "AUTH"):
				conn.Write([]byte("235 ok\r\n")) //nolint:errcheck
			case strings.HasPrefix(cmd, "MAIL"):
				conn.Write([]byte("250 ok\r\n")) //nolint:errcheck
			case strings.HasPrefix(cmd, "RCPT"):
				conn.Write([]byte("250 ok\r\n")) //nolint:errcheck
			case strings.HasPrefix(cmd, "DATA"):
				conn.Write([]byte("354 send data\r\n")) //nolint:errcheck
				for {
					dataLine, _ := rdr.ReadString('\n')
					sb.WriteString(dataLine)
					if strings.TrimRight(dataLine, "\r\n") == "." {
						conn.Write([]byte("250 ok\r\n")) //nolint:errcheck
						break
					}
				}
			case strings.HasPrefix(cmd, "QUIT"):
				conn.Write([]byte("221 bye\r\n")) //nolint:errcheck
				recv <- sb.String()
				return
			}
		}
	}()

	m := &Mailer{
		host:     "127.0.0.1",
		port:     port,
		username: "user@test.com",
		password: "secret",
		fromName: "Test Sender",
		fromAddr: "sender@test.com",
	}
	if err = m.Send(Message{
		To:      "recipient@test.com",
		Subject: "Phase4 Subject",
		HTML:    "<p>Hello from test</p>",
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	select {
	case data := <-recv:
		if !strings.Contains(data, "Subject: Phase4 Subject") {
			t.Errorf("Subject header not found in SMTP data:\n%s", data)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("SMTP server did not receive message within 2s")
	}
}

// TestStripCRLF regression-tests header injection: a Subject/To value
// containing a CRLF could otherwise inject an extra SMTP header (e.g. a
// hidden Bcc) or terminate the header block and forge a second message body.
func TestStripCRLF(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"normal subject", "normal subject"},
		{"evil\r\nBcc: attacker@evil.com", "evilBcc: attacker@evil.com"},
		{"evil\nBcc: attacker@evil.com", "evilBcc: attacker@evil.com"},
		{"trailing\r\n", "trailing"},
		{"", ""},
	} {
		got := stripCRLF(tc.in)
		if got != tc.want {
			t.Errorf("stripCRLF(%q) = %q, want %q", tc.in, got, tc.want)
		}
		if strings.ContainsAny(got, "\r\n") {
			t.Errorf("stripCRLF(%q) = %q still contains a CR or LF", tc.in, got)
		}
	}
}

func TestStripHTML(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"<h1>Hello</h1>", "Hello"},
		{"<p>Hello <b>World</b></p>", "Hello World"},
		{"no tags here", "no tags here"},
		{"<p>  spaces  </p>", "spaces"},
		{"<p>line1</p>\n<p>line2</p>", "line1\nline2"},
		{"", ""},
	} {
		got := stripHTML(tc.in)
		if got != tc.want {
			t.Errorf("stripHTML(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
