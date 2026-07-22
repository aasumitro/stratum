package contracts

import "context"

// UserInfo is the minimal projection of user data other modules may
// depend on. Never import internal/modules/user directly.
type UserInfo struct {
	ID        string
	AuthSub   string // JWT sub claim from the third-party IdP
	Email     string
	Name      string
	AvatarURL string
	Lang      string // from account.users.preferences.lang; empty if unset
}

// UserReader is implemented by the user module and consumed by modules
// that need to resolve a user without owning the users schema.
type UserReader interface {
	GetUserByAuthSub(ctx context.Context, sub string) (*UserInfo, error)

	// GetUserByEmail resolves a user by their account email. Internal
	// server-to-server lookup only (e.g. the notification worker deciding
	// whether an invitee already has an account) — never expose this as an
	// API a client can query directly, which would let anyone probe for
	// registered emails.
	GetUserByEmail(ctx context.Context, email string) (*UserInfo, error)

	// GetUsersByAuthSubs resolves multiple users in one call — batched
	// (WHERE auth_sub = ANY($1)), not one query per auth_sub, so callers
	// enriching a list of rows with profile data don't do it inside a loop.
	// Missing auth_subs are simply absent from the returned map, not an error.
	GetUsersByAuthSubs(ctx context.Context, authSubs []string) (map[string]UserInfo, error)

	// IsMFAEnabled reports whether the user has at least one verified MFA
	// factor enrolled. Backed by account.users.mfa_enabled, which is only
	// ever set from an authoritative Supabase Admin API lookup — never
	// trust a client-supplied value for this.
	IsMFAEnabled(ctx context.Context, sub string) (bool, error)
}
