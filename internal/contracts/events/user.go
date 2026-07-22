package events

import "time"

const (
	RoutingKeyUserUpdated       = "user.updated"
	RoutingKeyUserEmailChanged  = "user.email_changed"
	RoutingKeyUserDeleteRequest = "user.delete.request"
	RoutingKeyUserExportRequest = "user.export.request"
)

type UserUpdated struct {
	UserID    string    `json:"user_id"`
	UpdatedAt time.Time `json:"updated_at"`
}

// UserEmailChanged is published after account.users.email is synced from a
// verified Supabase auth.users webhook — never from PATCH /me, which
// deliberately never touches email.
type UserEmailChanged struct {
	UserID    string    `json:"user_id"`
	AuthSub   string    `json:"auth_sub"`
	OldEmail  string    `json:"old_email"`
	NewEmail  string    `json:"new_email"`
	ChangedAt time.Time `json:"changed_at"`
}

type UserTaskRequest struct {
	TaskID  string `json:"task_id"`
	AuthSub string `json:"auth_sub"`
}
