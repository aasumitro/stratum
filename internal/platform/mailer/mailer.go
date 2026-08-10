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

// Send delivers an email via SMTP, bounded by ctx — both connecting and the
// SMTP conversation that follows fail once ctx is done, instead of blocking
// on the OS's default TCP timeout (minutes) if the server never responds.
func (m *Mailer) Send(ctx context.Context, msg Message) error {
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

	var conn net.Conn
	var err error
	if m.port == 465 {
		conn, err = m.dialTLS(ctx, addr)
	} else {
		conn, err = (&net.Dialer{}).DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("mailer.Send: dial: %w", err)
	}
	defer conn.Close()

	// DialContext only bounds connecting; the SMTP conversation that follows
	// talks over plain conn.Read/Write, which net/smtp doesn't accept a
	// context for — a deadline on the conn itself is what actually bounds it.
	if deadline, ok := ctx.Deadline(); ok {
		if err := conn.SetDeadline(deadline); err != nil {
			return fmt.Errorf("mailer.Send: set deadline: %w", err)
		}
	}

	return deliver(conn, addr, auth, m.fromAddr, msg.To, []byte(body.String()))
}

// dialTLS handles implicit TLS (port 465).
func (m *Mailer) dialTLS(ctx context.Context, addr string) (net.Conn, error) {
	dialer := &tls.Dialer{Config: &tls.Config{ServerName: m.host, MinVersion: tls.VersionTLS12}}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("mailer.dialTLS: tls dial: %w", err)
	}
	return conn, nil
}

// deliver runs the SMTP conversation over an already-dialed conn, upgrading
// to STARTTLS when the server advertises it — mirrors net/smtp.SendMail's own
// internal sequence, replacing it so the plain (STARTTLS) and implicit-TLS
// (port 465) paths share one conversation instead of net/smtp doing its own,
// separately unbounded, dial for the former.
func deliver(conn net.Conn, addr string, auth smtp.Auth, from, to string, body []byte) error {
	host, _, _ := net.SplitHostPort(addr)
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return fmt.Errorf("mailer.deliver: smtp client: %w", err)
	}
	defer client.Close()

	if ok, _ := client.Extension("STARTTLS"); ok {
		if err := client.StartTLS(&tls.Config{ServerName: host, MinVersion: tls.VersionTLS12}); err != nil {
			return fmt.Errorf("mailer.deliver: starttls: %w", err)
		}
	}
	if err := client.Auth(auth); err != nil {
		return fmt.Errorf("mailer.deliver: smtp auth: %w", err)
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("mailer.deliver: mail from: %w", err)
	}
	if err := client.Rcpt(to); err != nil {
		return fmt.Errorf("mailer.deliver: rcpt to: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("mailer.deliver: data: %w", err)
	}
	if _, err := w.Write(body); err != nil {
		return fmt.Errorf("mailer.deliver: write body: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("mailer.deliver: close writer: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("mailer.deliver: quit: %w", err)
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
