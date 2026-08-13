package v1

import (
	"context"
	"fmt"
	"net/mail"
	"strconv"
	"strings"
	"time"

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

	// Validate recipient in the logic layer so gRPC and HTTP share the check
	// (HTTP binding is not enforced on the gRPC path). A rejected recipient is a
	// bad request, not a send, so it is left out of the send-latency histogram.
	if _, err := mail.ParseAddress(req.To); err != nil {
		span.SetAttributes(attribute.Bool("email.sent", false))
		return nil, fmt.Errorf("send email to %q: %w", req.To, ErrInvalidRecipient)
	}

	start := time.Now()
	defer func() { recordSendDuration(ctx, channelEmail, start) }()

	notification := &domain.Notification{
		Type:    "email",
		Message: req.Body,
		Title:   req.Subject,
	}

	// Insert using repository. With a delivery key the write is idempotent:
	// a retried send (e.g. a Temporal activity retry) replays the original
	// row instead of creating a duplicate inbox entry.
	if req.DeliveryKey != "" {
		replayed, err := s.repo.CreateWithDeliveryKey(ctx, notification, req.UserID, req.DeliveryKey)
		if err != nil {
			span.RecordError(err)
			return nil, fmt.Errorf("create notification: %w", err)
		}
		span.SetAttributes(
			attribute.Bool("email.sent", !replayed),
			attribute.Bool("email.replayed", replayed),
		)
		span.AddEvent("notification.email.sent")
		return notification, nil
	}

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

	// Validate recipient in the logic layer so gRPC and HTTP share the check
	// (gRPC does no recipient validation of its own). A rejected recipient is a
	// bad request, not a send, so it is left out of the send-latency histogram.
	if strings.TrimSpace(req.To) == "" {
		span.SetAttributes(attribute.Bool("sms.sent", false))
		return nil, fmt.Errorf("send sms to %q: %w", req.To, ErrInvalidRecipient)
	}

	start := time.Now()
	defer func() { recordSendDuration(ctx, channelSMS, start) }()

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
func (s *NotificationService) ListNotifications(ctx context.Context, userID string, limit, offset int) ([]domain.Notification, int, error) {
	ctx, span := middleware.StartSpan(ctx, "notification.list", trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("api.version", "v1"),
		attribute.String("user_id", userID),
	))
	defer span.End()

	// userID is the OIDC token subject — opaque, so only emptiness is invalid.
	if userID == "" {
		invalidErr := fmt.Errorf("user_id is required: %w", ErrInvalidUserID)
		span.RecordError(invalidErr)
		return nil, 0, invalidErr
	}

	total, err := s.repo.CountByUserID(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return nil, 0, err
	}

	notifications, err := s.repo.ListByUserID(ctx, userID, limit, offset)
	if err != nil {
		span.RecordError(err)
		return nil, 0, err
	}

	span.SetAttributes(attribute.Int("notifications.count", len(notifications)))
	if notifications == nil {
		notifications = []domain.Notification{}
	}
	return notifications, total, nil
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

	// userID is the OIDC token subject — opaque, so only emptiness is invalid.
	if userID == "" {
		invalidErr := fmt.Errorf("user_id is required: %w", ErrInvalidUserID)
		span.RecordError(invalidErr)
		return nil, invalidErr
	}

	notification, err := s.repo.FindByID(ctx, notificationID, userID)
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

	// userID is the OIDC token subject — opaque, so only emptiness is invalid.
	if userID == "" {
		invalidErr := fmt.Errorf("user_id is required: %w", ErrInvalidUserID)
		span.RecordError(invalidErr)
		return nil, invalidErr
	}

	updated, err := s.repo.MarkAsRead(ctx, notificationID, userID)
	if err != nil {
		span.RecordError(err)
		return nil, err
	}

	if !updated {
		// Row did not exist or is not owned by this user — do not leak existence.
		return nil, fmt.Errorf("notification id %q: %w", id, ErrNotificationNotFound)
	}

	recordRead(ctx, modeSingle, 1)

	// Return updated notification
	return s.GetNotification(ctx, id, userID)
}

// userScopedCount validates the user_id and runs a user-scoped action returning
// an integer (unread count, rows marked, …). Shared by the count-style methods so
// the validation + tracing live in one place.
func (s *NotificationService) userScopedCount(ctx context.Context, userID, spanName string, action func(context.Context, string) (int, error)) (int, error) {
	ctx, span := middleware.StartSpan(ctx, spanName, trace.WithAttributes(
		attribute.String("layer", "logic"),
		attribute.String("api.version", "v1"),
		attribute.String("user_id", userID),
	))
	defer span.End()

	// Security: userID is the OIDC token subject — opaque, so only emptiness
	// is invalid.
	if userID == "" {
		return 0, fmt.Errorf("user_id is required: %w", ErrInvalidUserID)
	}

	n, err := action(ctx, userID)
	if err != nil {
		span.RecordError(err)
		return 0, err
	}

	span.SetAttributes(attribute.Int("result.count", n))
	return n, nil
}

// CountUnread returns unread notification count for a user.
func (s *NotificationService) CountUnread(ctx context.Context, userID string) (int, error) {
	return s.userScopedCount(ctx, userID, "notification.count_unread", s.repo.CountUnreadByUserID)
}

// MarkAllAsRead marks every unread notification for a user as read and returns
// how many were flipped. Idempotent: marking zero rows is a success, not a 404.
func (s *NotificationService) MarkAllAsRead(ctx context.Context, userID string) (int, error) {
	n, err := s.userScopedCount(ctx, userID, "notification.mark_all_read", s.repo.MarkAllByUserID)
	if err != nil {
		return 0, err
	}
	recordRead(ctx, modeAll, int64(n))
	return n, nil
}
