package main

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func setupTestServer() (*Server, *OTPStore) {
	cfg := Config{
		HTTPPort:           3636,
		HTTPBind:           "127.0.0.1",
		APIToken:           "test_secret_token_123",
		OTPTTLSeconds:      300,
		OTPCooldownSeconds: 60,
		OTPMaxAttempts:     5,
	}

	otpStore := NewOTPStore(cfg.OTPTTLSeconds, cfg.OTPCooldownSeconds, cfg.OTPMaxAttempts)
	// waClient = nil in test -> dry run mode
	server := NewServer(cfg, otpStore, nil)
	return server, otpStore
}

func TestAuthMiddleware(t *testing.T) {
	server, _ := setupTestServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/send-otp", server.authMiddleware(server.handleSendOTP))

	reqBody := `{"uuid": "uuid-1", "phone": "628123456789"}`

	// 1. Missing Auth header -> 401
	req := httptest.NewRequest(http.MethodPost, "/send-otp", bytes.NewBufferString(reqBody))
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 2. Invalid Token -> 401
	req = httptest.NewRequest(http.MethodPost, "/send-otp", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer wrong_token")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 Unauthorized, got %d", rec.Code)
	}

	// 3. Valid Token -> 200
	req = httptest.NewRequest(http.MethodPost, "/send-otp", bytes.NewBufferString(reqBody))
	req.Header.Set("Authorization", "Bearer test_secret_token_123")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 OK, got %d. Body: %s", rec.Code, rec.Body.String())
	}
}

func TestSendAndVerifyOTPFlow(t *testing.T) {
	server, otpStore := setupTestServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/send-otp", server.authMiddleware(server.handleSendOTP))
	mux.HandleFunc("/verify-otp", server.authMiddleware(server.handleVerifyOTP))

	tokenHeader := "Bearer test_secret_token_123"
	uuid := "11111111-2222-3333-4444-555555555555"
	phone := "6281234567890"

	// 1. Invalid phone format -> 400
	sendBody, _ := json.Marshal(SendOTPRequest{UUID: uuid, Phone: "081234"})
	req := httptest.NewRequest(http.MethodPost, "/send-otp", bytes.NewReader(sendBody))
	req.Header.Set("Authorization", tokenHeader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid phone format, got %d", rec.Code)
	}

	// 2. Valid send-otp -> 200
	sendBody, _ = json.Marshal(SendOTPRequest{UUID: uuid, Phone: phone})
	req = httptest.NewRequest(http.MethodPost, "/send-otp", bytes.NewReader(sendBody))
	req.Header.Set("Authorization", tokenHeader)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for send-otp, got %d", rec.Code)
	}

	// 3. Immediate resend -> 429 Cooldown
	req = httptest.NewRequest(http.MethodPost, "/send-otp", bytes.NewReader(sendBody))
	req.Header.Set("Authorization", tokenHeader)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 for cooldown, got %d", rec.Code)
	}

	// Dapatkan code dari store untuk keperluan testing
	k := uuid + "|" + phone
	otpStore.mu.RLock()
	entry, exists := otpStore.store[k]
	otpStore.mu.RUnlock()
	if !exists {
		t.Fatalf("expected OTP entry to exist in store")
	}
	correctCode := entry.Code

	// 4. Verify with wrong OTP -> 400
	verifyBody, _ := json.Marshal(VerifyOTPRequest{UUID: uuid, Phone: phone, OTP: "999999"})
	req = httptest.NewRequest(http.MethodPost, "/verify-otp", bytes.NewReader(verifyBody))
	req.Header.Set("Authorization", tokenHeader)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for wrong OTP, got %d", rec.Code)
	}

	var resp APIResponse
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.AttemptsLeft != 4 {
		t.Fatalf("expected 4 attempts left, got %d", resp.AttemptsLeft)
	}

	// 5. Verify with correct OTP -> 200
	verifyBody, _ = json.Marshal(VerifyOTPRequest{UUID: uuid, Phone: phone, OTP: correctCode})
	req = httptest.NewRequest(http.MethodPost, "/verify-otp", bytes.NewReader(verifyBody))
	req.Header.Set("Authorization", tokenHeader)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for correct OTP, got %d", rec.Code)
	}

	// 6. Verify again after consumed -> 410 Gone (One-time use)
	req = httptest.NewRequest(http.MethodPost, "/verify-otp", bytes.NewReader(verifyBody))
	req.Header.Set("Authorization", tokenHeader)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone after OTP consumed, got %d", rec.Code)
	}
}

func TestMaxAttemptsExceeded(t *testing.T) {
	server, _ := setupTestServer()
	mux := http.NewServeMux()
	mux.HandleFunc("/send-otp", server.authMiddleware(server.handleSendOTP))
	mux.HandleFunc("/verify-otp", server.authMiddleware(server.handleVerifyOTP))

	tokenHeader := "Bearer test_secret_token_123"
	uuid := "uuid-max-attempts"
	phone := "628999999999"

	sendBody, _ := json.Marshal(SendOTPRequest{UUID: uuid, Phone: phone})
	req := httptest.NewRequest(http.MethodPost, "/send-otp", bytes.NewReader(sendBody))
	req.Header.Set("Authorization", tokenHeader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for send-otp, got %d", rec.Code)
	}

	// Coba salah 4 kali (Status 400)
	for i := 1; i <= 4; i++ {
		verifyBody, _ := json.Marshal(VerifyOTPRequest{UUID: uuid, Phone: phone, OTP: "000000"})
		req = httptest.NewRequest(http.MethodPost, "/verify-otp", bytes.NewReader(verifyBody))
		req.Header.Set("Authorization", tokenHeader)
		rec = httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("attempt %d: expected 400, got %d", i, rec.Code)
		}
	}

	// Percobaan ke-5 salah -> 429 Max attempts exceeded
	verifyBody, _ := json.Marshal(VerifyOTPRequest{UUID: uuid, Phone: phone, OTP: "000000"})
	req = httptest.NewRequest(http.MethodPost, "/verify-otp", bytes.NewReader(verifyBody))
	req.Header.Set("Authorization", tokenHeader)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusTooManyRequests {
		t.Fatalf("attempt 5: expected 429 Max Attempts Exceeded, got %d", rec.Code)
	}

	// Percobaan ke-6 -> 410 Gone (karena sudah dihapus)
	req = httptest.NewRequest(http.MethodPost, "/verify-otp", bytes.NewReader(verifyBody))
	req.Header.Set("Authorization", tokenHeader)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("attempt 6: expected 410 Gone, got %d", rec.Code)
	}
}

func TestOTPExpiration(t *testing.T) {
	cfg := Config{
		HTTPPort:           8080,
		HTTPBind:           "127.0.0.1",
		APIToken:           "test_secret_token_123",
		OTPTTLSeconds:      1, // 1 second TTL
		OTPCooldownSeconds: 0,
		OTPMaxAttempts:     5,
	}
	otpStore := NewOTPStore(cfg.OTPTTLSeconds, cfg.OTPCooldownSeconds, cfg.OTPMaxAttempts)
	server := NewServer(cfg, otpStore, nil)
	mux := http.NewServeMux()
	mux.HandleFunc("/send-otp", server.authMiddleware(server.handleSendOTP))
	mux.HandleFunc("/verify-otp", server.authMiddleware(server.handleVerifyOTP))

	tokenHeader := "Bearer test_secret_token_123"
	uuid := "uuid-expiry"
	phone := "628111111111"

	sendBody, _ := json.Marshal(SendOTPRequest{UUID: uuid, Phone: phone})
	req := httptest.NewRequest(http.MethodPost, "/send-otp", bytes.NewReader(sendBody))
	req.Header.Set("Authorization", tokenHeader)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200 for send-otp, got %d", rec.Code)
	}

	k := uuid + "|" + phone
	otpStore.mu.RLock()
	correctCode := otpStore.store[k].Code
	otpStore.mu.RUnlock()

	// Wait 1.1s for expiration
	time.Sleep(1100 * time.Millisecond)

	verifyBody, _ := json.Marshal(VerifyOTPRequest{UUID: uuid, Phone: phone, OTP: correctCode})
	req = httptest.NewRequest(http.MethodPost, "/verify-otp", bytes.NewReader(verifyBody))
	req.Header.Set("Authorization", tokenHeader)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusGone {
		t.Fatalf("expected 410 Gone for expired OTP, got %d", rec.Code)
	}
}

func TestTripayCallbackEndpoint(t *testing.T) {
	privateKey := "test-private-key-123"
	tripayClient := NewTripayClient("https://tripay.co.id/api-sandbox", "T1234", "test-api-key", privateKey, "QRIS")
	imMgr := NewImageMapOrderManager("test_upload", "test_transactions.json")

	// Daftarkan order pending
	order := &ImageMapOrder{
		PaymentID:     "MAP-WEBHOOK1",
		TransactionID: "DEV-T123456789",
		SenderPhone:   "628123456789",
		ChatJID:       "628123456789@s.whatsapp.net",
		MapName:       "testhook",
		Amount:        5000,
		Status:        "pending",
	}
	imMgr.RegisterAwaitingOrder(order)
	_ = imMgr.ApproveOrder("MAP-WEBHOOK1", "DEV-T123456789", 5000)

	waClient := &WAClient{
		tripayClient:    tripayClient,
		imagemapManager: imMgr,
	}

	cfg := Config{APIToken: "token123"}
	server := NewServer(cfg, nil, waClient)

	mux := http.NewServeMux()
	mux.HandleFunc("/api/tripay/callback", server.handleTripayCallback)

	// 1. GET method -> 405 Method Not Allowed
	req := httptest.NewRequest(http.MethodGet, "/api/tripay/callback", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405 Method Not Allowed for GET, got %d", rec.Code)
	}

	// 2. POST with invalid signature -> 400 Bad Request
	bodyJSON := `{"reference":"DEV-T123456789","merchant_ref":"MAP-WEBHOOK1","status":"PAID","total_amount":5000}`
	req = httptest.NewRequest(http.MethodPost, "/api/tripay/callback", bytes.NewBufferString(bodyJSON))
	req.Header.Set("X-Callback-Signature", "invalid_signature")
	req.Header.Set("X-Callback-Event", "payment_status")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("expected 400 Bad Request for invalid signature, got %d", rec.Code)
	}

	// 3. POST with valid signature & unhandled event -> 200 OK (unhandled event)
	h := hmac.New(sha256.New, []byte(privateKey))
	h.Write([]byte(bodyJSON))
	validSig := hex.EncodeToString(h.Sum(nil))

	req = httptest.NewRequest(http.MethodPost, "/api/tripay/callback", bytes.NewBufferString(bodyJSON))
	req.Header.Set("X-Callback-Signature", validSig)
	req.Header.Set("X-Callback-Event", "other_event")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for unhandled event, got %d", rec.Code)
	}

	// 4. POST with valid signature & payment_status = PAID -> 200 OK and order completed
	req = httptest.NewRequest(http.MethodPost, "/api/tripay/callback", bytes.NewBufferString(bodyJSON))
	req.Header.Set("X-Callback-Signature", validSig)
	req.Header.Set("X-Callback-Event", "payment_status")
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Errorf("expected 200 OK for valid PAID callback, got %d", rec.Code)
	}

	completed := imMgr.GetOrderByPaymentID("MAP-WEBHOOK1")
	if completed == nil || (completed.Status != "paid" && completed.Status != "waiting_mc_username") {
		t.Errorf("expected order to be paid or waiting_mc_username after webhook callback, got %+v", completed)
	}
}
