package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/mdp/qrterminal/v3"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/store/sqlstore"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	waLog "go.mau.fi/whatsmeow/util/log"
	"google.golang.org/protobuf/proto"

	_ "modernc.org/sqlite"
)

var (
	ErrWANotConnected = errors.New("whatsapp client is not connected")
	ErrWANotLoggedIn  = errors.New("whatsapp client is not logged in")
)

type ServerStatus struct {
	Online      bool      `json:"online"`
	PlayerCount int       `json:"player_count"`
	MaxPlayers  int       `json:"max_players"`
	PlayerList  []string  `json:"player_list"`
	TPS         float64   `json:"tps"`
	LastUpdated time.Time `json:"last_updated"`
}

type WAMessageCallback func(groupJid, sender, pushName, text string)

type WAClient struct {
	client          *whatsmeow.Client
	container       *sqlstore.Container
	config          Config
	mu              sync.RWMutex
	isLoggedIn      bool
	serverStatus    ServerStatus
	messageCallback WAMessageCallback
}

func NewWAClient(ctx context.Context, dbPath string, cfg Config) (*WAClient, error) {
	log := waLog.Stdout("WA-Client", "INFO", true)
	dbURI := fmt.Sprintf("file:%s?_foreign_keys=on", dbPath)
	container, err := sqlstore.New(ctx, "sqlite", dbURI, log)
	if err != nil {
		return nil, fmt.Errorf("failed to open session store: %w", err)
	}

	deviceStore, err := container.GetFirstDevice(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get first device: %w", err)
	}

	client := whatsmeow.NewClient(deviceStore, log)

	w := &WAClient{
		client:    client,
		container: container,
		config:    cfg,
	}

	client.AddEventHandler(w.eventHandler)

	return w, nil
}

func (w *WAClient) SetMessageCallback(cb WAMessageCallback) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messageCallback = cb
}

func (w *WAClient) UpdateServerStatus(status ServerStatus) {
	w.mu.Lock()
	defer w.mu.Unlock()
	status.Online = true
	status.LastUpdated = time.Now()
	w.serverStatus = status
}

func (w *WAClient) GetServerStatus() ServerStatus {
	w.mu.RLock()
	defer w.mu.RUnlock()
	status := w.serverStatus
	if time.Since(status.LastUpdated) > 35*time.Second {
		status.Online = false
	}
	return status
}

func (w *WAClient) eventHandler(rawEvt interface{}) {
	switch evt := rawEvt.(type) {
	case *events.LoggedOut:
		w.mu.Lock()
		w.isLoggedIn = false
		w.mu.Unlock()
		fmt.Println("[WA-Client] Logged out from WhatsApp. Re-scan QR to login.")
	case *events.Connected:
		w.mu.Lock()
		w.isLoggedIn = true
		w.mu.Unlock()
		fmt.Println("[WA-Client] Connected and authenticated successfully to WhatsApp.")
	case *events.Disconnected:
		fmt.Println("[WA-Client] Disconnected from WhatsApp network. Reconnecting...")
	case *events.StreamReplaced:
		fmt.Println("[WA-Client] Stream replaced. Session might be open elsewhere.")
	case *events.Message:
		w.handleIncomingMessage(evt)
	}
}

func (w *WAClient) parseCommand(text string) (prefix string, cmd string, args string, isCmd bool) {
	trimmed := strings.TrimSpace(text)
	prefixes := w.config.CommandPrefixes
	if len(prefixes) == 0 {
		prefixes = []string{".", "!", "#", "?"}
	}

	for _, p := range prefixes {
		if strings.HasPrefix(trimmed, p) {
			withoutPrefix := strings.TrimSpace(strings.TrimPrefix(trimmed, p))
			parts := strings.SplitN(withoutPrefix, " ", 2)
			cmdName := strings.ToLower(parts[0])
			argStr := ""
			if len(parts) > 1 {
				argStr = strings.TrimSpace(parts[1])
			}
			return p, cmdName, argStr, true
		}
	}
	return "", "", "", false
}

func (w *WAClient) handleIncomingMessage(evt *events.Message) {
	if evt.Info.IsFromMe {
		return
	}

	chatJID := evt.Info.Chat.String()
	sender := evt.Info.Sender.User
	pushName := evt.Info.PushName
	if pushName == "" {
		pushName = sender
	}

	var text string
	if msg := evt.Message.GetConversation(); msg != "" {
		text = msg
	} else if ext := evt.Message.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		text = ext.GetText()
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	// Parsing command berdasarkan prefix yang diatur (., !, #, ?, dll)
	prefix, cmd, args, isCmd := w.parseCommand(text)
	if !isCmd {
		// Pesan percakapan biasa di grup WA -> TIDAK DI-FORWARD ke Minecraft
		return
	}

	switch cmd {
	case "cekserver", "status", "server", "info":
		go w.replyServerStatus(evt.Info.Chat)

	case "chat", "mc", "game":
		if args == "" {
			_ = w.SendToJID(context.Background(), evt.Info.Chat, fmt.Sprintf("⚠️ Format salah! Gunakan: *%schat <pesan>* (contoh: %schat halo semuanya)", prefix, prefix))
			return
		}

		w.mu.RLock()
		cb := w.messageCallback
		w.mu.RUnlock()

		if cb != nil {
			cb(chatJID, sender, pushName, args)
			fmt.Printf("[WA -> MC] [%s] %s: %s\n", pushName, sender, args)

			// Kirim feedback balasan ke grup WhatsApp
			replyConfirm := fmt.Sprintf("✅ Pesan dari *%s* berhasil dikirim ke server Minecraft!", pushName)
			_ = w.SendToJID(context.Background(), evt.Info.Chat, replyConfirm)
		}
	}
}

func (w *WAClient) replyServerStatus(target types.JID) {
	status := w.GetServerStatus()

	var replyText string
	if status.Online {
		playerListStr := "Tidak ada player"
		if len(status.PlayerList) > 0 {
			playerListStr = strings.Join(status.PlayerList, ", ")
		}

		elapsed := time.Since(status.LastUpdated).Round(time.Second)
		replyText = fmt.Sprintf("🟢 *STATUS SERVER MINECRAFT*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"• Status: *ONLINE*\n"+
			"• Server: *%s*\n"+
			"• Player: *%d / %d Online*\n"+
			"• Daftar: %s\n"+
			"• TPS: *%.2f*\n"+
			"• Update: *%s lalu*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"_Ketik .chat <pesan> untuk mengirim chat ke in-game._",
			w.config.ServerName, status.PlayerCount, status.MaxPlayers, playerListStr, status.TPS, elapsed)
	} else {
		replyText = fmt.Sprintf("🔴 *STATUS SERVER MINECRAFT*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"• Status: *OFFLINE*\n"+
			"• Server: *%s*\n"+
			"• Keterangan: Server Minecraft sedang offline / restart.\n"+
			"━━━━━━━━━━━━━━━━━━━━━",
			w.config.ServerName)
	}

	_ = w.SendToJID(context.Background(), target, replyText)
}

func (w *WAClient) Start(ctx context.Context) error {
	if w.client.Store.ID == nil {
		qrChan, _ := w.client.GetQRChannel(ctx)
		err := w.client.Connect()
		if err != nil {
			return fmt.Errorf("failed to connect: %w", err)
		}

		go func() {
			for evt := range qrChan {
				if evt.Event == "code" {
					fmt.Println("\n================= SCAN QR CODE ==================")
					fmt.Println("Scan QR code berikut menggunakan aplikasi WhatsApp:")
					qrterminal.GenerateHalfBlock(evt.Code, qrterminal.L, os.Stdout)
					fmt.Println("=================================================")
				} else {
					fmt.Printf("[WA-Client] QR channel event: %s\n", evt.Event)
				}
			}
		}()
	} else {
		err := w.client.Connect()
		if err != nil {
			return fmt.Errorf("failed to reconnect: %w", err)
		}
		w.mu.Lock()
		w.isLoggedIn = true
		w.mu.Unlock()
		fmt.Println("[WA-Client] Reconnected using existing session.")
	}

	return nil
}

func (w *WAClient) IsReady() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.client != nil && w.client.IsConnected() && w.isLoggedIn
}

func (w *WAClient) SendMessage(ctx context.Context, phone, message string) error {
	if !w.IsReady() {
		return ErrWANotConnected
	}

	cleanPhone := strings.TrimPrefix(phone, "+")
	jid := types.NewJID(cleanPhone, types.DefaultUserServer)
	return w.SendToJID(ctx, jid, message)
}

func (w *WAClient) SendToJID(ctx context.Context, jid types.JID, message string) error {
	if !w.IsReady() {
		return ErrWANotConnected
	}

	msg := &waProto.Message{
		Conversation: proto.String(message),
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	_, err := w.client.SendMessage(sendCtx, jid, msg)
	if err != nil {
		return fmt.Errorf("failed to send wa message: %w", err)
	}

	return nil
}

func (w *WAClient) SendToGroups(ctx context.Context, message string) int {
	if !w.IsReady() {
		return 0
	}

	successCount := 0
	for _, rawJid := range w.config.GroupJIDs {
		cleanJid := strings.TrimSpace(rawJid)
		if cleanJid == "" {
			continue
		}

		var jid types.JID
		if strings.Contains(cleanJid, "@") {
			jid, _ = types.ParseJID(cleanJid)
		} else {
			jid = types.NewJID(cleanJid, types.GroupServer)
		}

		if err := w.SendToJID(ctx, jid, message); err == nil {
			successCount++
		} else {
			fmt.Printf("[WA-Client] Gagal kirim ke grup %s: %v\n", cleanJid, err)
		}
	}

	return successCount
}

func (w *WAClient) Disconnect() {
	if w.client != nil {
		w.client.Disconnect()
	}
	if w.container != nil {
		_ = w.container.Close()
	}
}
