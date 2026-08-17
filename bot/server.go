package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"
)

var phoneRegex = regexp.MustCompile(`^62\d{8,13}$`)

type Server struct {
	cfg      Config
	otpStore *OTPStore
	waClient *WAClient
	srv      *http.Server
}

type SendOTPRequest struct {
	UUID  string `json:"uuid"`
	Phone string `json:"phone"`
}

type VerifyOTPRequest struct {
	UUID  string `json:"uuid"`
	Phone string `json:"phone"`
	OTP   string `json:"otp"`
}

type APIResponse struct {
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
	AttemptsLeft int    `json:"attempts_left,omitempty"`
}

func NewServer(cfg Config, otpStore *OTPStore, waClient *WAClient) *Server {
	return &Server{
		cfg:      cfg,
		otpStore: otpStore,
		waClient: waClient,
	}
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		if !strings.HasPrefix(authHeader, "Bearer ") {
			s.writeJSON(w, http.StatusUnauthorized, APIResponse{Status: "error", Message: "unauthorized"})
			return
		}

		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token != s.cfg.APIToken {
			s.writeJSON(w, http.StatusUnauthorized, APIResponse{Status: "error", Message: "unauthorized"})
			return
		}

		next(w, r)
	}
}

func (s *Server) writeJSON(w http.ResponseWriter, statusCode int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	_ = json.NewEncoder(w).Encode(data)
}

func (s *Server) handleSendOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Status: "error", Message: "method_not_allowed"})
		return
	}

	var req SendOTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, APIResponse{Status: "error", Message: "invalid_request_body"})
		return
	}

	req.UUID = strings.TrimSpace(req.UUID)
	req.Phone = strings.TrimSpace(req.Phone)

	if req.UUID == "" || !phoneRegex.MatchString(req.Phone) {
		s.writeJSON(w, http.StatusBadRequest, APIResponse{Status: "error", Message: "invalid_phone_format"})
		return
	}

	// Cek koneksi WA sebelum generate OTP
	if s.waClient != nil && !s.waClient.IsReady() {
		s.writeJSON(w, http.StatusBadGateway, APIResponse{Status: "error", Message: "whatsapp_service_unavailable"})
		return
	}

	// Generate OTP
	code, err := s.otpStore.Generate(req.UUID, req.Phone)
	if err != nil {
		if errors.Is(err, ErrCooldown) {
			s.writeJSON(w, http.StatusTooManyRequests, APIResponse{Status: "error", Message: "cooldown_active"})
			return
		}
		s.writeJSON(w, http.StatusInternalServerError, APIResponse{Status: "error", Message: "internal_error"})
		return
	}

	// Kirim pesan WhatsApp
	message := fmt.Sprintf(
		"Kode verifikasi WhatsApp akun Minecraft Anda adalah: *%s*\n\nKode ini berlaku selama %d menit. JANGAN berikan kode ini kepada siapapun!",
		code,
		s.cfg.OTPTTLSeconds/60,
	)

	if s.waClient != nil {
		sendErr := s.waClient.SendMessage(r.Context(), req.Phone, message)
		if sendErr != nil {
			fmt.Printf("[HTTP-Server] Failed to send WhatsApp message to %s: %v\n", req.Phone, sendErr)
			s.writeJSON(w, http.StatusBadGateway, APIResponse{Status: "error", Message: "failed_to_send_whatsapp"})
			return
		}
	} else {
		// Mock/dry-run mode if waClient is nil (e.g. during testing)
		fmt.Printf("[HTTP-Server] [DRY-RUN] OTP generated for %s (%s): %s\n", req.UUID, req.Phone, code)
	}

	s.writeJSON(w, http.StatusOK, APIResponse{Status: "sent"})
}

func (s *Server) handleVerifyOTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Status: "error", Message: "method_not_allowed"})
		return
	}

	var req VerifyOTPRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, APIResponse{Status: "error", Message: "invalid_request_body"})
		return
	}

	req.UUID = strings.TrimSpace(req.UUID)
	req.Phone = strings.TrimSpace(req.Phone)
	req.OTP = strings.TrimSpace(req.OTP)

	if req.UUID == "" || req.Phone == "" || req.OTP == "" {
		s.writeJSON(w, http.StatusBadRequest, APIResponse{Status: "error", Message: "missing_required_fields"})
		return
	}

	res, err := s.otpStore.Verify(req.UUID, req.Phone, req.OTP)
	if err != nil {
		if errors.Is(err, ErrOTPNotFoundOrExpired) {
			s.writeJSON(w, http.StatusGone, APIResponse{Status: "error", Message: "otp_expired_or_not_found"})
			return
		}
		if errors.Is(err, ErrMaxAttemptsExceeded) {
			s.writeJSON(w, http.StatusTooManyRequests, APIResponse{
				Status:       "error",
				Message:      "max_attempts_exceeded",
				AttemptsLeft: 0,
			})
			return
		}
		if errors.Is(err, ErrWrongOTP) {
			s.writeJSON(w, http.StatusBadRequest, APIResponse{
				Status:       "error",
				Message:      "wrong_otp",
				AttemptsLeft: res.AttemptsLeft,
			})
			return
		}
		s.writeJSON(w, http.StatusInternalServerError, APIResponse{Status: "error", Message: "internal_error"})
		return
	}

	s.writeJSON(w, http.StatusOK, APIResponse{Status: "verified"})
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	waReady := s.waClient != nil && s.waClient.IsReady()
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":   "ok",
		"wa_ready": waReady,
		"time":     time.Now().Unix(),
	})
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/send-otp", s.authMiddleware(s.handleSendOTP))
	mux.HandleFunc("/verify-otp", s.authMiddleware(s.handleVerifyOTP))
	mux.HandleFunc("/health", s.handleHealth)

	addr := fmt.Sprintf("%s:%d", s.cfg.HTTPBind, s.cfg.HTTPPort)
	s.srv = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	fmt.Printf("[HTTP-Server] Listening on http://%s\n", addr)
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	if s.srv != nil {
		return s.srv.Shutdown(ctx)
	}
	return nil
}
