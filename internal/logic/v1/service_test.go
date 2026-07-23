package v1

import (
	"context"
	"errors"
	"testing"

	"github.com/duynhlab/notification-service/internal/core/domain"
)

// mockRepo is a configurable in-memory stub of domain.NotificationRepository.
type mockRepo struct {
	createErr error
	createFn  func(n *domain.Notification, userID int)
	replayed  bool

	findByID    *domain.Notification
	findByIDErr error

	listResult []domain.Notification
	listErr    error

	markUpdated bool
	markErr     error

	markAllCount int
	markAllErr   error

	unreadCount int
	unreadErr   error

	totalCount int
	totalErr   error
}

func (m *mockRepo) Create(_ context.Context, n *domain.Notification, userID int) error {
	if m.createFn != nil {
		m.createFn(n, userID)
	}
	return m.createErr
}

func (m *mockRepo) CreateWithDeliveryKey(_ context.Context, n *domain.Notification, userID int, _ string) (bool, error) {
	if m.createFn != nil {
		m.createFn(n, userID)
	}
	return m.replayed, m.createErr
}

func (m *mockRepo) FindByID(_ context.Context, _, _ int) (*domain.Notification, error) {
	return m.findByID, m.findByIDErr
}

func (m *mockRepo) ListByUserID(_ context.Context, _, _, _ int) ([]domain.Notification, error) {
	return m.listResult, m.listErr
}

func (m *mockRepo) MarkAsRead(_ context.Context, _, _ int) (bool, error) {
	return m.markUpdated, m.markErr
}

func (m *mockRepo) MarkAllByUserID(_ context.Context, _ int) (int, error) {
	return m.markAllCount, m.markAllErr
}

func (m *mockRepo) CountUnreadByUserID(_ context.Context, _ int) (int, error) {
	return m.unreadCount, m.unreadErr
}

func (m *mockRepo) CountByUserID(_ context.Context, _ int) (int, error) {
	return m.totalCount, m.totalErr
}

var errRepo = errors.New("repo failure")

func TestSendEmail(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		req       domain.SendEmailRequest
		repo      *mockRepo
		wantErrIs error
		wantNil   bool
	}{
		{
			name:    "valid email",
			req:     domain.SendEmailRequest{UserID: 1, To: "a@b.com", Subject: "Hi", Body: "Body"},
			repo:    &mockRepo{},
			wantNil: false,
		},
		{
			name:      "empty recipient",
			req:       domain.SendEmailRequest{UserID: 1, To: "", Subject: "Hi"},
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidRecipient,
			wantNil:   true,
		},
		{
			name:      "malformed recipient address",
			req:       domain.SendEmailRequest{UserID: 1, To: "not-an-email", Subject: "Hi"},
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidRecipient,
			wantNil:   true,
		},
		{
			name:      "repository error",
			req:       domain.SendEmailRequest{UserID: 1, To: "a@b.com", Subject: "Hi"},
			repo:      &mockRepo{createErr: errRepo},
			wantErrIs: errRepo,
			wantNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := NewNotificationService(tt.repo)
			got, err := svc.SendEmail(context.Background(), tt.req)

			assertErrIs(t, err, tt.wantErrIs)
			if tt.wantNil && got != nil {
				t.Errorf("expected nil notification, got %+v", got)
			}
			if !tt.wantNil {
				if got == nil {
					t.Fatal("expected notification, got nil")
				}
				if got.Type != "email" {
					t.Errorf("Type = %q, want email", got.Type)
				}
				if got.Message != tt.req.Body {
					t.Errorf("Message = %q, want Body %q", got.Message, tt.req.Body)
				}
				if got.Title != tt.req.Subject {
					t.Errorf("Title = %q, want Subject %q", got.Title, tt.req.Subject)
				}
			}
		})
	}
}

func TestSendEmailDeliveryKey(t *testing.T) {
	t.Run("with key uses idempotent path", func(t *testing.T) {
		var gotUserID int
		repo := &mockRepo{createFn: func(n *domain.Notification, userID int) {
			gotUserID = userID
			n.ID = "7"
		}}
		svc := NewNotificationService(repo)
		n, err := svc.SendEmail(context.Background(), domain.SendEmailRequest{
			UserID: 42, To: "a@b.dev", Subject: "s", Body: "b",
			DeliveryKey: "order:42:type:order_confirmed:version:1",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if gotUserID != 42 || n.ID != "7" {
			t.Fatalf("idempotent path not used: userID=%d id=%q", gotUserID, n.ID)
		}
	})

	t.Run("replayed send returns original row", func(t *testing.T) {
		repo := &mockRepo{replayed: true, createFn: func(n *domain.Notification, _ int) { n.ID = "7" }}
		svc := NewNotificationService(repo)
		n, err := svc.SendEmail(context.Background(), domain.SendEmailRequest{
			UserID: 42, To: "a@b.dev", Subject: "s", Body: "b", DeliveryKey: "k",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if n.ID != "7" {
			t.Fatalf("replay did not return original row: id=%q", n.ID)
		}
	})

	t.Run("repository error surfaces", func(t *testing.T) {
		repo := &mockRepo{createErr: errRepo}
		svc := NewNotificationService(repo)
		if _, err := svc.SendEmail(context.Background(), domain.SendEmailRequest{
			UserID: 42, To: "a@b.dev", Subject: "s", Body: "b", DeliveryKey: "k",
		}); err == nil {
			t.Fatal("want error")
		}
	})
}

func TestSendSMS(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		req       domain.SendSMSRequest
		repo      *mockRepo
		wantErrIs error
		wantNil   bool
	}{
		{
			name:    "valid sms",
			req:     domain.SendSMSRequest{UserID: 1, To: "+1555", Message: "hello"},
			repo:    &mockRepo{},
			wantNil: false,
		},
		{
			name:      "repository error",
			req:       domain.SendSMSRequest{UserID: 1, To: "+1555", Message: "hello"},
			repo:      &mockRepo{createErr: errRepo},
			wantErrIs: errRepo,
			wantNil:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := NewNotificationService(tt.repo)
			got, err := svc.SendSMS(context.Background(), tt.req)

			assertErrIs(t, err, tt.wantErrIs)
			if tt.wantNil && got != nil {
				t.Errorf("expected nil notification, got %+v", got)
			}
			if !tt.wantNil {
				if got == nil {
					t.Fatal("expected notification, got nil")
				}
				if got.Type != "sms" {
					t.Errorf("Type = %q, want sms", got.Type)
				}
			}
		})
	}
}

func TestListNotifications(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userID    string
		repo      *mockRepo
		wantErrIs error
		wantLen   int
	}{
		{
			name:    "valid user with results",
			userID:  "1",
			repo:    &mockRepo{listResult: []domain.Notification{{ID: "1"}, {ID: "2"}}},
			wantLen: 2,
		},
		{
			name:    "valid user nil result normalized to empty",
			userID:  "1",
			repo:    &mockRepo{listResult: nil},
			wantLen: 0,
		},
		{
			name:      "non-numeric user id",
			userID:    "abc",
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidUserID,
		},
		{
			name:      "zero user id",
			userID:    "0",
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidUserID,
		},
		{
			name:      "negative user id",
			userID:    "-5",
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidUserID,
		},
		{
			name:      "repository error",
			userID:    "1",
			repo:      &mockRepo{listErr: errRepo},
			wantErrIs: errRepo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := NewNotificationService(tt.repo)
			got, _, err := svc.ListNotifications(context.Background(), tt.userID, 20, 0)

			assertErrIs(t, err, tt.wantErrIs)
			if tt.wantErrIs == nil && len(got) != tt.wantLen {
				t.Errorf("len = %d, want %d", len(got), tt.wantLen)
			}
		})
	}
}

func TestGetNotification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		id        string
		userID    string
		repo      *mockRepo
		wantErrIs error
	}{
		{
			name:   "found",
			id:     "10",
			userID: "1",
			repo:   &mockRepo{findByID: &domain.Notification{ID: "10"}},
		},
		{
			name:      "invalid notification id",
			id:        "abc",
			userID:    "1",
			repo:      &mockRepo{},
			wantErrIs: ErrNotificationNotFound,
		},
		{
			name:      "invalid user id",
			id:        "10",
			userID:    "0",
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidUserID,
		},
		{
			name:      "not found returns sentinel",
			id:        "10",
			userID:    "1",
			repo:      &mockRepo{findByID: nil},
			wantErrIs: ErrNotificationNotFound,
		},
		{
			name:      "repository error",
			id:        "10",
			userID:    "1",
			repo:      &mockRepo{findByIDErr: errRepo},
			wantErrIs: errRepo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := NewNotificationService(tt.repo)
			got, err := svc.GetNotification(context.Background(), tt.id, tt.userID)

			assertErrIs(t, err, tt.wantErrIs)
			if tt.wantErrIs == nil && got == nil {
				t.Error("expected notification, got nil")
			}
		})
	}
}

func TestMarkAsRead(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		id        string
		userID    string
		repo      *mockRepo
		wantErrIs error
	}{
		{
			name:   "marked and refetched",
			id:     "10",
			userID: "1",
			repo:   &mockRepo{markUpdated: true, findByID: &domain.Notification{ID: "10", Read: true}},
		},
		{
			name:      "invalid notification id",
			id:        "abc",
			userID:    "1",
			repo:      &mockRepo{},
			wantErrIs: ErrNotificationNotFound,
		},
		{
			name:      "invalid user id",
			id:        "10",
			userID:    "x",
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidUserID,
		},
		{
			name:      "not updated returns not found",
			id:        "10",
			userID:    "1",
			repo:      &mockRepo{markUpdated: false},
			wantErrIs: ErrNotificationNotFound,
		},
		{
			name:      "repository error",
			id:        "10",
			userID:    "1",
			repo:      &mockRepo{markErr: errRepo},
			wantErrIs: errRepo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := NewNotificationService(tt.repo)
			got, err := svc.MarkAsRead(context.Background(), tt.id, tt.userID)

			assertErrIs(t, err, tt.wantErrIs)
			if tt.wantErrIs == nil && got == nil {
				t.Error("expected notification, got nil")
			}
		})
	}
}

func TestCountUnread(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		userID    string
		repo      *mockRepo
		wantErrIs error
		wantCount int
	}{
		{
			name:      "valid user",
			userID:    "1",
			repo:      &mockRepo{unreadCount: 7},
			wantCount: 7,
		},
		{
			name:      "empty user id",
			userID:    "",
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidUserID,
		},
		{
			name:      "non-numeric user id",
			userID:    "abc",
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidUserID,
		},
		{
			name:      "zero user id",
			userID:    "0",
			repo:      &mockRepo{},
			wantErrIs: ErrInvalidUserID,
		},
		{
			name:      "repository error",
			userID:    "1",
			repo:      &mockRepo{unreadErr: errRepo},
			wantErrIs: errRepo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			svc := NewNotificationService(tt.repo)
			got, err := svc.CountUnread(context.Background(), tt.userID)

			assertErrIs(t, err, tt.wantErrIs)
			if tt.wantErrIs == nil && got != tt.wantCount {
				t.Errorf("count = %d, want %d", got, tt.wantCount)
			}
		})
	}
}

// MarkAllAsRead delegates to the shared userScopedCount helper, whose user_id
// validation is exercised by TestCountUnread; these cases cover the delegation
// (marked count returned, repo error propagated) plus one invalid-id path.
func TestMarkAllAsRead(t *testing.T) {
	t.Parallel()

	t.Run("valid user returns marked count", func(t *testing.T) {
		t.Parallel()
		got, err := NewNotificationService(&mockRepo{markAllCount: 5}).MarkAllAsRead(context.Background(), "1")
		if err != nil || got != 5 {
			t.Fatalf("MarkAllAsRead = (%d, %v), want (5, nil)", got, err)
		}
	})

	t.Run("invalid user id is rejected", func(t *testing.T) {
		t.Parallel()
		_, err := NewNotificationService(&mockRepo{}).MarkAllAsRead(context.Background(), "0")
		assertErrIs(t, err, ErrInvalidUserID)
	})

	t.Run("repository error propagates", func(t *testing.T) {
		t.Parallel()
		_, err := NewNotificationService(&mockRepo{markAllErr: errRepo}).MarkAllAsRead(context.Background(), "1")
		assertErrIs(t, err, errRepo)
	})
}

// assertErrIs fails the test unless err matches the expectation: nil when want is
// nil, otherwise errors.Is(err, want).
func assertErrIs(t *testing.T, err, want error) {
	t.Helper()
	if want == nil {
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		return
	}
	if err == nil {
		t.Fatalf("expected error %v, got nil", want)
	}
	if !errors.Is(err, want) {
		t.Fatalf("error %v does not wrap %v", err, want)
	}
}
