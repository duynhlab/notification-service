package database

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/duynhlab/notification-service/internal/core/domain"
	"github.com/jackc/pgx/v5"
)

// NotificationRepository handles database operations for notifications.
// This abstraction keeps SQL queries in the Core layer (proper 3-layer architecture).
type NotificationRepository struct{}

// NewNotificationRepository creates a new NotificationRepository.
func NewNotificationRepository() *NotificationRepository {
	return &NotificationRepository{}
}

// CountUnreadByUserID returns the count of unread notifications for a user.
func (r *NotificationRepository) CountUnreadByUserID(ctx context.Context, userID int) (int, error) {
	db := GetPool()
	if db == nil {
		return 0, errors.New("database connection not available")
	}

	var count int
	err := db.QueryRow(ctx, `SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read = false`, userID).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count unread notifications: %w", err)
	}

	return count, nil
}

// Create inserts a new notification into the database.
func (r *NotificationRepository) Create(ctx context.Context, notification *domain.Notification, userID int) error {
	db := GetPool()
	if db == nil {
		return errors.New("database connection not available")
	}

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

	err := db.QueryRow(ctx, query, userID, title, message, notification.Type, false).Scan(&id, &createdAt)
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
	db := GetPool()
	if db == nil {
		return nil, errors.New("database connection not available")
	}

	query := `SELECT id, user_id, title, message, type, read, created_at FROM notifications WHERE id = $1 AND user_id = $2`
	var notificationID, ownerID int
	var title, message, notifType *string
	var read bool
	var createdAt time.Time

	err := db.QueryRow(ctx, query, id, userID).Scan(&notificationID, &ownerID, &title, &message, &notifType, &read, &createdAt)
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

// ListByUserID retrieves all notifications for a specific user.
func (r *NotificationRepository) ListByUserID(ctx context.Context, userID int) ([]domain.Notification, error) {
	db := GetPool()
	if db == nil {
		return nil, errors.New("database connection not available")
	}

	query := `SELECT id, user_id, title, message, type, read, created_at FROM notifications WHERE user_id = $1 ORDER BY created_at DESC`
	rows, err := db.Query(ctx, query, userID)
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
	db := GetPool()
	if db == nil {
		return false, errors.New("database connection not available")
	}

	query := `UPDATE notifications SET read = true WHERE id = $1 AND user_id = $2`
	result, err := db.Exec(ctx, query, id, userID)
	if err != nil {
		return false, fmt.Errorf("update notification: %w", err)
	}

	return result.RowsAffected() > 0, nil
}
