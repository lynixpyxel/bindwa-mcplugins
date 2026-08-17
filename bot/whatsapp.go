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

type WAClient struct {
	client     *whatsmeow.Client
	container  *sqlstore.Container
	mu         sync.RWMutex
	isLoggedIn bool
}

func NewWAClient(ctx context.Context, dbPath string) (*WAClient, error) {
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
	}

	client.AddEventHandler(w.eventHandler)

	return w, nil
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
	default:
		_ = evt
	}
}

func (w *WAClient) Start(ctx context.Context) error {
	if w.client.Store.ID == nil {
		// Device belum pairing, perlu QR scan
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
		// Device sudah pairing sebelumnya
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

func (w *WAClient) Disconnect() {
	if w.client != nil {
		w.client.Disconnect()
	}
	if w.container != nil {
		_ = w.container.Close()
	}
}
