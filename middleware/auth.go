package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
)

// AuthUser represents the user info returned from auth service
type AuthUser struct {
	ID       string `json:"id"`
	Username string `json:"username"`
	Email    string `json:"email"`
}

// AuthClient handles communication with the auth service
type AuthClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAuthClient creates a new auth client
func NewAuthClient(baseURL string) *AuthClient {
	return &AuthClient{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Second,
		},
	}
}

// GetMe retrieves user info from auth service using the token
func (c *AuthClient) GetMe(ctx context.Context, token string) (*AuthUser, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/auth/v1/private/me", nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.httpClient.Do(req) //nolint:gosec
	if err != nil {
		return nil, fmt.Errorf("request auth service: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("invalid or expired token")
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("auth service error: %d - %s", resp.StatusCode, string(body))
	}

	var user AuthUser
	if err := json.NewDecoder(resp.Body).Decode(&user); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}

	return &user, nil
}

// AuthMiddleware creates a middleware that validates tokens via auth service
// It sets "user_id" in the gin context if authentication succeeds
func AuthMiddleware(authClient *AuthClient) gin.HandlerFunc {
	return func(c *gin.Context) {
		// Get token from Authorization header
		authHeader := c.GetHeader("Authorization")
		if authHeader == "" {
			// No token provided - allow request with default user_id for demo compatibility
			// In production, you'd return 401 here
			c.Set("user_id", "1")
			c.Next()
			return
		}

		// Extract token from "Bearer <token>"
		const bearerPrefix = "Bearer "
		if len(authHeader) <= len(bearerPrefix) || authHeader[:len(bearerPrefix)] != bearerPrefix {
			logger := GetLoggerFromGinContext(c)
			logger.Warn("Malformed Authorization header", zap.String("header", authHeader))
			c.Set("user_id", "1")
			c.Next()
			return
		}
		token := authHeader[len(bearerPrefix):]

		// Call auth service to validate token
		user, err := authClient.GetMe(c.Request.Context(), token)
		if err != nil {
			logger := GetLoggerFromGinContext(c)
			logger.Warn("Auth validation failed", zap.Error(err))

			// For demo compatibility, fall back to default user_id
			// In production: c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid token"})
			c.Set("user_id", "1")
			c.Next()
			return
		}

		// Set user_id in context for handlers to use
		c.Set("user_id", user.ID)
		c.Set("username", user.Username)
		c.Next()
	}
}
