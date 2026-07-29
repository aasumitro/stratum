package mailer

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/smtp"
	"regexp"
	"strings"

	"github.com/aasumitro/stratum/internal/platform/config"
)

// Mailer sends email via SMTP. Zero-value is safe (IsConfigured returns false).
type Mailer struct {
	host     string
	port     int
	username string
	password string
	fromName string
	fromAddr string
}

// Message represents an outgoing email.
type Message struct {
	To      string
	Subject string
	HTML    string
	Text    string
}

// New creates a Mailer from config. If host is empty, the mailer is a no-op.
func New(cfg config.SMTPConfig) *Mailer {
	return &Mailer{
		host:     cfg.Host,
		port:     cfg.Port,
		username: cfg.Username,
		password: cfg.Password,
		fromName: cfg.FromName,
		fromAddr: cfg.Username,
	}
}

// IsConfigured returns true if SMTP host is set.
func (m *Mailer) IsConfigured() bool {
	return m.host != ""
}

// Send delivers an email via SMTP.
func (m *Mailer) Send(msg Message) error {
	if !m.IsConfigured() {
		return nil
	}

	if msg.Text == "" && msg.HTML != "" {
		msg.Text = stripHTML(msg.HTML)
	}

	from := fmt.Sprintf("%s <%s>", m.fromName, m.fromAddr)
	headers := map[string]string{
		"From": from,
		// To/Subject can carry attacker-controlled data (Subject is often
		// built from user input, e.g. an invitee's display name); a raw
		// CR/LF would let it inject extra headers or a second message body
		// into the SMTP payload. Currently only reachable via a validated
		// email address for To, but Subject has no such guarantee — strip
		// unconditionally rather than relying on a caller's validation.
		"To":           stripCRLF(msg.To),
		"Subject":      stripCRLF(msg.Subject),
		"MIME-Version": "1.0",
		"Content-Type": `multipart/alternative; boundary="boundary"`,
	}

	var body strings.Builder
	for k, v := range headers {
		fmt.Fprintf(&body, "%s: %s\r\n", k, v)
	}
	body.WriteString("\r\n")

	body.WriteString("--boundary\r\n")
	body.WriteString("Content-Type: text/plain; charset=\"utf-8\"\r\n\r\n")
	body.WriteString(msg.Text)
	body.WriteString("\r\n")

	if msg.HTML != "" {
		body.WriteString("--boundary\r\n")
		body.WriteString("Content-Type: text/html; charset=\"utf-8\"\r\n\r\n")
		body.WriteString(msg.HTML)
		body.WriteString("\r\n")
	}
	body.WriteString("--boundary--\r\n")

	addr := fmt.Sprintf("%s:%d", m.host, m.port)
	auth := smtp.PlainAuth("", m.username, m.password, m.host)

	if m.port == 465 {
		return m.sendTLS(addr, auth, m.fromAddr, msg.To, []byte(body.String()))
	}
	if err := smtp.SendMail(addr, auth, m.fromAddr, []string{msg.To}, []byte(body.String())); err != nil {
		return fmt.Errorf("mailer.Send: %w", err)
	}
	return nil
}

// sendTLS handles implicit TLS (port 465).
func (m *Mailer) sendTLS(addr string, auth smtp.Auth, from, to string, body []byte) error {
	dialer := &tls.Dialer{Config: &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(context.Background(), "tcp", addr)
	if err != nil {
		return fmt.Errorf("mailer.sendTLS: tls dial: %w", err)
	}
	defer conn.Close()

	host, _, _ := net.SplitHostPort(addr)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("mailer.sendTLS: smtp client: %w", err)
	}
	defer client.Close()

	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("mailer.sendTLS: smtp auth: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mailer.sendTLS: mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("mailer.sendTLS: rcpt to: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer.sendTLS: data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("mailer.sendTLS: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer.sendTLS: close writer: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("mailer.sendTLS: quit: %w", err)
	}
	return nil
}

var crlfReplacer = strings.NewReplacer("\r", "", "\n", "")

// stripCRLF removes carriage returns and line feeds so a value written into
// a raw SMTP header can't inject additional headers or terminate the header
// block early.
func stripCRLF(s string) string {
	return crlfReplacer.Replace(s)
}

var htmlTagRe = regexp.MustCompile(`<[^>]*>`)

func stripHTML(s string) string {
	text := htmlTagRe.ReplaceAllString(s, "")
	var out []string
	for line := range strings.SplitSeq(text, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return strings.Join(out, "\n")
}
