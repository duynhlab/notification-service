package v1

import (
	"context"
	"errors"
	"net/http"

	"github.com/duynhlab/notification-service/internal/core/domain"
	logicv1 "github.com/duynhlab/notification-service/internal/logic/v1"
	"github.com/duynhlab/notification-service/middleware"
	"github.com/duynhlab/pkg/httpx"
	"github.com/gin-gonic/gin"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
)

const (
	// attrAuthMissing is the span attribute set when no authenticated user is present.
	attrAuthMissing = "auth.missing"
	// logMsgMissingUserID is logged when user_id is absent from the request context.
	logMsgMissingUserID = "Missing user_id in request context"
	// errAuthRequired is the response message when a request lacks a valid user.
	errAuthRequired = "Authentication required"
	// errInternal is the generic 500 response message.
	errInternal = "Internal server error"
)

type Handler struct {
	service *logicv1.NotificationService
}

func NewHandler(service *logicv1.NotificationService) *Handler {
	return &Handler{service: service}
}

func (h *Handler) SendEmail(c *gin.Context) {
	ctx, span := middleware.StartSpan(c.Request.Context(), "http.request", trace.WithAttributes(
		attribute.String("layer", "web"),
		attribute.String("method", c.Request.Method),
		attribute.String("path", c.Request.URL.Path),
	))
	defer span.End()

	zapLogger := middleware.GetLoggerFromGinContext(c)

	var req domain.SendEmailRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		span.SetAttributes(attribute.Bool("request.valid", false))
		span.RecordError(err)
		zapLogger.Error("Invalid request", zap.Error(err))
		httpx.RespondError(c, http.StatusBadRequest, httpx.CodeValidation, "invalid request body")
		return
	}

	span.SetAttributes(attribute.Bool("request.valid", true))
	notification, err := h.service.SendEmail(ctx, req)
	if err != nil {
		span.RecordError(err)
		zapLogger.Error("Failed to send email", zap.Error(err))

		switch {
		case errors.Is(err, logicv1.ErrInvalidRecipient):
			httpx.RespondError(c, http.StatusBadRequest, httpx.CodeValidation, "Invalid recipient")
		case errors.Is(err, logicv1.ErrDeliveryFailed):
			httpx.RespondError(c, http.StatusInternalServerError, httpx.CodeInternal, "Delivery failed")
		default:
			httpx.RespondError(c, http.StatusInternalServerError, httpx.CodeInternal, errInternal)
		}
		return
	}

	zapLogger.Info("Email sent", zap.String("notification_id", notification.ID))
	c.JSON(http.StatusOK, notification)
}

func (h *Handler) SendSMS(c *gin.Context) {
	ctx, span := middleware.StartSpan(c.Request.Context(), "http.request", trace.WithAttributes(
		attribute.String("layer", "web"),
		attribute.String("method", c.Request.Method),
		attribute.String("path", c.Request.URL.Path),
	))
	defer span.End()

	zapLogger := middleware.GetLoggerFromGinContext(c)

	var req domain.SendSMSRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		span.SetAttributes(attribute.Bool("request.valid", false))
		span.RecordError(err)
		zapLogger.Error("Invalid request", zap.Error(err))
		httpx.RespondError(c, http.StatusBadRequest, httpx.CodeValidation, "invalid request body")
		return
	}

	span.SetAttributes(attribute.Bool("request.valid", true))
	notification, err := h.service.SendSMS(ctx, req)
	if err != nil {
		span.RecordError(err)
		zapLogger.Error("Failed to send SMS", zap.Error(err))
		httpx.RespondError(c, http.StatusInternalServerError, httpx.CodeInternal, errInternal)
		return
	}

	zapLogger.Info("SMS sent", zap.String("notification_id", notification.ID))
	c.JSON(http.StatusOK, notification)
}

// ListNotifications handles GET /notification/v1/private/notifications
func (h *Handler) ListNotifications(c *gin.Context) {
	ctx, span := middleware.StartSpan(c.Request.Context(), "http.request", trace.WithAttributes(
		attribute.String("layer", "web"),
		attribute.String("api.version", "v1"),
		attribute.String("method", c.Request.Method),
		attribute.String("path", c.Request.URL.Path),
	))
	defer span.End()

	zapLogger := middleware.GetLoggerFromGinContext(c)

	// Security: Require valid user_id from auth middleware
	userID := c.GetString("user_id")
	if userID == "" {
		span.SetAttributes(attribute.Bool(attrAuthMissing, true))
		zapLogger.Warn(logMsgMissingUserID)
		httpx.RespondError(c, http.StatusUnauthorized, httpx.CodeUnauthorized, errAuthRequired)
		return
	}

	page, pageSize := httpx.ParsePage(c)
	notifications, total, err := h.service.ListNotifications(ctx, userID, pageSize, httpx.Offset(page, pageSize))
	if err != nil {
		span.RecordError(err)
		zapLogger.Error("Failed to list notifications", zap.Error(err))
		httpx.RespondError(c, http.StatusInternalServerError, httpx.CodeInternal, errInternal)
		return
	}

	zapLogger.Info("Notifications listed", zap.Int("count", len(notifications)))
	c.JSON(http.StatusOK, httpx.NewPaginated(notifications, page, pageSize, total))
}

// handleNotificationByID is a shared handler for operations on a single notification by ID.
// It extracts common boilerplate (span setup, ID extraction, error handling) to avoid duplication.
func (h *Handler) handleNotificationByID(
	c *gin.Context,
	action func(ctx context.Context, id, userID string) (*domain.Notification, error),
	successLog string,
) {
	ctx, span := middleware.StartSpan(c.Request.Context(), "http.request", trace.WithAttributes(
		attribute.String("layer", "web"),
		attribute.String("api.version", "v1"),
		attribute.String("method", c.Request.Method),
		attribute.String("path", c.Request.URL.Path),
	))
	defer span.End()

	zapLogger := middleware.GetLoggerFromGinContext(c)

	// Security: Require valid user_id from auth middleware
	userID := c.GetString("user_id")
	if userID == "" {
		span.SetAttributes(attribute.Bool(attrAuthMissing, true))
		zapLogger.Warn(logMsgMissingUserID)
		httpx.RespondError(c, http.StatusUnauthorized, httpx.CodeUnauthorized, errAuthRequired)
		return
	}

	id := c.Param("id")
	span.SetAttributes(attribute.String("notification.id", id))

	notification, err := action(ctx, id, userID)
	if err != nil {
		span.RecordError(err)
		zapLogger.Error(successLog+" failed", zap.Error(err))

		switch {
		case errors.Is(err, logicv1.ErrNotificationNotFound):
			httpx.RespondError(c, http.StatusNotFound, httpx.CodeNotFound, "Notification not found")
		default:
			httpx.RespondError(c, http.StatusInternalServerError, httpx.CodeInternal, errInternal)
		}
		return
	}

	zapLogger.Info(successLog, zap.String("notification_id", id))
	c.JSON(http.StatusOK, notification)
}

// GetNotification handles GET /notification/v1/private/notifications/:id
func (h *Handler) GetNotification(c *gin.Context) {
	h.handleNotificationByID(c, h.service.GetNotification, "Notification retrieved")
}

// MarkAsRead handles PATCH /notification/v1/private/notifications/:id
func (h *Handler) MarkAsRead(c *gin.Context) {
	h.handleNotificationByID(c, h.service.MarkAsRead, "Notification marked as read")
}

// respondUserScopedCount runs a user-scoped action returning an integer,
// applying the shared auth-check, error handling, and {resultKey: n} response
// used by the count-style endpoints. resultKey names both the JSON field and
// the log field; errLog / okLog are the failure / success log messages.
func (h *Handler) respondUserScopedCount(c *gin.Context, resultKey, errLog, okLog string, action func(context.Context, string) (int, error)) {
	ctx, span := middleware.StartSpan(c.Request.Context(), "http.request", trace.WithAttributes(
		attribute.String("layer", "web"),
		attribute.String("api.version", "v1"),
		attribute.String("method", c.Request.Method),
		attribute.String("path", c.Request.URL.Path),
	))
	defer span.End()

	zapLogger := middleware.GetLoggerFromGinContext(c)

	// Security: Require valid user_id from auth middleware
	userID := c.GetString("user_id")
	if userID == "" {
		span.SetAttributes(attribute.Bool(attrAuthMissing, true))
		zapLogger.Warn(logMsgMissingUserID)
		httpx.RespondError(c, http.StatusUnauthorized, httpx.CodeUnauthorized, errAuthRequired)
		return
	}

	n, err := action(ctx, userID)
	if err != nil {
		span.RecordError(err)
		zapLogger.Error(errLog, zap.Error(err))
		httpx.RespondError(c, http.StatusInternalServerError, httpx.CodeInternal, errInternal)
		return
	}

	zapLogger.Info(okLog, zap.Int(resultKey, n))
	c.JSON(http.StatusOK, gin.H{resultKey: n})
}

// GetUnreadCount handles GET /notification/v1/private/notifications/count
func (h *Handler) GetUnreadCount(c *gin.Context) {
	h.respondUserScopedCount(c, "count", "Failed to count unread notifications", "Unread count retrieved", h.service.CountUnread)
}

// MarkAllAsRead handles PATCH /notification/v1/private/notifications/read-all
func (h *Handler) MarkAllAsRead(c *gin.Context) {
	h.respondUserScopedCount(c, "updated", "Failed to mark all notifications read", "Marked all notifications read", h.service.MarkAllAsRead)
}
