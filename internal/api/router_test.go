package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/swe-labs/railbridge/internal/config"
	"github.com/swe-labs/railbridge/internal/store"
)

func TestAPI_HealthAndReady(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_api.db")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("config load: %v", err)
	}

	st, err := store.Open(ctx, "sqlite://"+dbPath)
	if err != nil {
		t.Fatalf("store open: %v", err)
	}
	defer st.Close()

	server := NewServer(cfg, st, nil, nil)
	handler := server.Handler()

	// 1. Test /healthz
	reqHealth := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rrHealth := httptest.NewRecorder()
	handler.ServeHTTP(rrHealth, reqHealth)

	if rrHealth.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /healthz, got %d", rrHealth.Code)
	}

	var healthResp map[string]any
	if err := json.Unmarshal(rrHealth.Body.Bytes(), &healthResp); err != nil {
		t.Fatalf("failed to decode /healthz JSON: %v", err)
	}
	if healthResp["status"] != "pass" {
		t.Errorf("expected health status 'pass', got %v", healthResp["status"])
	}

	// 2. Test /readyz
	reqReady := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rrReady := httptest.NewRecorder()
	handler.ServeHTTP(rrReady, reqReady)

	if rrReady.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /readyz, got %d", rrReady.Code)
	}

	var readyResp map[string]any
	if err := json.Unmarshal(rrReady.Body.Bytes(), &readyResp); err != nil {
		t.Fatalf("failed to decode /readyz JSON: %v", err)
	}
	if readyResp["status"] != "ready" || readyResp["database"] != "connected" {
		t.Errorf("unexpected readyz payload: %v", readyResp)
	}

	// 3. Test Root / Portal HTML
	reqRoot := httptest.NewRequest(http.MethodGet, "/", nil)
	rrRoot := httptest.NewRecorder()
	handler.ServeHTTP(rrRoot, reqRoot)

	if rrRoot.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from root /, got %d", rrRoot.Code)
	}
	if ct := rrRoot.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("expected HTML Content-Type, got %s", ct)
	}
	bodyStr := rrRoot.Body.String()
	if len(bodyStr) < 200 {
		t.Errorf("expected portal HTML content, got short body: %s", bodyStr)
	}

	// 4. Test /docs Portal
	reqDocs := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rrDocs := httptest.NewRecorder()
	handler.ServeHTTP(rrDocs, reqDocs)

	if rrDocs.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /docs, got %d", rrDocs.Code)
	}
	if ct := rrDocs.Header().Get("Content-Type"); ct != "text/html; charset=utf-8" {
		t.Errorf("expected HTML Content-Type for /docs, got %s", ct)
	}

	// 5. Test /static/datastar.js
	reqJS := httptest.NewRequest(http.MethodGet, "/static/datastar.js", nil)
	rrJS := httptest.NewRecorder()
	handler.ServeHTTP(rrJS, reqJS)

	if rrJS.Code != http.StatusOK {
		t.Fatalf("expected 200 OK from /static/datastar.js, got %d", rrJS.Code)
	}
	if ct := rrJS.Header().Get("Content-Type"); ct != "application/javascript; charset=utf-8" {
		t.Errorf("expected JS Content-Type for /static/datastar.js, got %s", ct)
	}
}
