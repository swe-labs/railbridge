package api

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/swe-labs/railbridge/internal/config"
	"github.com/swe-labs/railbridge/internal/email"
	"github.com/swe-labs/railbridge/internal/sms"
	"github.com/swe-labs/railbridge/internal/store"
)

// Server encapsulates HTTP routing, middleware, and dependency references.
type Server struct {
	Config       *config.Config
	Store        *store.DB
	Mux          *http.ServeMux
	SMSService   *sms.Service
	EmailService *email.Service
}

// NewServer constructs the HTTP server and registers core middleware and routes.
func NewServer(cfg *config.Config, st *store.DB, smsSvc *sms.Service, emailSvc *email.Service) *Server {
	if smsSvc == nil {
		primary := sms.NewGreenwebDriver(cfg.GreenwebAPIToken)
		fallback := &sms.MockDriver{DriverName: "android_sim_bridge"}
		smsSvc = sms.NewService(st, primary, fallback)
	}
	if emailSvc == nil {
		var emailDriver email.Driver
		if cfg.Env != "development" && cfg.EmailDriver == "smtp" && cfg.SMTPHost != "" {
			emailDriver = email.NewSMTPDriver(cfg.SMTPHost, cfg.SMTPPort, cfg.SMTPUser, cfg.SMTPPass, cfg.SMTPUser)
		} else {
			// Local development / sandbox mode uses in-memory mock driver
			emailDriver = &email.MockEmailDriver{}
		}
		emailSvc = email.NewService(st, emailDriver)
	}

	s := &Server{
		Config:       cfg,
		Store:        st,
		Mux:          http.NewServeMux(),
		SMSService:   smsSvc,
		EmailService: emailSvc,
	}
	s.registerRoutes()
	return s
}

// Handler returns the root http.Handler with production middleware applied.
func (s *Server) Handler() http.Handler {
	return s.recoverMiddleware(s.loggingMiddleware(s.Mux))
}

func (s *Server) registerRoutes() {
	// Probes (Liveness and Database Readiness)
	s.Mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.Mux.HandleFunc("GET /readyz", s.handleReadyz)

	// API v1 General Info
	s.Mux.HandleFunc("GET /v1/info", s.handleInfo)

	// Payment Rail Endpoints (Tenant Scoped)
	s.Mux.HandleFunc("POST /v1/payments/sessions", s.TenantAuthMiddleware(s.handleCreatePaymentSession))
	s.Mux.HandleFunc("GET /v1/payments/sessions/{id}", s.TenantAuthMiddleware(s.handleGetPaymentSession))
	s.Mux.HandleFunc("POST /v1/payments/sessions/{id}/verify", s.TenantAuthMiddleware(s.handleVerifyPaymentSession))

	// SMS Rail Endpoints (Tenant Scoped)
	s.Mux.HandleFunc("POST /v1/sms/messages", s.TenantAuthMiddleware(s.handleSendSMS))

	// Email Rail Endpoints
	s.Mux.HandleFunc("POST /v1/emails/messages", s.TenantAuthMiddleware(s.handleSendEmail))
	s.Mux.HandleFunc("POST /v1/emails/aliases", s.TenantAuthMiddleware(s.handleCreateEmailAlias))
	s.Mux.HandleFunc("POST /v1/emails/inbound", s.handleInboundEmail)

	// Android SIM Bridge Ingestion Endpoints (Device Scoped)
	s.Mux.HandleFunc("POST /v1/ingest/android-ping", s.DeviceAuthMiddleware(s.handleAndroidPing))
	s.Mux.HandleFunc("POST /v1/ingest/android-sms", s.DeviceAuthMiddleware(s.handleIngestAndroidSMS))

	// Hosted Checkout Multilingual UI
	s.Mux.HandleFunc("GET /checkout/{id}", s.handleHostedCheckout)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"status":  "pass",
		"version": "v1.0.0",
		"env":     s.Config.Env,
		"time":    time.Now().UTC().Format(time.RFC3339),
	})
}

func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	if err := s.Store.Ready(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]any{
			"status":   "fail",
			"database": "unreachable",
			"error":    err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":   "ready",
		"database": "connected",
		"dialect":  s.Store.Dialect,
	})
}

func (s *Server) handleInfo(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"name":        "Railbridge",
		"version":     "v1.0.0",
		"go_version":  "1.27.4",
		"environment": s.Config.Env,
		"docs":        "https://github.com/swe-labs/railbridge",
	})
}

// Middleware: Logging
func (s *Server) loggingMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriterWrapper{ResponseWriter: w, statusCode: http.StatusOK}

		// Enforce standard input ceiling of 1MB on requests to prevent memory exhaustion
		r.Body = http.MaxBytesReader(w, r.Body, 1048576)

		next.ServeHTTP(rw, r)

		slog.Info("HTTP Request",
			"method", r.Method,
			"path", r.URL.Path,
			"status", rw.statusCode,
			"duration_ms", time.Since(start).Milliseconds(),
			"remote_addr", r.RemoteAddr,
		)
	})
}

// Middleware: Panic Recovery
func (s *Server) recoverMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				slog.Error("Unhandled Panic Recovered",
					"error", fmt.Sprintf("%v", rec),
					"stack", string(debug.Stack()),
				)
				writeJSON(w, http.StatusInternalServerError, map[string]string{
					"type":   "https://railbridge.alwaysdata.net/errors/internal",
					"title":  "Internal Server Error",
					"detail": "An unexpected error occurred. This failure has been logged.",
				})
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

type responseWriterWrapper struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriterWrapper) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
