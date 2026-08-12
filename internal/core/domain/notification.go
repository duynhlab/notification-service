package domain

import "context"

// NotificationRepository persists notifications. userID is the OIDC token
// subject — an opaque string (ADR-042), never parsed or compared numerically.
type NotificationRepository interface {
	Create(ctx context.Context, notification *Notification, userID string) error
	// CreateWithDeliveryKey inserts a notification deduplicated on deliveryKey:
	// a retried send with the same key loads the original row into notification
	// instead of inserting a duplicate. It reports whether the row was replayed.
	CreateWithDeliveryKey(ctx context.Context, notification *Notification, userID, deliveryKey string) (replayed bool, err error)
	FindByID(ctx context.Context, id int, userID string) (*Notification, error)
	ListByUserID(ctx context.Context, userID string, limit, offset int) ([]Notification, error)
	MarkAsRead(ctx context.Context, id int, userID string) (bool, error)
	MarkAllByUserID(ctx context.Context, userID string) (int, error)
	CountUnreadByUserID(ctx context.Context, userID string) (int, error)
	CountByUserID(ctx context.Context, userID string) (int, error)
}

type Notification struct {
	ID        string `json:"id"`
	Type      string `json:"type"`
	Title     string `json:"title,omitempty"`
	Message   string `json:"message"`
	Status    string `json:"status"`
	Read      bool   `json:"read"`
	CreatedAt string `json:"created_at,omitempty"`
}

type SendEmailRequest struct {
	// UserID is the OIDC token subject — an opaque string (ADR-042).
	UserID  string `json:"user_id" binding:"required"`
	To      string `json:"to" binding:"required,email"`
	Subject string `json:"subject" binding:"required"`
	Body    string `json:"body" binding:"required"`
	// DeliveryKey is an optional idempotency key: a retried send with the same
	// key replays the original notification instead of creating a duplicate.
	// Empty keeps the legacy at-least-once behavior.
	DeliveryKey string `json:"delivery_key,omitempty"`
}

type SendSMSRequest struct {
	// UserID is the OIDC token subject — an opaque string (ADR-042).
	UserID  string `json:"user_id" binding:"required"`
	To      string `json:"to" binding:"required"`
	Message string `json:"message" binding:"required"`
}
