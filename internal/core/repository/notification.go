// Package repository holds the Core-layer database implementations of the domain
// repository interfaces. Implementations receive their *pgxpool.Pool via the
// constructor (dependency injection) rather than reaching for a global pool.
package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/duynhlab/notification-service/internal/core/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// NotificationRepository is the pgx-backed implementation of
// domain.NotificationRepository.
type NotificationRepository struct {
	pool *pgxpool.Pool
}

// NewNotificationRepository wires the repository with an injected connection pool.
func NewNotificationRepository(pool *pgxpool.Pool) domain.NotificationRepository {
	return &NotificationRepository{pool: pool}
}

// CountUnreadByUserID returns the count of unread notifications for a user.
func (r *NotificationRepository) CountUnreadByUserID(ctx context.Context, userID int) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read = false`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}
	return count, nil
}

// CountByUserID returns the total number of notifications for a user (for pagination).
func (r *NotificationRepository) CountByUserID(ctx context.Context, userID int) (int, error) {
	var count int
	err := r.pool.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = $1`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count notifications: %w", err)
	}
	return count, nil
}

// Create inserts a new notification into the database.
func (r *NotificationRepository) Create(ctx context.Context, notification *domain.Notification, userID int) error {
	query := `INSERT INTO notifications (user_id, title, message, type, read) VALUES ($1, $2, $3, $4, $5) RETURNING id, created_at`
	var id int
	var createdAt time.Time

	// Use title as message if not provided, or vice versa, to match existing logic
	title := notification.Title
	if title == "" {
		title = notification.Message
	}
	message := notification.Message
	if message == "" {
		message = title
	}

	err := r.pool.QueryRow(ctx, query, userID, title, message, notification.Type, false).Scan(&id, &createdAt)
	if err != nil {
		return fmt.Errorf("insert notification: %w", err)
	}

	notification.ID = strconv.Itoa(id)
	notification.CreatedAt = createdAt.Format(time.RFC3339)
	notification.Read = false
	notification.Status = "sent" // Default status

	return nil
}

// FindByID retrieves a notification by its ID, scoped to the owning user.
func (r *NotificationRepository) FindByID(ctx context.Context, id, userID int) (*domain.Notification, error) {
	query := `SELECT id, user_id, title, message, type, read, created_at FROM notifications WHERE id = $1 AND user_id = $2`
	var notificationID, ownerID int
	var title, message, notifType *string
	var read bool
	var createdAt time.Time

	err := r.pool.QueryRow(ctx, query, id, userID).Scan(&notificationID, &ownerID, &title, &message, &notifType, &read, &createdAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil // Return nil if not found, let caller handle specific error
		}
		return nil, fmt.Errorf("query notification: %w", err)
	}

	notification := &domain.Notification{
		ID:        strconv.Itoa(notificationID),
		Status:    "sent",
		Read:      read,
		CreatedAt: createdAt.Format(time.RFC3339),
	}
	if title != nil {
		notification.Title = *title
	}
	if message != nil {
		notification.Message = *message
	}
	// Fallback/Backward compat logic
	if notification.Title == "" && notification.Message != "" {
		notification.Title = notification.Message
	} else if notification.Message == "" && notification.Title != "" {
		notification.Message = notification.Title
	}

	if notifType != nil {
		notification.Type = *notifType
	}

	return notification, nil
}

// ListByUserID retrieves a page of notifications for a specific user, newest first.
func (r *NotificationRepository) ListByUserID(ctx context.Context, userID, limit, offset int) ([]domain.Notification, error) {
	query := `SELECT id, user_id, title, message, type, read, created_at FROM notifications WHERE user_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`
	rows, err := r.pool.Query(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query notifications: %w", err)
	}
	defer rows.Close()

	var notifications []domain.Notification
	for rows.Next() {
		var notificationID, dbUserID int
		var title, message, notifType *string
		var read bool
		var createdAt time.Time

		err := rows.Scan(&notificationID, &dbUserID, &title, &message, &notifType, &read, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("scan notification: %w", err)
		}

		notif := domain.Notification{
			ID:        strconv.Itoa(notificationID),
			Status:    "sent",
			Read:      read,
			CreatedAt: createdAt.Format(time.RFC3339),
		}
		if title != nil {
			notif.Title = *title
		}
		if message != nil {
			notif.Message = *message
		}
		// Fallback/Backward compat logic
		if notif.Title == "" && notif.Message != "" {
			notif.Title = notif.Message
		} else if notif.Message == "" && notif.Title != "" {
			notif.Message = notif.Title
		}

		if notifType != nil {
			notif.Type = *notifType
		}

		notifications = append(notifications, notif)
	}

	if err = rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate notifications: %w", err)
	}

	return notifications, nil
}

// MarkAsRead marks a notification as read, scoped to the owning user.
// Returns true if updated, false if not found (or not owned by the user).
func (r *NotificationRepository) MarkAsRead(ctx context.Context, id, userID int) (bool, error) {
	query := `UPDATE notifications SET read = true WHERE id = $1 AND user_id = $2`
	result, err := r.pool.Exec(ctx, query, id, userID)
	if err != nil {
		return false, fmt.Errorf("update notification: %w", err)
	}
	return result.RowsAffected() > 0, nil
}

// MarkAllByUserID marks every unread notification for a user as read and returns
// how many rows were flipped. Idempotent: a second call affects zero rows.
func (r *NotificationRepository) MarkAllByUserID(ctx context.Context, userID int) (int, error) {
	query := `UPDATE notifications SET read = true WHERE user_id = $1 AND read = false`
	result, err := r.pool.Exec(ctx, query, userID)
	if err != nil {
		return 0, fmt.Errorf("mark all notifications read: %w", err)
	}
	return int(result.RowsAffected()), nil
}
