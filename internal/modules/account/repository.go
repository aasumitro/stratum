package account

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/aasumitro/stratum/internal/platform/audit"
	"github.com/aasumitro/stratum/internal/platform/db"
)

type userRecord struct {
	ID          string          `json:"id"`
	AuthSub     string          `json:"auth_sub"`
	Email       string          `json:"email"`
	FullName    string          `json:"full_name"`
	AvatarURL   string          `json:"avatar_url"`
	Preferences json.RawMessage `json:"preferences"`
	CreatedAt   time.Time       `json:"created_at"`
	UpdatedAt   time.Time       `json:"updated_at"`
}

type repository struct{}

func (r *repository) upsertUser(
	ctx context.Context, q db.Querier,
	authSub, email, fullName, avatarURL string,
) (*userRecord, error) {
	u := new(userRecord)
	err := q.QueryRow(ctx, `
		INSERT INTO account.users (auth_sub, email, full_name, avatar_url)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (auth_sub) DO UPDATE
			SET email      = EXCLUDED.email,
			    full_name  = EXCLUDED.full_name,
			    avatar_url = EXCLUDED.avatar_url,
			    updated_at = now()
		RETURNING id, auth_sub, email, full_name, avatar_url, preferences, created_at, updated_at`,
		authSub, email, fullName, avatarURL,
	).Scan(&u.ID, &u.AuthSub, &u.Email, &u.FullName, &u.AvatarURL, &u.Preferences, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("account.upsertUser: %w", err)
	}
	return u, nil
}

func (r *repository) findUserByAuthSub(ctx context.Context, q db.Querier, authSub string) (*userRecord, error) {
	u := new(userRecord)
	err := q.QueryRow(ctx, `
		SELECT id, auth_sub, email, full_name, avatar_url, preferences, created_at, updated_at
		FROM account.users WHERE auth_sub = $1`,
		authSub,
	).Scan(&u.ID, &u.AuthSub, &u.Email, &u.FullName, &u.AvatarURL, &u.Preferences, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("account.findUserByAuthSub: %w", err)
	}
	return u, nil
}

// findUserByEmail backs GetUserByEmail — account.users.email is UNIQUE NOT
// NULL, already indexed by that constraint.
func (r *repository) findUserByEmail(ctx context.Context, q db.Querier, email string) (*userRecord, error) {
	u := new(userRecord)
	err := q.QueryRow(ctx, `
		SELECT id, auth_sub, email, full_name, avatar_url, preferences, created_at, updated_at
		FROM account.users WHERE email = $1`,
		email,
	).Scan(&u.ID, &u.AuthSub, &u.Email, &u.FullName, &u.AvatarURL, &u.Preferences, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("account.findUserByEmail: %w", err)
	}
	return u, nil
}

// findUsersByAuthSubs resolves multiple users in one query — used by
// GetUsersByAuthSubs to enrich a list of rows (e.g. organization members)
// with profile data without an N+1 lookup per row.
func (r *repository) findUsersByAuthSubs(ctx context.Context, q db.Querier, authSubs []string) ([]userRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, auth_sub, email, full_name, avatar_url, preferences, created_at, updated_at
		FROM account.users WHERE auth_sub = ANY($1)`,
		authSubs,
	)
	if err != nil {
		return nil, fmt.Errorf("account.findUsersByAuthSubs: %w", err)
	}
	defer rows.Close()

	var out []userRecord
	for rows.Next() {
		var u userRecord
		if err := rows.Scan(&u.ID, &u.AuthSub, &u.Email, &u.FullName, &u.AvatarURL, &u.Preferences, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("account.findUsersByAuthSubs: scan: %w", err)
		}
		out = append(out, u)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("account.findUsersByAuthSubs: %w", err)
	}
	return out, nil
}

func (r *repository) updateUser(
	ctx context.Context, q db.Querier,
	authSub, fullName, avatarURL string,
) (*userRecord, error) {
	u := new(userRecord)
	err := q.QueryRow(ctx, `
		UPDATE account.users
		SET full_name  = COALESCE(NULLIF($2, ''), full_name),
		    avatar_url = COALESCE(NULLIF($3, ''), avatar_url),
		    updated_at = now()
		WHERE auth_sub = $1
		RETURNING id, auth_sub, email, full_name, avatar_url, preferences, created_at, updated_at`,
		authSub, fullName, avatarURL,
	).Scan(&u.ID, &u.AuthSub, &u.Email, &u.FullName, &u.AvatarURL, &u.Preferences, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("account.updateUser: %w", err)
	}
	return u, nil
}

// updateEmail syncs the cached email from a verified Supabase auth.users
// webhook, returning the previous email alongside the updated record so
// callers can record a before/after audit trail without a second query.
// Returns pgx.ErrNoRows if the auth_sub hasn't onboarded yet (no account.users
// row) — callers should treat that as a no-op, not a failure.
func (r *repository) updateEmail(
	ctx context.Context, q db.Querier, authSub, email string,
) (u *userRecord, oldEmail string, err error) {
	u = new(userRecord)
	err = q.QueryRow(ctx, `
		WITH old AS (SELECT email FROM account.users WHERE auth_sub = $1)
		UPDATE account.users SET email = $2, updated_at = now() WHERE auth_sub = $1
		RETURNING id, auth_sub, email, full_name, avatar_url, preferences, created_at, updated_at,
			(SELECT email FROM old)`,
		authSub, email,
	).Scan(&u.ID, &u.AuthSub, &u.Email, &u.FullName, &u.AvatarURL, &u.Preferences, &u.CreatedAt, &u.UpdatedAt, &oldEmail)
	if err != nil {
		// %w preserves errors.Is(err, pgx.ErrNoRows) for callers — see the
		// doc comment above, still accurate through the wrap.
		return nil, "", fmt.Errorf("account.updateEmail: %w", err)
	}
	return u, oldEmail, nil
}

// insertEmailChangeAuditEvent records the email change via audit.InsertDirect.
// This bypasses the HTTP audit middleware entirely (the webhook route lives
// outside /api/v1, same as the Stripe/Xendit webhooks), so it's a manual
// write here.
func (r *repository) insertEmailChangeAuditEvent(
	ctx context.Context, q db.Querier,
	authSub, oldEmail, newEmail string,
) error {
	metadata, _ := json.Marshal(map[string]string{"before": oldEmail, "after": newEmail})
	if err := audit.InsertDirect(
		ctx, q, authSub, "EMAIL_CHANGE",
		"/webhooks/supabase/user-updated",
		200, metadata,
	); err != nil {
		return fmt.Errorf("account.insertEmailChangeAuditEvent: %w", err)
	}
	return nil
}

func (r *repository) updatePreferences(
	ctx context.Context, q db.Querier,
	authSub string, prefs json.RawMessage,
) error {
	_, err := q.Exec(ctx, `
		UPDATE account.users SET preferences = preferences || $2::jsonb, updated_at = now() WHERE auth_sub = $1`,
		authSub, prefs,
	)
	if err != nil {
		return fmt.Errorf("account.updatePreferences: %w", err)
	}
	return nil
}

func (r *repository) updateAvatarURL(ctx context.Context, q db.Querier, authSub, avatarURL string) (*userRecord, error) {
	u := new(userRecord)
	err := q.QueryRow(ctx, `
		UPDATE account.users SET avatar_url = $2, updated_at = now() WHERE auth_sub = $1
		RETURNING id, auth_sub, email, full_name, avatar_url, preferences, created_at, updated_at`,
		authSub, avatarURL,
	).Scan(&u.ID, &u.AuthSub, &u.Email, &u.FullName, &u.AvatarURL, &u.Preferences, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("account.updateAvatarURL: %w", err)
	}
	return u, nil
}

func (r *repository) deleteUser(ctx context.Context, q db.Querier, authSub string) error {
	if _, err := q.Exec(ctx, `DELETE FROM account.users WHERE auth_sub = $1`, authSub); err != nil {
		return fmt.Errorf("account.deleteUser: %w", err)
	}
	return nil
}

func (r *repository) deleteLoginEvents(ctx context.Context, q db.Querier, authSub string) error {
	if _, err := q.Exec(ctx, `DELETE FROM account.login_events WHERE auth_sub = $1`, authSub); err != nil {
		return fmt.Errorf("account.deleteLoginEvents: %w", err)
	}
	return nil
}

func (r *repository) isMFAEnabled(ctx context.Context, q db.Querier, authSub string) (bool, error) {
	var enabled bool
	if err := q.QueryRow(ctx, `SELECT mfa_enabled FROM account.users WHERE auth_sub = $1`, authSub).Scan(&enabled); err != nil {
		return false, fmt.Errorf("account.isMFAEnabled: %w", err)
	}
	return enabled, nil
}

func (r *repository) setMFAEnabled(ctx context.Context, q db.Querier, authSub string, enabled bool) error {
	_, err := q.Exec(ctx,
		`UPDATE account.users SET mfa_enabled = $2, updated_at = now() WHERE auth_sub = $1`,
		authSub, enabled,
	)
	if err != nil {
		return fmt.Errorf("account.setMFAEnabled: %w", err)
	}
	return nil
}

type taskRecord struct {
	ID          string          `json:"id"`
	AuthSub     string          `json:"auth_sub"`
	Kind        string          `json:"kind"`
	Status      string          `json:"status"`
	Result      json.RawMessage `json:"result,omitempty"`
	Error       string          `json:"error,omitempty"`
	CreatedAt   time.Time       `json:"created_at"`
	CompletedAt *time.Time      `json:"completed_at,omitempty"`
}

func (r *repository) insertTask(ctx context.Context, q db.Querier, authSub, kind string) (*taskRecord, error) {
	t := new(taskRecord)
	err := q.QueryRow(ctx, `
		INSERT INTO account.tasks (auth_sub, kind) VALUES ($1, $2)
		RETURNING id, auth_sub, kind, status, result, error, created_at, completed_at`,
		authSub, kind,
	).Scan(&t.ID, &t.AuthSub, &t.Kind, &t.Status, &t.Result, &t.Error, &t.CreatedAt, &t.CompletedAt)
	if err != nil {
		return nil, fmt.Errorf("account.insertTask: %w", err)
	}
	return t, nil
}

func (r *repository) completeTask(ctx context.Context, q db.Querier, id string, result json.RawMessage) error {
	_, err := q.Exec(ctx, `
		UPDATE account.tasks SET status = 'completed', result = $2, completed_at = now() WHERE id = $1`,
		id, result,
	)
	if err != nil {
		return fmt.Errorf("account.completeTask: %w", err)
	}
	return nil
}

func (r *repository) failTask(ctx context.Context, q db.Querier, id, errMsg string) error {
	_, err := q.Exec(ctx, `
		UPDATE account.tasks SET status = 'failed', error = $2, completed_at = now() WHERE id = $1`,
		id, errMsg,
	)
	if err != nil {
		return fmt.Errorf("account.failTask: %w", err)
	}
	return nil
}

func (r *repository) markTaskProcessing(ctx context.Context, q db.Querier, id string) error {
	if _, err := q.Exec(ctx, `UPDATE account.tasks SET status = 'processing' WHERE id = $1`, id); err != nil {
		return fmt.Errorf("account.markTaskProcessing: %w", err)
	}
	return nil
}

func (r *repository) listTasks(ctx context.Context, q db.Querier, authSub string) ([]taskRecord, error) {
	rows, err := q.Query(ctx, `
		SELECT id, auth_sub, kind, status, result, error, created_at, completed_at
		FROM account.tasks WHERE auth_sub = $1 ORDER BY created_at DESC LIMIT 20`,
		authSub,
	)
	if err != nil {
		return nil, fmt.Errorf("account.listTasks: %w", err)
	}
	defer rows.Close()

	var out []taskRecord
	for rows.Next() {
		var t taskRecord
		if err := rows.Scan(&t.ID, &t.AuthSub, &t.Kind, &t.Status, &t.Result, &t.Error, &t.CreatedAt, &t.CompletedAt); err != nil {
			return nil, fmt.Errorf("account.listTasks: scan: %w", err)
		}
		out = append(out, t)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("account.listTasks: %w", err)
	}
	return out, nil
}

func (r *repository) findTask(ctx context.Context, q db.Querier, id, authSub string) (*taskRecord, error) {
	t := new(taskRecord)
	err := q.QueryRow(ctx, `
		SELECT id, auth_sub, kind, status, result, error, created_at, completed_at
		FROM account.tasks WHERE id = $1 AND auth_sub = $2`,
		id, authSub,
	).Scan(&t.ID, &t.AuthSub, &t.Kind, &t.Status, &t.Result, &t.Error, &t.CreatedAt, &t.CompletedAt)
	if err != nil {
		return nil, fmt.Errorf("account.findTask: %w", err)
	}
	return t, nil
}

type loginEventRecord struct {
	ID        string    `json:"id"`
	AuthSub   string    `json:"auth_sub"`
	IPAddress string    `json:"ip_address"`
	UserAgent string    `json:"user_agent"`
	CreatedAt time.Time `json:"created_at"`
}

func (r *repository) insertLoginEvent(ctx context.Context, q db.Querier, authSub, ip, ua string) error {
	_, err := q.Exec(ctx, `
		INSERT INTO account.login_events (auth_sub, ip_address, user_agent) VALUES ($1, $2, $3)`,
		authSub, ip, ua,
	)
	if err != nil {
		return fmt.Errorf("account.insertLoginEvent: %w", err)
	}
	return nil
}

// listLoginEvents paginates by created_at (not id — account.login_events.id
// is gen_random_uuid(), not time-ordered, so "id < cursor ORDER BY id DESC"
// wouldn't mean newest-first). Same cursor convention as
// internal/platform/audit.ListByActorCursor.
//
// Deliberately keys the cursor on created_at alone, not (created_at, id) —
// two events landing in the same nanosecond could skip/duplicate one row at
// a page boundary. Fine for a personal session list; use a composite keyset
// cursor if this ever needs stronger pagination guarantees.
func (r *repository) listLoginEvents(
	ctx context.Context, q db.Querier,
	authSub, cursor string, limit int,
) ([]loginEventRecord, string, error) {
	query := `SELECT id, auth_sub, ip_address, user_agent, created_at
		FROM account.login_events
		WHERE auth_sub = $1 AND created_at > now() - interval '90 days'`
	args := db.NewArgs(authSub)

	if cursor != "" {
		ts, err := time.Parse(time.RFC3339Nano, cursor)
		if err != nil {
			return nil, "", fmt.Errorf("account.listLoginEvents: invalid cursor: %w", err)
		}
		query += fmt.Sprintf(" AND created_at < $%d", args.Add(ts))
	}

	query += fmt.Sprintf(" ORDER BY created_at DESC LIMIT $%d", args.Add(limit))

	rows, err := q.Query(ctx, query, args.Values()...)
	if err != nil {
		return nil, "", fmt.Errorf("account.listLoginEvents: %w", err)
	}
	defer rows.Close()

	out := make([]loginEventRecord, 0, limit)
	for rows.Next() {
		var e loginEventRecord
		if err := rows.Scan(&e.ID, &e.AuthSub, &e.IPAddress, &e.UserAgent, &e.CreatedAt); err != nil {
			return nil, "", fmt.Errorf("account.listLoginEvents: scan: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, "", fmt.Errorf("account.listLoginEvents: %w", err)
	}

	nextCursor := ""
	if len(out) == limit {
		nextCursor = out[len(out)-1].CreatedAt.Format(time.RFC3339Nano)
	}
	return out, nextCursor, nil
}
