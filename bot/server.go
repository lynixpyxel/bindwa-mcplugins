package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"go.mau.fi/whatsmeow/types"
)

var phoneRegex = regexp.MustCompile(`^62\d{8,13}$`)

type Server struct {
	cfg       Config
	otpStore  *OTPStore
	waClient  *WAClient
	srv       *http.Server
	wsClients map[*websocket.Conn]bool
	wsMu      sync.RWMutex
	upgrader  websocket.Upgrader
}

type SendOTPRequest struct {
	UUID     string `json:"uuid"`
	Phone    string `json:"phone"`
	Username string `json:"username,omitempty"`
	Player   string `json:"player,omitempty"`
}

type VerifyOTPRequest struct {
	UUID     string `json:"uuid"`
	Phone    string `json:"phone"`
	OTP      string `json:"otp"`
	Username string `json:"username,omitempty"`
	Player   string `json:"player,omitempty"`
}

type SendGroupChatRequest struct {
	Player      string `json:"player"`
	Message     string `json:"message"`
	ReplyToID   string `json:"reply_to_id,omitempty"`
	ReplyGroup  string `json:"reply_group,omitempty"`
	ReplySender string `json:"reply_sender,omitempty"`
	QuotedText  string `json:"quoted_text,omitempty"`
}

type SendNotificationRequest struct {
	Title   string `json:"title"`
	Message string `json:"message"`
}

type APIResponse struct {
	Status       string `json:"status"`
	Message      string `json:"message,omitempty"`
	AttemptsLeft int    `json:"attempts_left,omitempty"`
}

func NewServer(cfg Config, otpStore *OTPStore, waClient *WAClient) *Server {
	s := &Server{
		cfg:       cfg,
		otpStore:  otpStore,
		waClient:  waClient,
		wsClients: make(map[*websocket.Conn]bool),
		upgrader: websocket.Upgrader{
			CheckOrigin: func(r *http.Request) bool {
				return true
			},
		},
	}

	if waClient != nil {
		waClient.SetMessageCallback(func(data WAMessageData) bool {
			return s.broadcastGroupChatToWS(data)
		})
		waClient.SetBroadcastCallback(func(payload interface{}) bool {
			return s.BroadcastJSONToWS(payload)
		})
	}

	return s
}

// BroadcastJSONToWS mem-broadcast objek JSON ke semua client WebSocket (Minecraft plugin).
func (s *Server) BroadcastJSONToWS(payload interface{}) bool {
	bytes, err := json.Marshal(payload)
	if err != nil {
		return false
	}

	s.wsMu.Lock()
	defer s.wsMu.Unlock()

	if len(s.wsClients) == 0 {
		return false
	}

	sentCount := 0
	for conn := range s.wsClients {
		if err := conn.WriteMessage(websocket.TextMessage, bytes); err != nil {
			_ = conn.Close()
			delete(s.wsClients, conn)
		} else {
			sentCount++
		}
	}

	return sentCount > 0
}

func (s *Server) broadcastGroupChatToWS(data WAMessageData) bool {
	payload := map[string]interface{}{
		"type":          "chat_wa",
		"msg_id":        data.MsgID,
		"group":         data.GroupJID,
		"group_name":    data.GroupName,
		"sender":        data.SenderPhone,
		"sender_jid":    data.SenderJID,
		"push_name":     data.PushName,
		"text":          data.Text,
		"quoted_author": data.QuotedAuthor,
		"quoted_text":   data.QuotedText,
		"server_name":   s.cfg.ServerName,
	}
	return s.BroadcastJSONToWS(payload)
}

func (s *Server) authMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		authHeader := r.Header.Get("Authorization")
		token := strings.TrimPrefix(authHeader, "Bearer ")
		if token == "" {
			token = r.URL.Query().Get("token")
		}

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

	if s.waClient != nil && !s.waClient.IsReady() {
		s.writeJSON(w, http.StatusBadGateway, APIResponse{Status: "error", Message: "whatsapp_service_unavailable"})
		return
	}

	username := strings.TrimSpace(req.Username)
	if username == "" {
		username = strings.TrimSpace(req.Player)
	}

	code, err := s.otpStore.Generate(req.UUID, req.Phone, username)
	if err != nil {
		if errors.Is(err, ErrCooldown) {
			s.writeJSON(w, http.StatusTooManyRequests, APIResponse{Status: "error", Message: "cooldown_active"})
			return
		}
		s.writeJSON(w, http.StatusInternalServerError, APIResponse{Status: "error", Message: "internal_error"})
		return
	}

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

	// Kirim notifikasi konfirmasi akun jika WhatsApp bot aktif
	if s.waClient != nil && s.waClient.IsReady() {
		userJID := types.NewJID(normalizePhone(req.Phone), types.DefaultUserServer)

		username := req.Username
		if username == "" {
			username = req.Player
		}
		if username == "" {
			username = res.Username
		}
		if username == "" {
			username = "Player"
		}
		bindTime := time.Now().Format("02 Jan 2006 15:04:05 WIB")

		msg := fmt.Sprintf("*VERIFIKASI BERHASIL*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"• Username MC: *%s*\n"+
			"• UUID: `%s`\n"+
			"• Nomor WA: *%s*\n"+
			"• Waktu Bind: *%s*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"Akun Minecraft dan nomor WhatsApp Anda kini telah resmi terhubung.",
			username, req.UUID, req.Phone, bindTime)

		_ = s.waClient.SendToJID(r.Context(), userJID, msg)

		// Kirim log audit ke grup logs jika ada yang di-link (.linkgroup logs)
		logMsg := fmt.Sprintf("*[LOG] VERIFIKASI AKUN*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"• Player: *%s*\n"+
			"• UUID: `%s`\n"+
			"• Nomor WA: *%s*\n"+
			"• Waktu: *%s*\n"+
			"• Status: *Berhasil Terhubung*\n"+
			"━━━━━━━━━━━━━━━━━━━━━",
			username, req.UUID, req.Phone, bindTime)
		s.waClient.SendToLogGroups(r.Context(), logMsg)
	}

	s.writeJSON(w, http.StatusOK, APIResponse{Status: "verified"})
}

func (s *Server) handleSendGroupChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Status: "error", Message: "method_not_allowed"})
		return
	}

	var req SendGroupChatRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, APIResponse{Status: "error", Message: "invalid_request_body"})
		return
	}

	if strings.TrimSpace(req.Message) == "" {
		s.writeJSON(w, http.StatusBadRequest, APIResponse{Status: "error", Message: "empty_message"})
		return
	}

	playerName := req.Player
	if playerName == "" {
		playerName = "Server"
	}

	waMsg := fmt.Sprintf("*|Server|* <%s>: %s", playerName, req.Message)
	if s.waClient != nil {
		var count int
		if req.ReplyGroup != "" {
			var gJID types.JID
			if strings.Contains(req.ReplyGroup, "@") {
				gJID, _ = types.ParseJID(req.ReplyGroup)
			} else {
				gJID = types.NewJID(req.ReplyGroup, types.GroupServer)
			}
			err := s.waClient.SendReplyToGroup(r.Context(), gJID, waMsg, req.ReplyToID, req.ReplySender, req.QuotedText)
			if err == nil {
				count = 1
			}
		} else if req.ReplyToID != "" {
			count = s.waClient.SendToGroupsWithReply(r.Context(), waMsg, req.ReplyToID, req.ReplySender, req.QuotedText)
		} else {
			count = s.waClient.SendToGroups(r.Context(), waMsg)
		}

		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "sent",
			"groups": count,
		})
	} else {
		fmt.Printf("[HTTP-Server] [DRY-RUN] Group chat: %s (ReplyTo: %s)\n", waMsg, req.ReplyToID)
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "sent", "dry_run": true})
	}
}

func (s *Server) handleSendNotification(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, APIResponse{Status: "error", Message: "method_not_allowed"})
		return
	}

	var req SendNotificationRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, APIResponse{Status: "error", Message: "invalid_request_body"})
		return
	}

	if s.waClient != nil {
		var count int
		if strings.Contains(strings.ToLower(req.Title), "leaderboard") {
			s.waClient.UpdateLeaderboardFromText(req.Message)
			count = s.waClient.BroadcastRichLeaderboard(r.Context())
		} else {
			formatted := fmt.Sprintf("*%s*\n%s", req.Title, req.Message)
			count = s.waClient.SendToGroups(r.Context(), formatted)
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{
			"status": "sent",
			"groups": count,
		})
	} else {
		fmt.Printf("[HTTP-Server] [DRY-RUN] Group notif: %s: %s\n", req.Title, req.Message)
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "sent", "dry_run": true})
	}
}

func (s *Server) handleServerStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var status ServerStatus
		if err := json.NewDecoder(r.Body).Decode(&status); err != nil {
			s.writeJSON(w, http.StatusBadRequest, APIResponse{Status: "error", Message: "invalid_request_body"})
			return
		}
		if s.waClient != nil {
			s.waClient.UpdateServerStatus(status)
		}
		s.writeJSON(w, http.StatusOK, map[string]interface{}{"status": "updated"})
		return
	}

	if s.waClient != nil {
		s.writeJSON(w, http.StatusOK, s.waClient.GetServerStatus())
	} else {
		s.writeJSON(w, http.StatusOK, ServerStatus{Online: false})
	}
}

func (s *Server) handleWS(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token != s.cfg.APIToken {
		authHeader := r.Header.Get("Authorization")
		token = strings.TrimPrefix(authHeader, "Bearer ")
	}

	if token != s.cfg.APIToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	conn, err := s.upgrader.Upgrade(w, r, nil)
	if err != nil {
		fmt.Printf("[WS-Server] Upgrade failed: %v\n", err)
		return
	}

	s.wsMu.Lock()
	s.wsClients[conn] = true
	s.wsMu.Unlock()
	fmt.Printf("[WS-Server] Minecraft plugin connected via WebSocket (%s)\n", conn.RemoteAddr())

	go func() {
		defer func() {
			s.wsMu.Lock()
			delete(s.wsClients, conn)
			s.wsMu.Unlock()
			_ = conn.Close()
			fmt.Println("[WS-Server] Minecraft plugin disconnected from WebSocket")
		}()

		for {
			_, msg, err := conn.ReadMessage()
			if err != nil {
				break
			}

			var payload map[string]interface{}
			if jsonErr := json.Unmarshal(msg, &payload); jsonErr == nil {
				msgType, _ := payload["type"].(string)
				switch msgType {
				case "status_heartbeat":
					var st ServerStatus
					rawBytes, _ := json.Marshal(payload)
					if unErr := json.Unmarshal(rawBytes, &st); unErr == nil {
						if s.waClient != nil {
							s.waClient.UpdateServerStatus(st)
						}
					}
				case "chat":
					player, _ := payload["player"].(string)
					text, _ := payload["message"].(string)
					replyToID, _ := payload["reply_to_id"].(string)
					replyGroup, _ := payload["reply_group"].(string)
					replySender, _ := payload["reply_sender"].(string)
					quotedText, _ := payload["quoted_text"].(string)

					if text != "" {
						waMsg := fmt.Sprintf("*|Server|* <%s>: %s", player, text)
						if s.waClient != nil {
							if replyGroup != "" {
								var gJID types.JID
								if strings.Contains(replyGroup, "@") {
									gJID, _ = types.ParseJID(replyGroup)
								} else {
									gJID = types.NewJID(replyGroup, types.GroupServer)
								}
								_ = s.waClient.SendReplyToGroup(context.Background(), gJID, waMsg, replyToID, replySender, quotedText)
							} else if replyToID != "" {
								_ = s.waClient.SendToGroupsWithReply(context.Background(), waMsg, replyToID, replySender, quotedText)
							} else {
								_ = s.waClient.SendToGroups(context.Background(), waMsg)
							}
						}
					}
				case "notif":
					title, _ := payload["title"].(string)
					text, _ := payload["message"].(string)
					if text != "" {
						if s.waClient != nil {
							if strings.Contains(strings.ToLower(title), "leaderboard") {
								s.waClient.UpdateLeaderboardFromText(text)
								s.waClient.BroadcastRichLeaderboard(context.Background())
							} else {
								formatted := fmt.Sprintf("*%s*\n%s", title, text)
								s.waClient.SendToGroups(context.Background(), formatted)
							}
						}
					}
				case "imagemap_order_status":
					orderID, _ := payload["order_id"].(string)
					bound, _ := payload["bound"].(bool)
					playerName, _ := payload["player_name"].(string)
					if s.waClient != nil && orderID != "" {
						s.waClient.HandleImageMapOrderStatusFromMC(orderID, bound, playerName)
					}
				}
			}
		}
	}()
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	waReady := s.waClient != nil && s.waClient.IsReady()
	var st ServerStatus
	if s.waClient != nil {
		st = s.waClient.GetServerStatus()
	}
	s.writeJSON(w, http.StatusOK, map[string]interface{}{
		"status":        "ok",
		"wa_ready":      waReady,
		"server_status": st,
		"time":          time.Now().Unix(),
	})
}

func (s *Server) Start() error {
	mux := http.NewServeMux()
	mux.HandleFunc("/send-otp", s.authMiddleware(s.handleSendOTP))
	mux.HandleFunc("/verify-otp", s.authMiddleware(s.handleVerifyOTP))
	mux.HandleFunc("/send-group-chat", s.authMiddleware(s.handleSendGroupChat))
	mux.HandleFunc("/send-notification", s.authMiddleware(s.handleSendNotification))
	mux.HandleFunc("/server-status", s.authMiddleware(s.handleServerStatus))
	mux.HandleFunc("/ws", s.handleWS)
	mux.HandleFunc("/health", s.handleHealth)

	// Static file server untuk melayani gambar ImageMap ke server Minecraft
	uploadDir := s.cfg.ImageMapUploadDir
	if uploadDir == "" {
		uploadDir = "upload/images"
	}
	_ = os.MkdirAll(uploadDir, 0755)
	mux.Handle("/images/", http.StripPrefix("/images/", http.FileServer(http.Dir(uploadDir))))

	addr := fmt.Sprintf("%s:%d", s.cfg.HTTPBind, s.cfg.HTTPPort)
	s.srv = &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
	}

	fmt.Printf("[HTTP-Server] Listening on http://%s\n", addr)
	if err := s.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("http server failed: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) error {
	s.wsMu.Lock()
	for conn := range s.wsClients {
		_ = conn.Close()
	}
	s.wsClients = make(map[*websocket.Conn]bool)
	s.wsMu.Unlock()

	if s.srv != nil {
		return s.srv.Shutdown(ctx)
	}
	return nil
}
