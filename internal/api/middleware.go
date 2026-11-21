package api

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/swe-labs/railbridge/internal/crypto"
	"github.com/swe-labs/railbridge/internal/models"
	"github.com/swe-labs/railbridge/internal/store"
)

type contextKey string

const (
	tenantContextKey contextKey = "tenant"
	deviceContextKey contextKey = "device"
)

// TenantFromContext extracts the authenticated tenant from the request context.
func TenantFromContext(ctx context.Context) (*models.Tenant, bool) {
	t, ok := ctx.Value(tenantContextKey).(*models.Tenant)
	return t, ok
}

// DeviceFromContext extracts the authenticated Android bridge device from the request context.
func DeviceFromContext(ctx context.Context) (*models.Device, bool) {
	d, ok := ctx.Value(deviceContextKey).(*models.Device)
	return d, ok
}

// TenantAuthMiddleware authenticates requests using the Bearer rb_live_... or rb_test_... API key.
func (s *Server) TenantAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing_authorization", "Authorization header is required")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeError(w, http.StatusUnauthorized, "invalid_authorization_format", "Authorization header must be Bearer <API_KEY>")
			return
		}

		apiKey := strings.TrimSpace(parts[1])
		if !strings.HasPrefix(apiKey, "rb_live_") && !strings.HasPrefix(apiKey, "rb_test_") {
			writeError(w, http.StatusUnauthorized, "invalid_api_key_prefix", "API key must begin with rb_live_ or rb_test_")
			return
		}

		keyHash := crypto.HashSHA256([]byte(apiKey))
		tenant, err := s.Store.GetTenantByAPIKeyHash(r.Context(), keyHash)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid_api_key", "Provided API key is invalid or deactivated")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "auth_error", "Failed to verify credentials: "+err.Error())
			return
		}

		ctx := context.WithValue(r.Context(), tenantContextKey, tenant)
		next(w, r.WithContext(ctx))
	}
}

// DeviceAuthMiddleware authenticates Android SIM Bridge connections using Bearer rb_dev_...
func (s *Server) DeviceAuthMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			writeError(w, http.StatusUnauthorized, "missing_device_token", "Device authorization token is required")
			return
		}

		parts := strings.SplitN(authHeader, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			writeError(w, http.StatusUnauthorized, "invalid_token_format", "Authorization must be Bearer <DEVICE_TOKEN>")
			return
		}

		token := strings.TrimSpace(parts[1])
		tokenHash := crypto.HashSHA256([]byte(token))

		device, err := s.Store.GetDeviceByTokenHash(r.Context(), tokenHash)
		if errors.Is(err, store.ErrNotFound) {
			writeError(w, http.StatusUnauthorized, "invalid_device_token", "Unrecognized or revoked bridge device token")
			return
		}
		if err != nil {
			writeError(w, http.StatusInternalServerError, "device_auth_error", "Database error verifying device")
			return
		}

		ctx := context.WithValue(r.Context(), deviceContextKey, device)
		next(w, r.WithContext(ctx))
	}
}

func writeError(w http.ResponseWriter, status int, errorType, detail string) {
	writeJSON(w, status, map[string]any{
		"type":   "https://railbridge.alwaysdata.net/errors/" + errorType,
		"title":  http.StatusText(status),
		"status": status,
		"detail": detail,
	})
}
