package contracts

import (
	"context"
	"time"
)

// NotificationWriter is implemented by the notification module and consumed
// by modules that need to mutate notification-owned data across a schema
// boundary — currently only the GDPR account-deletion flow.
type NotificationWriter interface {
	// DeleteAllForUser deletes every notification message addressed to a user.
	DeleteAllForUser(ctx context.Context, authSub string) error
}

// MessageInfo is the minimal projection of a notification.messages row for
// the GDPR data-export flow.
type MessageInfo struct {
	ID             string    `json:"id"`
	OrganizationID string    `json:"organization_id"`
	Kind           string    `json:"kind"`
	Channel        string    `json:"channel"`
	Subject        string    `json:"subject"`
	Body           string    `json:"body"`
	CreatedAt      time.Time `json:"created_at"`
}

// NotificationReader is implemented by the notification module and consumed
// by modules that need a user's notification history without owning the
// notification schema.
type NotificationReader interface {
	// ListForUser returns up to limit of a user's most recent notification
	// messages across every kind/channel, newest first.
	ListForUser(ctx context.Context, authSub string, limit int) ([]MessageInfo, error)
}
