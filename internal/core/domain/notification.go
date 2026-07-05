package domain

import "context"

type NotificationRepository interface {
	Create(ctx context.Context, notification *Notification, userID int) error
	FindByID(ctx context.Context, id, userID int) (*Notification, error)
	ListByUserID(ctx context.Context, userID, limit, offset int) ([]Notification, error)
	MarkAsRead(ctx context.Context, id, userID int) (bool, error)
	MarkAllByUserID(ctx context.Context, userID int) (int, error)
	CountUnreadByUserID(ctx context.Context, userID int) (int, error)
	CountByUserID(ctx context.Context, userID int) (int, error)
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
	UserID  int    `json:"user_id" binding:"required"`
	To      string `json:"to" binding:"required,email"`
	Subject string `json:"subject" binding:"required"`
	Body    string `json:"body" binding:"required"`
}

type SendSMSRequest struct {
	UserID  int    `json:"user_id" binding:"required"`
	To      string `json:"to" binding:"required"`
	Message string `json:"message" binding:"required"`
}
