// Package v1 implements the gRPC transport for notification, version 1. It is a
// thin adapter over the logic layer (mirroring internal/web/v1) so the gRPC and
// HTTP paths share the same business logic and return identical data.
package v1

import (
	"context"
	"errors"

	"github.com/duynhlab/notification-service/internal/core/domain"
	logicv1 "github.com/duynhlab/notification-service/internal/logic/v1"
	notificationv1 "github.com/duynhlab/pkg/proto/notification/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Notifier is the logic-layer dependency the gRPC server needs.
// *logicv1.NotificationService satisfies it.
type Notifier interface {
	SendEmail(ctx context.Context, req domain.SendEmailRequest) (*domain.Notification, error)
	SendSMS(ctx context.Context, req domain.SendSMSRequest) (*domain.Notification, error)
}

// Server implements notificationv1.NotificationServiceServer.
type Server struct {
	notificationv1.UnimplementedNotificationServiceServer

	svc Notifier
}

// NewServer creates a gRPC NotificationService server backed by the logic service.
func NewServer(svc Notifier) *Server {
	return &Server{svc: svc}
}

// SendEmail mirrors POST /notification/v1/internal/notifications/email, sending an
// email notification and returning the created record.
func (s *Server) SendEmail(
	ctx context.Context,
	req *notificationv1.SendEmailRequest,
) (*notificationv1.SendEmailResponse, error) {
	n, err := s.svc.SendEmail(ctx, domain.SendEmailRequest{
		UserID:      int(req.GetUserId()),
		To:          req.GetTo(),
		Subject:     req.GetSubject(),
		Body:        req.GetBody(),
		DeliveryKey: req.GetDeliveryKey(),
	})
	if err != nil {
		if errors.Is(err, logicv1.ErrInvalidRecipient) {
			return nil, status.Error(codes.InvalidArgument, "invalid recipient")
		}
		return nil, status.Error(codes.Internal, "failed to send email")
	}

	return &notificationv1.SendEmailResponse{Notification: toProto(n)}, nil
}

// SendSMS mirrors POST /notification/v1/internal/notifications/sms, sending an SMS
// notification and returning the created record.
func (s *Server) SendSMS(
	ctx context.Context,
	req *notificationv1.SendSMSRequest,
) (*notificationv1.SendSMSResponse, error) {
	n, err := s.svc.SendSMS(ctx, domain.SendSMSRequest{
		UserID:  int(req.GetUserId()),
		To:      req.GetTo(),
		Message: req.GetMessage(),
	})
	if err != nil {
		if errors.Is(err, logicv1.ErrInvalidRecipient) {
			return nil, status.Error(codes.InvalidArgument, "invalid recipient")
		}
		return nil, status.Error(codes.Internal, "failed to send sms")
	}

	return &notificationv1.SendSMSResponse{Notification: toProto(n)}, nil
}

// toProto maps a domain notification to its protobuf representation. CreatedAt is
// stored as an RFC3339 string in the domain model, so it maps through directly.
func toProto(n *domain.Notification) *notificationv1.Notification {
	return &notificationv1.Notification{
		Id:        n.ID,
		Type:      n.Type,
		Title:     n.Title,
		Message:   n.Message,
		Status:    n.Status,
		Read:      n.Read,
		CreatedAt: n.CreatedAt,
	}
}
