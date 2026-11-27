package notification

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/aasumitro/stratum/internal/contracts"
	"github.com/aasumitro/stratum/internal/platform/config"
	"github.com/aasumitro/stratum/internal/platform/mailer"
)

func TestDigestCutoff(t *testing.T) {
	jakarta, _ := time.LoadLocation("Asia/Jakarta")
	cases := []struct {
		name string
		now  time.Time
		tz   string
		want time.Time
	}{
		{"after 08:00 local", time.Date(2026, 11, 24, 2, 30, 0, 0, time.UTC), "Asia/Jakarta", time.Date(2026, 11, 24, 8, 0, 0, 0, jakarta)},
		{"before 08:00 local", time.Date(2026, 11, 23, 23, 0, 0, 0, time.UTC), "Asia/Jakarta", time.Date(2026, 11, 23, 8, 0, 0, 0, jakarta)},
		{"no zone is UTC", time.Date(2026, 11, 24, 9, 0, 0, 0, time.UTC), "", time.Date(2026, 11, 24, 8, 0, 0, 0, time.UTC)},
		{"unknown zone is UTC", time.Date(2026, 11, 24, 7, 0, 0, 0, time.UTC), "Mars/Base", time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)},
	}
	for _, tc := range cases {
		if got := digestCutoff(tc.now, tc.tz); !got.Equal(tc.want) {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.want)
		}
	}
}

// fakeSMTP accepts SMTP sessions on a local port and reports each message's
// recipient and data.
func fakeSMTP(t *testing.T) (port int, received <-chan string) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	out := make(chan string, 10)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			go func(conn net.Conn) {
				defer conn.Close()
				var sb strings.Builder
				rdr := bufio.NewReader(conn)
				conn.Write([]byte("220 localhost ESMTP\r\n")) //nolint:errcheck
				for {
					line, err := rdr.ReadString('\n')
					if err != nil {
						return
					}
					sb.WriteString(line)
					cmd := strings.ToUpper(strings.TrimSpace(line))
					switch {
					case strings.HasPrefix(cmd, "EHLO"):
						conn.Write([]byte("250-localhost\r\n250 AUTH PLAIN\r\n")) //nolint:errcheck
					case strings.HasPrefix(cmd, "AUTH"):
						conn.Write([]byte("235 ok\r\n")) //nolint:errcheck
					case strings.HasPrefix(cmd, "MAIL"), strings.HasPrefix(cmd, "RCPT"):
						conn.Write([]byte("250 ok\r\n")) //nolint:errcheck
					case strings.HasPrefix(cmd, "DATA"):
						conn.Write([]byte("354 send data\r\n")) //nolint:errcheck
						for {
							l, _ := rdr.ReadString('\n')
							sb.WriteString(l)
							if strings.TrimRight(l, "\r\n") == "." {
								conn.Write([]byte("250 ok\r\n")) //nolint:errcheck
								break
							}
						}
					case strings.HasPrefix(cmd, "QUIT"):
						conn.Write([]byte("221 bye\r\n")) //nolint:errcheck
						out <- sb.String()
						return
					}
				}
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, out
}

type digestUserReader struct{}

func (digestUserReader) GetUserByAuthSub(context.Context, string) (*contracts.UserInfo, error) {
	return nil, errors.New("not implemented")
}
func (digestUserReader) GetUserByEmail(context.Context, string) (*contracts.UserInfo, error) {
	return nil, errors.New("not implemented")
}
func (digestUserReader) GetUsersByAuthSubs(_ context.Context, subs []string) (map[string]contracts.UserInfo, error) {
	out := map[string]contracts.UserInfo{}
	for _, s := range subs {
		out[s] = contracts.UserInfo{AuthSub: s, Email: s + "@example.com", Lang: "en", Timezone: "Asia/Jakarta"}
	}
	return out, nil
}
func (digestUserReader) IsMFAEnabled(context.Context, string) (bool, error) { return false, nil }

func recv(t *testing.T, ch <-chan string) string {
	t.Helper()
	select {
	case m := <-ch:
		return m
	case <-time.After(5 * time.Second):
		t.Fatal("no email within 5s")
		return ""
	}
}

func digestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("TEST_DATABASE_URL not set")
	}
	pool, err := pgxpool.New(t.Context(), dsn)
	if err != nil {
		t.Fatalf("open pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func heldCount(t *testing.T, pool *pgxpool.Pool, orgID, status string) int {
	t.Helper()
	var n int
	if err := pool.QueryRow(t.Context(), `SELECT count(*) FROM notification.messages WHERE organization_id = $1 AND kind = 'email' AND status = $2`,
		orgID, status).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", status, err)
	}
	return n
}

// With the digest on, an eligible email is held and an urgent one is sent.
// The digest goes out only once the user's local 08:00 has passed after the
// email was held, exactly once, and with the subject as typed.
func TestIntegration_Digest_HoldsThenSendsOnce(t *testing.T) {
	pool := digestPool(t)
	const authSub = "integ_digest_user"
	const orgID = "0e1e0000-0000-0000-0000-0000000000d4"
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM notification.messages WHERE organization_id = $1`, orgID)
		pool.Exec(ctx, `DELETE FROM notification.preferences WHERE auth_sub = $1`, authSub)
	})
	port, received := fakeSMTP(t)
	m := mailer.New(config.SMTPConfig{Host: "127.0.0.1", Port: port, Username: "noreply@test.local", FromName: "Test"})
	svc := New(pool, m, nil, digestUserReader{}, "http://app.test", nil).svc
	ctx := t.Context()

	// Off by default: sent at once, and the row names its recipient.
	svc.sendEmail(ctx, orgID, authSub+"@example.com", authSub, "invoice_paid", "en", mailer.TemplateData{Amount: "$10"})
	recv(t, received)
	var recipient *string
	if err := pool.QueryRow(ctx, `SELECT auth_sub FROM notification.messages WHERE organization_id = $1 AND status = 'sent'`, orgID).Scan(&recipient); err != nil {
		t.Fatalf("read sent email row: %v", err)
	}
	if recipient == nil || *recipient != authSub {
		t.Errorf("sent email row: want auth_sub %q, got %v", authSub, recipient)
	}
	if _, err := svc.repo.upsertPreference(ctx, pool, authSub, "email", digestEventType, true); err != nil {
		t.Fatalf("turn digest on: %v", err)
	}
	svc.sendEmail(ctx, orgID, authSub+"@example.com", authSub, "usage_limit_warning", "en", mailer.TemplateData{OrganizationName: "R&D", UsageDetail: "92 / 100 members"})
	svc.sendEmail(ctx, orgID, authSub+"@example.com", authSub, "invoice_failed", "en", mailer.TemplateData{Amount: "$30"})
	recv(t, received) // invoice_failed is never held
	if n := heldCount(t, pool, orgID, "held"); n != 1 {
		t.Fatalf("want 1 held email, got %d", n)
	}

	// Held at 07:30 in Jakarta (00:30 UTC): not due at 07:45, due at 08:05.
	if _, err := pool.Exec(ctx, `UPDATE notification.messages SET created_at = '2026-11-24 00:30:00+00' WHERE organization_id = $1 AND status = 'held'`, orgID); err != nil {
		t.Fatalf("backdate held email: %v", err)
	}
	svc.sendDueDigests(ctx, time.Date(2026, 11, 24, 0, 45, 0, 0, time.UTC))
	if n := heldCount(t, pool, orgID, "held"); n != 1 {
		t.Fatalf("before 08:00 local: want it still held, got %d held", n)
	}

	at := time.Date(2026, 11, 24, 1, 5, 0, 0, time.UTC)
	svc.sendDueDigests(ctx, at)
	data := recv(t, received)
	if !strings.Contains(data, "Subject: Your daily summary") {
		t.Errorf("digest email: want the digest subject, got:\n%s", data)
	}
	if strings.Contains(data, "R&amp;amp;D") {
		t.Errorf("subject escaped twice in the digest:\n%s", data)
	}
	if !strings.Contains(data, "92 / 100 members of the plan") {
		t.Errorf("digest email: want the item's summary line, got:\n%s", data)
	}
	if n := heldCount(t, pool, orgID, "held"); n != 0 {
		t.Errorf("after the digest: want nothing held, got %d", n)
	}

	svc.sendDueDigests(ctx, at.Add(time.Hour))
	select {
	case extra := <-received:
		t.Errorf("a second tick sent again:\n%s", extra)
	case <-time.After(500 * time.Millisecond):
	}
}

// A digest that cannot be sent stays held for the next tick, and is given up
// on once it has been due for a day.
func TestIntegration_Digest_FailedSendRetriesThenGivesUp(t *testing.T) {
	pool := digestPool(t)
	const authSub = "integ_digest_fail_user"
	const orgID = "0e1e0000-0000-0000-0000-0000000000d5"
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM notification.messages WHERE organization_id = $1`, orgID)
		pool.Exec(ctx, `DELETE FROM notification.preferences WHERE auth_sub = $1`, authSub)
	})
	dead := mailer.New(config.SMTPConfig{Host: "127.0.0.1", Port: 1, Username: "noreply@test.local", FromName: "Test"})
	svc := New(pool, dead, nil, digestUserReader{}, "http://app.test", nil).svc
	ctx := t.Context()
	if _, err := svc.repo.upsertPreference(ctx, pool, authSub, "email", digestEventType, true); err != nil {
		t.Fatalf("turn digest on: %v", err)
	}
	svc.sendEmail(ctx, orgID, authSub+"@example.com", authSub, "usage_limit_warning", "en", mailer.TemplateData{})
	if _, err := pool.Exec(ctx, `UPDATE notification.messages SET created_at = '2026-11-24 00:30:00+00' WHERE organization_id = $1 AND status = 'held'`, orgID); err != nil {
		t.Fatalf("backdate held email: %v", err)
	}

	svc.sendDueDigests(ctx, time.Date(2026, 11, 24, 1, 5, 0, 0, time.UTC))
	if n := heldCount(t, pool, orgID, "held"); n != 1 {
		t.Fatalf("after a failed send: want it still held, got %d", n)
	}

	// A second email held at 09:00 Jakarta, after that day's cutoff: due the
	// next morning for the first time, so it is not given up with the first.
	svc.sendEmail(ctx, orgID, authSub+"@example.com", authSub, "invoice_paid", "en", mailer.TemplateData{})
	if _, err := pool.Exec(ctx, `UPDATE notification.messages SET created_at = '2026-11-24 02:00:00+00'
		WHERE organization_id = $1 AND status = 'held' AND channel = 'invoice_paid'`, orgID); err != nil {
		t.Fatalf("backdate second email: %v", err)
	}
	svc.sendDueDigests(ctx, time.Date(2026, 11, 25, 1, 5, 0, 0, time.UTC))
	if n := heldCount(t, pool, orgID, "failed"); n != 1 {
		t.Errorf("due for a day and still failing: want only the first failed, got %d failed", n)
	}
	if n := heldCount(t, pool, orgID, "held"); n != 1 {
		t.Errorf("first due this morning: want the second still held, got %d held", n)
	}
}

// failingSMTP accepts a connection, reports it on accepted, and drops it after
// a moment, so a send fails a little after it starts.
func failingSMTP(t *testing.T) (port int, accepted <-chan struct{}) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	ch := make(chan struct{}, 1)
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			select {
			case ch <- struct{}{}:
			default:
			}
			go func(c net.Conn) {
				time.Sleep(300 * time.Millisecond)
				c.Close()
			}(conn)
		}
	}()
	return ln.Addr().(*net.TCPAddr).Port, ch
}

// A send that fails while the worker shuts down gives up nothing: even an item
// due for more than a day stays held for the next start.
func TestIntegration_Digest_ShutdownGivesUpNothing(t *testing.T) {
	pool := digestPool(t)
	const authSub = "integ_digest_shutdown_user"
	const orgID = "0e1e0000-0000-0000-0000-0000000000d6"
	t.Cleanup(func() {
		ctx := context.Background()
		pool.Exec(ctx, `DELETE FROM notification.messages WHERE organization_id = $1`, orgID)
		pool.Exec(ctx, `DELETE FROM notification.preferences WHERE auth_sub = $1`, authSub)
	})
	port, accepted := failingSMTP(t)
	m := mailer.New(config.SMTPConfig{Host: "127.0.0.1", Port: port, Username: "noreply@test.local", FromName: "Test"})
	svc := New(pool, m, nil, digestUserReader{}, "http://app.test", nil).svc
	if _, err := svc.repo.upsertPreference(t.Context(), pool, authSub, "email", digestEventType, true); err != nil {
		t.Fatalf("turn digest on: %v", err)
	}
	svc.sendEmail(t.Context(), orgID, authSub+"@example.com", authSub, "invoice_paid", "en", mailer.TemplateData{})
	if _, err := pool.Exec(t.Context(), `UPDATE notification.messages SET created_at = '2026-11-20 00:30:00+00' WHERE organization_id = $1 AND status = 'held'`, orgID); err != nil {
		t.Fatalf("backdate held email: %v", err)
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		svc.sendDueDigests(ctx, time.Date(2026, 11, 24, 1, 5, 0, 0, time.UTC))
		close(done)
	}()
	// Cancel only once the send is in flight (it fails ~300ms later), so the
	// loop cannot stop at its top check before reaching the send.
	select {
	case <-accepted:
	case <-time.After(5 * time.Second):
		t.Fatal("the digest send never reached the relay")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("sendDueDigests did not return after cancel")
	}
	if n := heldCount(t, pool, orgID, "held"); n != 1 {
		t.Errorf("after a shutdown: want the item still held, got %d held", n)
	}
}
