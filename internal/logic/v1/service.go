package v1

import (
	"context"
	"fmt"
	"strconv"

	"github.com/duynhlab/notification-service/internal/core/domain"
	"github.com/duynhlab/notification-service/middleware"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type NotificationService struct {
	repo domain.NotificationRepository
}

func NewNotificationService(repo domain.NotificationRepository) *NotificationService {
	return &NotificationService{
		repo: repo,
	}
}

func (s *NotificationService) SendEmail(ctx context.Context, req domain.SendEmailRequest) (*domain.Notification, error) {
	ctx, span := middleware.StartSpan(ctx, "notification.email", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("to", req.To),
	))
	defer span.End()

	// Validate recipient
	if req.To == "" || req.To == "invalid" {
		span.SetAttributes(attribute.Bool("email.sent", false))
		return nil, fmt.Errorf("send email to %q: %w", req.To, ErrInvalidRecipient)
	}

	notification := &domain.Notification{
		Type:    "email",
		Message: req.Subject, // Using subject as message/title
		Title:   req.Subject,
	}

	// Insert using repository
	err := s.repo.Create(ctx, notification, req.UserID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("create notification: %w", err)
	}

	span.SetAttributes(attribute.Bool("email.sent", true))
	span.AddEvent("notification.email.sent")

	return notification, nil
}

func (s *NotificationService) SendSMS(ctx context.Context, req domain.SendSMSRequest) (*domain.Notification, error) {
	ctx, span := middleware.StartSpan(ctx, "notification.sms", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("to", req.To),
	))
	defer span.End()

	notification := &domain.Notification{
		Type:    "sms",
		Message: req.Message,
		Title:   "SMS",
	}

	// Insert using repository
	err := s.repo.Create(ctx, notification, req.UserID)
	if err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("create notification: %w", err)
	}

	span.SetAttributes(attribute.Bool("sms.sent", true))
	span.AddEvent("notification.sms.sent")

	return notification, nil
}

// ListNotifications returns all notifications for a user
func (s *NotificationService) ListNotifications(ctx context.Context, userID string) ([]domain.Notification, error) {
	ctx, span := middleware.StartSpan(ctx, "notification.list", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("api.version", "v1"),
		attribute.String("user_id", userID),
	))
	defer span.End()

	uid, err := strconv.Atoi(userID)
	if err != nil || uid <= 0 {
		invalidErr := fmt.Errorf("invalid user_id %q: %w", userID, ErrInvalidUserID)
		span.RecordError(invalidErr)
		return nil, invalidErr
	}

	notifications, err := s.repo.ListByUserID(ctx, uid)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	span.SetAttributes(attribute.Int("notifications.count", len(notifications)))
	if notifications == nil {
		return []domain.Notification{}, nil
	}
	return notifications, nil
}

// GetNotification retrieves a single notification by ID, scoped to the owning user.
func (s *NotificationService) GetNotification(ctx context.Context, id, userID string) (*domain.Notification, error) {
	ctx, span := middleware.StartSpan(ctx, "notification.get", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("api.version", "v1"),
		attribute.String("notification.id", id),
		attribute.String("user_id", userID),
	))
	defer span.End()

	notificationID, err := strconv.Atoi(id)
	if err != nil {
		span.SetAttributes(attribute.Bool("notification.found", false))
		return nil, fmt.Errorf("invalid notification id %q: %w", id, ErrNotificationNotFound)
	}

	uid, err := strconv.Atoi(userID)
	if err != nil || uid <= 0 {
		invalidErr := fmt.Errorf("invalid user_id %q: %w", userID, ErrInvalidUserID)
		span.RecordError(invalidErr)
		return nil, invalidErr
	}

	notification, err := s.repo.FindByID(ctx, notificationID, uid)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	if notification == nil {
		span.SetAttributes(attribute.Bool("notification.found", false))
		return nil, fmt.Errorf("get notification by id %q: %w", id, ErrNotificationNotFound)
	}

	span.SetAttributes(attribute.Bool("notification.found", true))
	return notification, nil
}

// MarkAsRead marks a notification as read, scoped to the owning user.
func (s *NotificationService) MarkAsRead(ctx context.Context, id, userID string) (*domain.Notification, error) {
	ctx, span := middleware.StartSpan(ctx, "notification.mark_read", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("api.version", "v1"),
		attribute.String("notification.id", id),
		attribute.String("user_id", userID),
	))
	defer span.End()

	notificationID, err := strconv.Atoi(id)
	if err != nil {
		return nil, fmt.Errorf("invalid notification id %q: %w", id, ErrNotificationNotFound)
	}

	uid, err := strconv.Atoi(userID)
	if err != nil || uid <= 0 {
		invalidErr := fmt.Errorf("invalid user_id %q: %w", userID, ErrInvalidUserID)
		span.RecordError(invalidErr)
		return nil, invalidErr
	}

	updated, err := s.repo.MarkAsRead(ctx, notificationID, uid)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	if !updated {
		// Row did not exist or is not owned by this user — do not leak existence.
		return nil, fmt.Errorf("notification id %q: %w", id, ErrNotificationNotFound)
	}

	// Return updated notification
	return s.GetNotification(ctx, id, userID)
}

// CountUnread returns unread notification count for a user
func (s *NotificationService) CountUnread(ctx context.Context, userID string) (int, error) {
	ctx, span := middleware.StartSpan(ctx, "notification.count_unread", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("api.version", "v1"),
		attribute.String("user_id", userID),
	))
	defer span.End()

	// Security: Validate userID - reject empty or invalid input
	if userID == "" {
		return 0, fmt.Errorf("user_id is required: %w", ErrInvalidUserID)
	}
	uid, err := strconv.Atoi(userID)
	if err != nil || uid <= 0 {
		invalidErr := fmt.Errorf("invalid user_id %q: %w", userID, ErrInvalidUserID)
		span.RecordError(invalidErr)
		return 0, invalidErr
	}

	// Use repository for database access (proper 3-layer architecture)
	count, err := s.repo.CountUnreadByUserID(ctx, uid)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}

	span.SetAttributes(attribute.Int("notifications.unread_count", count))
	return count, nil
}
