package v1

import (
	"context"
	"errors"
	"testing"

	"github.com/duynhlab/notification-service/internal/core/domain"
	logicv1 "github.com/duynhlab/notification-service/internal/logic/v1"
	notificationv1 "github.com/duynhlab/pkg/proto/notification/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// sendStub is a configurable Notifier double. It records the last request so
// tests can assert what the adapter forwarded to the logic layer.
type sendStub struct {
	result *domain.Notification
	err    error

	gotEmail domain.SendEmailRequest
	gotSMS   domain.SendSMSRequest
}

func (s *sendStub) SendEmail(_ context.Context, req domain.SendEmailRequest) (*domain.Notification, error) {
	s.gotEmail = req
	return s.result, s.err
}
func (s *sendStub) SendSMS(_ context.Context, req domain.SendSMSRequest) (*domain.Notification, error) {
	s.gotSMS = req
	return s.result, s.err
}

// aliceSub is the fixed Keycloak subject of the demo user alice (ADR-042):
// user_id is the OIDC token subject, an opaque string.
const aliceSub = "a11ce000-0000-4000-8000-000000000001"

func sample() *domain.Notification {
	return &domain.Notification{
		ID: "5", Type: "email", Title: "Hi", Message: "Body",
		Status: "sent", Read: false, CreatedAt: "2026-06-30T00:00:00Z",
	}
}

func TestServer_SendEmail(t *testing.T) {
	t.Run("success maps domain to proto", func(t *testing.T) {
		stub := &sendStub{result: sample()}
		srv := NewServer(stub)
		resp, err := srv.SendEmail(context.Background(), &notificationv1.SendEmailRequest{
			UserId: aliceSub, To: "a@b.com", Subject: "Hi", Body: "Body",
		})
		if err != nil {
			t.Fatalf("got error %v, want nil", err)
		}
		if stub.gotEmail.UserID != aliceSub {
			t.Errorf("forwarded UserID = %q, want the opaque subject %q", stub.gotEmail.UserID, aliceSub)
		}
		n := resp.GetNotification()
		if n.GetId() != "5" || n.GetType() != "email" || n.GetTitle() != "Hi" {
			t.Errorf("notification = %+v, want id=5 type=email title=Hi", n)
		}
		if n.GetMessage() != "Body" || n.GetStatus() != "sent" || n.GetRead() {
			t.Errorf("notification fields mismatch: %+v", n)
		}
		if n.GetCreatedAt() != "2026-06-30T00:00:00Z" {
			t.Errorf("created_at = %q", n.GetCreatedAt())
		}
	})

	t.Run("invalid recipient -> InvalidArgument", func(t *testing.T) {
		srv := NewServer(&sendStub{err: logicv1.ErrInvalidRecipient})
		_, err := srv.SendEmail(context.Background(), &notificationv1.SendEmailRequest{To: "bad"})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
		}
	})

	t.Run("generic error -> Internal", func(t *testing.T) {
		srv := NewServer(&sendStub{err: errors.New("smtp down")})
		_, err := srv.SendEmail(context.Background(), &notificationv1.SendEmailRequest{To: "a@b.com"})
		if status.Code(err) != codes.Internal {
			t.Fatalf("code = %v, want Internal", status.Code(err))
		}
	})
}

func TestServer_SendSMS(t *testing.T) {
	t.Run("success maps domain to proto", func(t *testing.T) {
		stub := &sendStub{result: sample()}
		srv := NewServer(stub)
		resp, err := srv.SendSMS(context.Background(), &notificationv1.SendSMSRequest{
			UserId: aliceSub, To: "+123", Message: "hello",
		})
		if err != nil {
			t.Fatalf("got error %v, want nil", err)
		}
		if resp.GetNotification().GetId() != "5" {
			t.Errorf("id = %q, want 5", resp.GetNotification().GetId())
		}
		if stub.gotSMS.UserID != aliceSub {
			t.Errorf("forwarded UserID = %q, want the opaque subject %q", stub.gotSMS.UserID, aliceSub)
		}
	})

	t.Run("invalid recipient -> InvalidArgument", func(t *testing.T) {
		srv := NewServer(&sendStub{err: logicv1.ErrInvalidRecipient})
		_, err := srv.SendSMS(context.Background(), &notificationv1.SendSMSRequest{To: ""})
		if status.Code(err) != codes.InvalidArgument {
			t.Fatalf("code = %v, want InvalidArgument", status.Code(err))
		}
	})

	t.Run("generic error -> Internal", func(t *testing.T) {
		srv := NewServer(&sendStub{err: errors.New("gateway down")})
		_, err := srv.SendSMS(context.Background(), &notificationv1.SendSMSRequest{To: "+1"})
		if status.Code(err) != codes.Internal {
			t.Fatalf("code = %v, want Internal", status.Code(err))
		}
	})
}
