package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/duynhlab/notification-service/internal/core/domain"
	logicv1 "github.com/duynhlab/notification-service/internal/logic/v1"
	"github.com/gin-gonic/gin"
)

func init() { gin.SetMode(gin.TestMode) }

// mockRepo is a configurable domain.NotificationRepository double for web tests.
type mockRepo struct {
	createErr error

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

func (m *mockRepo) Create(_ context.Context, _ *domain.Notification, _ int) error {
	return m.createErr
}
func (m *mockRepo) CreateWithDeliveryKey(_ context.Context, _ *domain.Notification, _ int, _ string) (bool, error) {
	return false, m.createErr
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

func newHandler(repo domain.NotificationRepository) *Handler {
	return NewHandler(logicv1.NewNotificationService(repo))
}

func newCtx(method, target, userID string, params gin.Params) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, nil)
	if userID != "" {
		c.Set("user_id", userID)
	}
	c.Params = params
	return c, rec
}

func ctxWithBody(method, target, userID, body string) (*gin.Context, *httptest.ResponseRecorder) {
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(method, target, strings.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if userID != "" {
		c.Set("user_id", userID)
	}
	return c, rec
}

// decode returns the parsed JSON body.
func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("invalid JSON body %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestListNotifications_Success(t *testing.T) {
	repo := &mockRepo{listResult: []domain.Notification{{ID: "1"}, {ID: "2"}}, totalCount: 2}
	c, rec := newCtx(http.MethodGet, "/notification/v1/private/notifications?page=1&page_size=5", "1", nil)

	newHandler(repo).ListNotifications(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := decode(t, rec)
	if body["total_items"].(float64) != 2 {
		t.Errorf("total_items = %v, want 2", body["total_items"])
	}
	if items, ok := body["items"].([]any); !ok || len(items) != 2 {
		t.Errorf("items = %v, want length 2", body["items"])
	}
}

func TestListNotifications_Unauthorized(t *testing.T) {
	c, rec := newCtx(http.MethodGet, "/notification/v1/private/notifications", "", nil)
	newHandler(&mockRepo{}).ListNotifications(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "UNAUTHORIZED" {
		t.Errorf("code = %v, want UNAUTHORIZED", code)
	}
}

func TestListNotifications_ServiceError(t *testing.T) {
	repo := &mockRepo{totalErr: context.DeadlineExceeded}
	c, rec := newCtx(http.MethodGet, "/notification/v1/private/notifications", "1", nil)
	newHandler(repo).ListNotifications(c)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "INTERNAL_ERROR" {
		t.Errorf("code = %v, want INTERNAL_ERROR", code)
	}
}

func TestGetUnreadCount_Success(t *testing.T) {
	repo := &mockRepo{unreadCount: 7}
	c, rec := newCtx(http.MethodGet, "/notification/v1/private/notifications/count", "1", nil)
	newHandler(repo).GetUnreadCount(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := decode(t, rec); body["count"].(float64) != 7 {
		t.Errorf("count = %v, want 7", body["count"])
	}
}

func TestGetUnreadCount_Unauthorized(t *testing.T) {
	c, rec := newCtx(http.MethodGet, "/notification/v1/private/notifications/count", "", nil)
	newHandler(&mockRepo{}).GetUnreadCount(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "UNAUTHORIZED" {
		t.Errorf("code = %v, want UNAUTHORIZED", code)
	}
}

func TestGetUnreadCount_ServiceError(t *testing.T) {
	repo := &mockRepo{unreadErr: context.DeadlineExceeded}
	c, rec := newCtx(http.MethodGet, "/notification/v1/private/notifications/count", "1", nil)
	newHandler(repo).GetUnreadCount(c)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestGetNotification_Success(t *testing.T) {
	repo := &mockRepo{findByID: &domain.Notification{ID: "10"}}
	c, rec := newCtx(http.MethodGet, "/notification/v1/private/notifications/10", "1", gin.Params{{Key: "id", Value: "10"}})
	newHandler(repo).GetNotification(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestGetNotification_NotFound(t *testing.T) {
	repo := &mockRepo{findByID: nil}
	c, rec := newCtx(http.MethodGet, "/notification/v1/private/notifications/9", "1", gin.Params{{Key: "id", Value: "9"}})
	newHandler(repo).GetNotification(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", code)
	}
}

func TestGetNotification_Unauthorized(t *testing.T) {
	c, rec := newCtx(http.MethodGet, "/notification/v1/private/notifications/10", "", gin.Params{{Key: "id", Value: "10"}})
	newHandler(&mockRepo{}).GetNotification(c)

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestMarkAsRead_Success(t *testing.T) {
	repo := &mockRepo{markUpdated: true, findByID: &domain.Notification{ID: "10", Read: true}}
	c, rec := newCtx(http.MethodPatch, "/notification/v1/private/notifications/10", "1", gin.Params{{Key: "id", Value: "10"}})
	newHandler(repo).MarkAsRead(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
}

func TestMarkAsRead_NotFound(t *testing.T) {
	repo := &mockRepo{markUpdated: false}
	c, rec := newCtx(http.MethodPatch, "/notification/v1/private/notifications/9", "1", gin.Params{{Key: "id", Value: "9"}})
	newHandler(repo).MarkAsRead(c)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "NOT_FOUND" {
		t.Errorf("code = %v, want NOT_FOUND", code)
	}
}

// MarkAllAsRead and GetUnreadCount share the respondUserScopedCount helper whose
// auth (401) and service-error (500) branches are covered by the GetUnreadCount
// tests; this only needs the success/delegation path.
func TestMarkAllAsRead_Success(t *testing.T) {
	repo := &mockRepo{markAllCount: 5}
	c, rec := newCtx(http.MethodPatch, "/notification/v1/private/notifications/read-all", "1", nil)
	newHandler(repo).MarkAllAsRead(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := decode(t, rec); body["updated"].(float64) != 5 {
		t.Errorf("updated = %v, want 5", body["updated"])
	}
}

func TestSendEmail_Success(t *testing.T) {
	c, rec := ctxWithBody(http.MethodPost, "/notification/v1/internal/email", "",
		`{"user_id":1,"to":"a@b.com","subject":"Hi","body":"Body"}`)
	newHandler(&mockRepo{}).SendEmail(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := decode(t, rec); body["type"] != "email" {
		t.Errorf("type = %v, want email", body["type"])
	}
}

func TestSendEmail_InvalidBody(t *testing.T) {
	c, rec := ctxWithBody(http.MethodPost, "/notification/v1/internal/email", "", "{")
	newHandler(&mockRepo{}).SendEmail(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", code)
	}
}

func TestSendEmail_InvalidRecipient(t *testing.T) {
	// Missing/invalid email fails request binding (binding:"required,email").
	c, rec := ctxWithBody(http.MethodPost, "/notification/v1/internal/email", "",
		`{"user_id":1,"to":"not-an-email","subject":"Hi","body":"Body"}`)
	newHandler(&mockRepo{}).SendEmail(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", code)
	}
}

func TestSendEmail_ServiceError(t *testing.T) {
	c, rec := ctxWithBody(http.MethodPost, "/notification/v1/internal/email", "",
		`{"user_id":1,"to":"a@b.com","subject":"Hi","body":"Body"}`)
	newHandler(&mockRepo{createErr: context.DeadlineExceeded}).SendEmail(c)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}

func TestSendSMS_Success(t *testing.T) {
	c, rec := ctxWithBody(http.MethodPost, "/notification/v1/internal/sms", "",
		`{"user_id":1,"to":"+15551234","message":"hello"}`)
	newHandler(&mockRepo{}).SendSMS(c)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if body := decode(t, rec); body["type"] != "sms" {
		t.Errorf("type = %v, want sms", body["type"])
	}
}

func TestSendSMS_InvalidBody(t *testing.T) {
	c, rec := ctxWithBody(http.MethodPost, "/notification/v1/internal/sms", "", "{")
	newHandler(&mockRepo{}).SendSMS(c)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
	if code := decode(t, rec)["code"]; code != "VALIDATION_ERROR" {
		t.Errorf("code = %v, want VALIDATION_ERROR", code)
	}
}

func TestSendSMS_ServiceError(t *testing.T) {
	c, rec := ctxWithBody(http.MethodPost, "/notification/v1/internal/sms", "",
		`{"user_id":1,"to":"+15551234","message":"hello"}`)
	newHandler(&mockRepo{createErr: context.DeadlineExceeded}).SendSMS(c)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
}
