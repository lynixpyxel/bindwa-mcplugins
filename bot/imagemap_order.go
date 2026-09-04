package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skip2/go-qrcode"
	"go.mau.fi/whatsmeow"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// ── Casaku API Client ────────────────────────────────────────────────────────

type CasakuClient struct {
	BaseURL    string
	LicenseKey string
	QRID       string
	HTTPClient *http.Client
}

func NewCasakuClient(baseURL, licenseKey, qrID string) *CasakuClient {
	if baseURL == "" {
		baseURL = "https://api.casaku.id"
	}
	return &CasakuClient{
		BaseURL:    strings.TrimRight(baseURL, "/"),
		LicenseKey: licenseKey,
		QRID:       qrID,
		HTTPClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type CasakuQRISResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message,omitempty"`
	Data    struct {
		TransactionID    string `json:"transactionId"`
		TotalAmount      int    `json:"totalAmount"`
		QRString         string `json:"qr_string"`
		ExpiredInMinutes int    `json:"expiredInMinutes"`
	} `json:"data"`
}

type CasakuStatusResponse struct {
	Status  int    `json:"status"`
	Message string `json:"message,omitempty"`
	Data    struct {
		TransactionID string `json:"transactionId"`
		Status        string `json:"status"` // pending, paid, success, cancel, expired
		Amount        int    `json:"amount"`
	} `json:"data"`
}

func (c *CasakuClient) GenerateQRIS(ctx context.Context, amount int) (*CasakuQRISResponse, error) {
	reqBody := map[string]interface{}{
		"qr_id":            c.QRID,
		"amount":           amount,
		"packageIds":       []string{"id.dana"},
		"qrType":           "dynamic",
		"paymentMethod":    "qris",
		"useQris":          true,
		"useUniqueCode":    true,
		"expiredInMinutes": 15,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal qris request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/generate/v2/qris", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create qris request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-license-key", c.LicenseKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("casaku request failed: %w", err)
	}
	defer resp.Body.Close()

	var result CasakuQRISResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode casaku response: %w", err)
	}

	if result.Status != 200 || result.Data.QRString == "" {
		if result.Message != "" {
			return nil, errors.New(result.Message)
		}
		return nil, fmt.Errorf("casaku returned HTTP %d", resp.StatusCode)
	}

	return &result, nil
}

func (c *CasakuClient) CheckStatus(ctx context.Context, transactionID string) (*CasakuStatusResponse, error) {
	reqBody := map[string]interface{}{
		"transactionId": transactionID,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/generate/check-status", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-license-key", c.LicenseKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result CasakuStatusResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func (c *CasakuClient) CancelPayment(ctx context.Context, transactionID string) error {
	reqBody := map[string]interface{}{
		"transactionId": transactionID,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, "POST", c.BaseURL+"/api/generate/cancel-status", bytes.NewReader(bodyBytes))
	if err != nil {
		return err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-license-key", c.LicenseKey)

	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

// ── ImageMap Order Models & Store ────────────────────────────────────────────

type ImageMapOrder struct {
	PaymentID     string    `json:"payment_id"`
	TransactionID string    `json:"transaction_id,omitempty"`
	SenderPhone   string    `json:"sender_phone"`
	SenderJID     string    `json:"sender_jid"`
	ChatJID       string    `json:"chat_jid"`
	OriginalMsgID string    `json:"original_msg_id,omitempty"`
	MapName       string    `json:"map_name"`
	Width         int       `json:"width"`
	Height        int       `json:"height"`
	TotalTiles    int       `json:"total_tiles"`
	Amount        int       `json:"amount"`
	Status        string    `json:"status"` // awaiting_approval, pending, paid, rejected, cancelled_by_user, expired
	SavedFileName string    `json:"saved_file_name,omitempty"`
	RejectReason  string    `json:"reject_reason,omitempty"`
	CreatedAt     time.Time `json:"created_at"`
	UpdatedAt     time.Time `json:"updated_at"`

	// In-memory binary image data
	ImageData []byte `json:"-"`
}

type ImageMapOrderManager struct {
	mu           sync.RWMutex
	activeOrders map[string]*ImageMapOrder // key: mapName (lower)
	byPaymentID  map[string]*ImageMapOrder // key: paymentID (upper)
	cancelChans  map[string]chan struct{}  // key: transactionID
	uploadDir    string
	dbPath       string
}

func NewImageMapOrderManager(uploadDir, dbPath string) *ImageMapOrderManager {
	if uploadDir == "" {
		uploadDir = "upload/images"
	}
	if dbPath == "" {
		dbPath = "upload/imagemap_transactions.json"
	}
	_ = os.MkdirAll(uploadDir, 0755)
	_ = os.MkdirAll(filepath.Dir(dbPath), 0755)

	return &ImageMapOrderManager{
		activeOrders: make(map[string]*ImageMapOrder),
		byPaymentID:  make(map[string]*ImageMapOrder),
		cancelChans:  make(map[string]chan struct{}),
		uploadDir:    uploadDir,
		dbPath:       dbPath,
	}
}

// IsMapNameFileExists memeriksa apakah nama file map sudah ada di folder lokal (misal: logo.png, logo.jpg).
func (m *ImageMapOrderManager) IsMapNameFileExists(mapName string) bool {
	cleanName := strings.ToLower(strings.TrimSpace(mapName))
	extensions := []string{".png", ".jpg", ".jpeg", ".webp", ".gif"}

	for _, ext := range extensions {
		filePath := filepath.Join(m.uploadDir, cleanName+ext)
		if _, err := os.Stat(filePath); err == nil {
			return true
		}
	}
	return false
}

// IsMapNameActivePending memeriksa apakah ada order lain dengan nama yang sama yang sedang pending atau menunggu persetujuan admin.
func (m *ImageMapOrderManager) IsMapNameActivePending(mapName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cleanName := strings.ToLower(strings.TrimSpace(mapName))
	order, exists := m.activeOrders[cleanName]
	return exists && order != nil && (order.Status == "awaiting_approval" || order.Status == "pending")
}

// RegisterAwaitingOrder mendaftarkan pesanan baru yang sedang menunggu verifikasi moderasi admin.
func (m *ImageMapOrderManager) RegisterAwaitingOrder(order *ImageMapOrder) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanName := strings.ToLower(strings.TrimSpace(order.MapName))
	cleanID := strings.ToUpper(strings.TrimSpace(order.PaymentID))
	order.PaymentID = cleanID
	order.Status = "awaiting_approval"

	m.activeOrders[cleanName] = order
	m.byPaymentID[cleanID] = order

	go m.saveTransactionRecord(order)
}

// GetOrderByPaymentID mengambil data order berdasarkan Payment ID (case-insensitive).
func (m *ImageMapOrderManager) GetOrderByPaymentID(paymentID string) *ImageMapOrder {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cleanID := strings.ToUpper(strings.TrimSpace(paymentID))
	return m.byPaymentID[cleanID]
}

// ApproveOrder menandai pesanan disetujui admin dan mendaftarkan transactionID Casaku untuk pemantauan pembayaran.
func (m *ImageMapOrderManager) ApproveOrder(paymentID, transactionID string, finalAmount int) chan struct{} {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanID := strings.ToUpper(strings.TrimSpace(paymentID))
	order, exists := m.byPaymentID[cleanID]
	if !exists || order == nil {
		return nil
	}

	order.TransactionID = transactionID
	order.Amount = finalAmount
	order.Status = "pending"
	order.UpdatedAt = time.Now()

	cancelCh := make(chan struct{})
	m.cancelChans[transactionID] = cancelCh

	go m.saveTransactionRecord(order)
	return cancelCh
}

// RejectOrder menandai pesanan ditolak oleh admin.
func (m *ImageMapOrderManager) RejectOrder(paymentID, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanID := strings.ToUpper(strings.TrimSpace(paymentID))
	order, exists := m.byPaymentID[cleanID]
	if !exists || order == nil {
		return
	}

	order.Status = "rejected"
	order.RejectReason = reason
	order.UpdatedAt = time.Now()

	cleanName := strings.ToLower(strings.TrimSpace(order.MapName))
	delete(m.activeOrders, cleanName)

	go m.saveTransactionRecord(order)
}

// CompleteOrder menandai pesanan berhasil dibayar dan menyimpan gambar.
func (m *ImageMapOrderManager) CompleteOrder(paymentID string, savedFileName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanID := strings.ToUpper(strings.TrimSpace(paymentID))
	order, exists := m.byPaymentID[cleanID]
	if !exists || order == nil {
		return errors.New("order not found")
	}

	order.Status = "paid"
	order.SavedFileName = savedFileName
	order.UpdatedAt = time.Now()

	cleanName := strings.ToLower(strings.TrimSpace(order.MapName))
	delete(m.activeOrders, cleanName)
	if order.TransactionID != "" {
		delete(m.cancelChans, order.TransactionID)
	}

	go m.saveTransactionRecord(order)
	return nil
}

// CancelOrder membatalkan pesanan.
func (m *ImageMapOrderManager) CancelOrder(paymentID, reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanID := strings.ToUpper(strings.TrimSpace(paymentID))
	order, exists := m.byPaymentID[cleanID]
	if !exists || order == nil {
		return
	}

	order.Status = reason
	order.UpdatedAt = time.Now()

	cleanName := strings.ToLower(strings.TrimSpace(order.MapName))
	delete(m.activeOrders, cleanName)

	if order.TransactionID != "" {
		if ch, ok := m.cancelChans[order.TransactionID]; ok {
			close(ch)
			delete(m.cancelChans, order.TransactionID)
		}
	}

	go m.saveTransactionRecord(order)
}

// GetUserPendingOrder mencari pesanan milik nomor pengirim yang sedang awaiting_approval atau pending pembayaran.
func (m *ImageMapOrderManager) GetUserPendingOrder(senderPhone string) *ImageMapOrder {
	m.mu.RLock()
	defer m.mu.RUnlock()

	for _, order := range m.activeOrders {
		if order.SenderPhone == senderPhone && (order.Status == "awaiting_approval" || order.Status == "pending") {
			return order
		}
	}
	return nil
}

func (m *ImageMapOrderManager) saveTransactionRecord(order *ImageMapOrder) {
	m.mu.Lock()
	defer m.mu.Unlock()

	var records map[string]*ImageMapOrder
	data, err := os.ReadFile(m.dbPath)
	if err == nil {
		_ = json.Unmarshal(data, &records)
	}
	if records == nil {
		records = make(map[string]*ImageMapOrder)
	}

	records[order.PaymentID] = order
	out, err := json.MarshalIndent(records, "", "  ")
	if err == nil {
		_ = os.WriteFile(m.dbPath, out, 0644)
	}
}

// ── Parameter Parsing & Validation ───────────────────────────────────────────

var (
	nameRegex        = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)
	dimensionCrossRe = regexp.MustCompile(`^(\d+)[xX*](\d+)$`)
)

// ParseImageMapArgs mem-parsing argumen command imagemap.
// Format yang didukung:
// 1. Quoted/Caption Media:
//    - `<name> <size_h> <size_w>` (contoh: `logo 2 2`)
//    - `<name> <size_w>x<size_h>` (contoh: `logo 2x2`)
// 2. Teks biasa dengan Link URL:
//    - `<url> <name> <size_h> <size_w>`
//    - `<url> <name> <size_w>x<size_h>`
//    - `<name> <size_w>x<size_h> <url>`
func ParseImageMapArgs(rawArgs string, hasMedia bool) (imgURL, mapName string, width, height int, err error) {
	allTokens := strings.Fields(strings.TrimSpace(rawArgs))
	if len(allTokens) == 0 {
		if hasMedia {
			return "", "", 0, 0, errors.New("format salah! Gunakan: *<nama> <tinggi> <lebar>* atau *<nama> <lebar>x<tinggi>*\nContoh: `.imagemap logo 2 2` atau `.imagemap logo 2x2`")
		}
		return "", "", 0, 0, errors.New("format salah! Gunakan salah satu cara:\n1. Reply/quote foto: `.imagemap <nama> <tinggi> <lebar>`\n2. Kirim foto dg caption: `.imagemap <nama> <lebar>x<tinggi>`\n3. Pakai link gambar: `.imagemap <url> <nama> <lebar>x<tinggi>`")
	}

	var tokens []string
	for _, tok := range allTokens {
		lowerTok := strings.ToLower(tok)
		if (strings.HasPrefix(lowerTok, "http://") || strings.HasPrefix(lowerTok, "https://")) && imgURL == "" {
			imgURL = tok
		} else {
			tokens = append(tokens, tok)
		}
	}

	if imgURL == "" && !hasMedia {
		return "", "", 0, 0, errors.New("kamu belum menyertakan gambar!\nSilakan reply foto, kirim foto dengan caption, atau sertakan link URL gambar (diawali https://).")
	}

	if len(tokens) == 0 {
		return "", "", 0, 0, errors.New("masukkan nama dan ukuran map!\nContoh: `.imagemap logo 2x2` atau `.imagemap logo 2 2`")
	}

	if len(tokens) == 1 {
		return "", "", 0, 0, errors.New("ukuran map belum diisi! Contoh: `.imagemap logo 2x2` atau `.imagemap logo 2 2`")
	}

	mapName = tokens[0]

	if !nameRegex.MatchString(mapName) {
		return "", "", 0, 0, errors.New("nama map hanya boleh mengandung huruf, angka, underscore (_), dan strip (-). Tanpa spasi atau simbol lain.")
	}
	if len(mapName) < 2 || len(mapName) > 30 {
		return "", "", 0, 0, errors.New("panjang nama map harus antara 2 hingga 30 karakter.")
	}

	// Cek apakah format cross: logo 2x2
	if len(tokens) == 2 {
		match := dimensionCrossRe.FindStringSubmatch(tokens[1])
		if len(match) == 3 {
			w, _ := strconv.Atoi(match[1])
			h, _ := strconv.Atoi(match[2])
			width = w
			height = h
		} else {
			return "", "", 0, 0, errors.New("format ukuran salah! Gunakan: `2x2` atau `2 2` (tinggi lebar).")
		}
	} else if len(tokens) >= 3 {
		// Format: logo <size_h> <size_w>
		h, errH := strconv.Atoi(tokens[1])
		w, errW := strconv.Atoi(tokens[2])
		if errH != nil || errW != nil {
			return "", "", 0, 0, errors.New("ukuran tinggi dan lebar harus berupa angka bulat! Contoh: `.imagemap logo 2 2`")
		}
		height = h
		width = w
	}

	if width <= 0 || height <= 0 {
		return "", "", 0, 0, errors.New("ukuran map minimal 1x1!")
	}
	if width > 10 || height > 10 {
		return "", "", 0, 0, errors.New("ukuran map maksimal 10x10 agar tidak membebani server Minecraft!")
	}

	return imgURL, mapName, width, height, nil
}

// ── Image Extraction & Normalization ─────────────────────────────────────────

// ExtractImageFromEvent mengunduh atau mengekstrak data gambar dari pesan WhatsApp (caption, quoted, atau URL).
func ExtractImageFromEvent(ctx context.Context, client *whatsmeow.Client, evt *events.Message, urlStr string) ([]byte, error) {
	// 1. Jika ada URL gambar langsung
	if urlStr != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		if err != nil {
			return nil, fmt.Errorf("URL tidak valid: %w", err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CSMPImageMapBot/1.0)")

		clientHTTP := &http.Client{Timeout: 15 * time.Second}
		resp, err := clientHTTP.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gagal mengunduh gambar dari link: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, fmt.Errorf("link gambar mengembalikan HTTP %d", resp.StatusCode)
		}

		imgBytes, err := io.ReadAll(io.LimitReader(resp.Body, 15*1024*1024)) // max 15MB
		if err != nil {
			return nil, fmt.Errorf("gagal membaca data gambar dari link: %w", err)
		}
		return ValidateAndConvertToPNG(imgBytes)
	}

	if client == nil || evt == nil || evt.Message == nil {
		return nil, errors.New("pesan tidak mengandung gambar")
	}

	// 2. Cek jika pesan saat ini adalah gambar (Direct Image Caption)
	if imgMsg := evt.Message.GetImageMessage(); imgMsg != nil {
		imgBytes, err := client.Download(ctx, imgMsg)
		if err != nil {
			return nil, fmt.Errorf("gagal mengunduh foto dari WhatsApp: %w", err)
		}
		return ValidateAndConvertToPNG(imgBytes)
	}

	// 3. Cek jika me-reply pesan gambar (Quoted Image)
	var ctxInfo *waProto.ContextInfo
	if ext := evt.Message.GetExtendedTextMessage(); ext != nil {
		ctxInfo = ext.GetContextInfo()
	}

	if ctxInfo != nil && ctxInfo.GetQuotedMessage() != nil {
		qm := ctxInfo.GetQuotedMessage()
		if qImg := qm.GetImageMessage(); qImg != nil {
			imgBytes, err := client.Download(ctx, qImg)
			if err != nil {
				return nil, fmt.Errorf("gagal mengunduh foto yang di-quote: %w", err)
			}
			return ValidateAndConvertToPNG(imgBytes)
		}
	}

	return nil, errors.New("tidak ditemukan gambar pada pesan ataupun pesan yang di-reply")
}

// ValidateAndConvertToPNG memvalidasi bahwa byte adalah gambar yang valid dan mengonversinya ke format PNG murni.
func ValidateAndConvertToPNG(imgBytes []byte) ([]byte, error) {
	if len(imgBytes) == 0 {
		return nil, errors.New("file gambar kosong")
	}

	// Decode format gambar apa pun (JPEG, PNG, GIF)
	img, _, err := image.Decode(bytes.NewReader(imgBytes))
	if err != nil {
		return nil, fmt.Errorf("format file tidak didukung atau gambar rusak: %w", err)
	}

	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		return nil, fmt.Errorf("gagal mengonversi gambar ke format PNG: %w", err)
	}

	return buf.Bytes(), nil
}

// generateOrderPaymentID membuat ID pesanan acak aman.
func generateOrderPaymentID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return fmt.Sprintf("MAP-%X", b)
}

type InteractiveButtonDef struct {
	Text string
	ID   string
}

// SendImageButtons mengirim gambar disertai tombol interactive ke WhatsApp.
// Menggunakan struktur ButtonsMessage sesuai implementasi Baileys dan order-modules.js,
// dengan fallback berjenjang ke TemplateMessage, InteractiveMessage (tanpa viewOnce), dan standard image.
func (w *WAClient) SendImageButtons(ctx context.Context, jid types.JID, imageBytes []byte, captionTitle, bodyText, footerText string, buttons []InteractiveButtonDef) error {
	if !w.IsReady() {
		return ErrWANotConnected
	}

	uploadResp, err := w.client.Upload(ctx, imageBytes, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("failed to upload image: %w", err)
	}

	imageMsg := &waProto.ImageMessage{
		URL:           &uploadResp.URL,
		DirectPath:    &uploadResp.DirectPath,
		MediaKey:      uploadResp.MediaKey,
		FileEncSHA256: uploadResp.FileEncSHA256,
		FileSHA256:    uploadResp.FileSHA256,
		FileLength:    &uploadResp.FileLength,
		Mimetype:      proto.String("image/png"),
	}

	fullCaption := bodyText
	if captionTitle != "" {
		fullCaption = "*" + captionTitle + "*\n\n" + bodyText
	}

	sendCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	// 1. Metode Utama: ButtonsMessage (Baileys & order-modules.js)
	var waButtons []*waProto.ButtonsMessage_Button
	for _, b := range buttons {
		waButtons = append(waButtons, &waProto.ButtonsMessage_Button{
			ButtonID: proto.String(b.ID),
			ButtonText: &waProto.ButtonsMessage_Button_ButtonText{
				DisplayText: proto.String(b.Text),
			},
			Type: waProto.ButtonsMessage_Button_RESPONSE.Enum(),
		})
	}

	buttonsMsg := &waProto.ButtonsMessage{
		Header: &waProto.ButtonsMessage_ImageMessage{
			ImageMessage: imageMsg,
		},
		HeaderType:  waProto.ButtonsMessage_IMAGE.Enum(),
		ContentText: proto.String(fullCaption),
		FooterText:  proto.String(footerText),
		Buttons:     waButtons,
	}

	_, err = w.client.SendMessage(sendCtx, jid, &waProto.Message{
		ButtonsMessage: buttonsMsg,
	})
	if err == nil {
		return nil
	}

	fmt.Printf("[WA-Buttons] ButtonsMessage error (%v), mencoba TemplateMessage...\n", err)

	// 2. Fallback: TemplateMessage (HydratedFourRowTemplate dari Baileys)
	var hydratedButtons []*waProto.HydratedTemplateButton
	for i, b := range buttons {
		idx := uint32(i + 1)
		hydratedButtons = append(hydratedButtons, &waProto.HydratedTemplateButton{
			Index: &idx,
			HydratedButton: &waProto.HydratedTemplateButton_QuickReplyButton{
				QuickReplyButton: &waProto.HydratedTemplateButton_HydratedQuickReplyButton{
					DisplayText: proto.String(b.Text),
					ID:          proto.String(b.ID),
				},
			},
		})
	}

	templateMsg := &waProto.TemplateMessage{
		Format: &waProto.TemplateMessage_HydratedFourRowTemplate_{
			HydratedFourRowTemplate: &waProto.TemplateMessage_HydratedFourRowTemplate{
				Title: &waProto.TemplateMessage_HydratedFourRowTemplate_ImageMessage{
					ImageMessage: imageMsg,
				},
				HydratedContentText: proto.String(fullCaption),
				HydratedFooterText:  proto.String(footerText),
				HydratedButtons:     hydratedButtons,
				TemplateID:          proto.String(fmt.Sprintf("tpl-%d", time.Now().UnixNano())),
			},
		},
	}

	_, err = w.client.SendMessage(sendCtx, jid, &waProto.Message{
		TemplateMessage: templateMsg,
	})
	if err == nil {
		return nil
	}

	fmt.Printf("[WA-Buttons] TemplateMessage error (%v), mencoba InteractiveMessage langsung (tanpa viewOnce)...\n", err)

	// 3. Fallback: InteractiveMessage langsung (tanpa ViewOnce)
	var nativeButtons []*waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton
	for _, b := range buttons {
		btnJSON := fmt.Sprintf(`{"display_text":%q,"id":%q}`, b.Text, b.ID)
		nativeButtons = append(nativeButtons, &waProto.InteractiveMessage_NativeFlowMessage_NativeFlowButton{
			Name:             proto.String("quick_reply"),
			ButtonParamsJSON: proto.String(btnJSON),
		})
	}

	interactiveMsg := &waProto.InteractiveMessage{
		Header: &waProto.InteractiveMessage_Header{
			Title:              proto.String(captionTitle),
			HasMediaAttachment: proto.Bool(true),
			Media: &waProto.InteractiveMessage_Header_ImageMessage{
				ImageMessage: imageMsg,
			},
		},
		Body: &waProto.InteractiveMessage_Body{
			Text: proto.String(bodyText),
		},
		Footer: &waProto.InteractiveMessage_Footer{
			Text: proto.String(footerText),
		},
		InteractiveMessage: &waProto.InteractiveMessage_NativeFlowMessage_{
			NativeFlowMessage: &waProto.InteractiveMessage_NativeFlowMessage{
				Buttons: nativeButtons,
			},
		},
	}

	_, err = w.client.SendMessage(sendCtx, jid, &waProto.Message{
		InteractiveMessage: interactiveMsg,
	})
	if err == nil {
		return nil
	}

	fmt.Printf("[WA-Buttons] InteractiveMessage error (%v), fallback ke standard image...\n", err)

	// 4. Fallback Terakhir: Standard Image dengan caption lengkap
	fallbackCaption := fullCaption
	if footerText != "" {
		fallbackCaption += "\n\n" + footerText
	}
	return w.SendImageReply(ctx, jid, imageBytes, "image/png", fallbackCaption, nil)
}

// SendImageInteractiveButtons alias untuk kompatibilitas ke SendImageButtons.
func (w *WAClient) SendImageInteractiveButtons(ctx context.Context, jid types.JID, imageBytes []byte, captionTitle, bodyText, footerText string, buttons []InteractiveButtonDef) error {
	return w.SendImageButtons(ctx, jid, imageBytes, captionTitle, bodyText, footerText, buttons)
}

func (w *WAClient) getModerationTargets() []types.JID {
	var targets []types.JID
	if len(w.config.LogGroupJIDs) > 0 {
		for _, raw := range w.config.LogGroupJIDs {
			clean := strings.TrimSpace(raw)
			if clean == "" {
				continue
			}
			var jid types.JID
			if strings.Contains(clean, "@") {
				jid, _ = types.ParseJID(clean)
			} else {
				jid = types.NewJID(clean, types.GroupServer)
			}
			if !jid.IsEmpty() {
				targets = append(targets, jid)
			}
		}
	} else if w.config.OwnerNumber != "" {
		ownerJID := types.NewJID(normalizePhone(w.config.OwnerNumber), types.DefaultUserServer)
		targets = append(targets, ownerJID)
	}
	return targets
}

var orderIDRegex = regexp.MustCompile(`(?i)\bMAP-[A-F0-9]+\b`)

func (w *WAClient) extractOrderIDFromEvent(evt *events.Message) string {
	var ctxInfo *waProto.ContextInfo
	if ext := evt.Message.GetExtendedTextMessage(); ext != nil {
		ctxInfo = ext.GetContextInfo()
	} else if btn := evt.Message.GetButtonsResponseMessage(); btn != nil {
		ctxInfo = btn.GetContextInfo()
	} else if tmpl := evt.Message.GetTemplateButtonReplyMessage(); tmpl != nil {
		ctxInfo = tmpl.GetContextInfo()
	} else if img := evt.Message.GetImageMessage(); img != nil {
		ctxInfo = img.GetContextInfo()
	} else if inter := evt.Message.GetInteractiveResponseMessage(); inter != nil {
		ctxInfo = inter.GetContextInfo()
	}

	if ctxInfo != nil {
		if qm := ctxInfo.GetQuotedMessage(); qm != nil {
			text := qm.GetConversation()
			if text == "" && qm.GetExtendedTextMessage() != nil {
				text = qm.GetExtendedTextMessage().GetText()
			}
			if text == "" && qm.GetImageMessage() != nil {
				text = qm.GetImageMessage().GetCaption()
			}
			if text == "" && qm.GetButtonsMessage() != nil {
				text = qm.GetButtonsMessage().GetContentText()
			}
			if text == "" && qm.GetTemplateMessage() != nil {
				if h := qm.GetTemplateMessage().GetHydratedFourRowTemplate(); h != nil {
					text = h.GetHydratedContentText()
				}
			}
			if text == "" && qm.GetInteractiveMessage() != nil && qm.GetInteractiveMessage().GetBody() != nil {
				text = qm.GetInteractiveMessage().GetBody().GetText()
			}
			if match := orderIDRegex.FindString(text); match != "" {
				return strings.ToUpper(match)
			}
		}
	}
	return ""
}

// ── WAClient Order Handlers ──────────────────────────────────────────────────

// ProcessImageMapOrder menangani request pemesanan imagemap dari pesan WhatsApp.
func (w *WAClient) ProcessImageMapOrder(ctx context.Context, evt *events.Message, rawArgs string) {
	chatJID := evt.Info.Chat
	senderPhone := w.resolveSenderPhone(evt)

	// Deteksi apakah pesan menyertakan gambar langsung atau me-reply gambar
	isDirectImg := evt.Message.GetImageMessage() != nil
	isQuotedImg := false
	if ext := evt.Message.GetExtendedTextMessage(); ext != nil && ext.GetContextInfo() != nil && ext.GetContextInfo().GetQuotedMessage() != nil {
		isQuotedImg = ext.GetContextInfo().GetQuotedMessage().GetImageMessage() != nil
	}
	hasMedia := isDirectImg || isQuotedImg

	imgURL, mapName, width, height, err := ParseImageMapArgs(rawArgs, hasMedia)
	if err != nil {
		_ = w.SendReplyToGroup(ctx, chatJID, err.Error(), string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// Cek apakah user sudah punya order yang sedang berjalan
	if pending := w.imagemapManager.GetUserPendingOrder(senderPhone); pending != nil {
		replyMsg := fmt.Sprintf("Anda masih memiliki pesanan imagemap yang belum diselesaikan.\n\n"+
			"Nama Map: %s (%dx%d)\n"+
			"Total: Rp %s\n"+
			"Order ID: %s\n"+
			"Status: %s\n\n"+
			"Selesaikan pesanan tersebut atau ketik .cancelmap untuk membatalkannya.",
			pending.MapName, pending.Width, pending.Height, formatRupiah(pending.Amount), pending.PaymentID, pending.Status)
		_ = w.SendReplyToGroup(ctx, chatJID, replyMsg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// 1. Cek apakah nama map sudah pernah digunakan di upload/images/
	if w.imagemapManager.IsMapNameFileExists(mapName) {
		replyMsg := fmt.Sprintf("Nama map '%s' sudah pernah digunakan di server Minecraft.\n"+
			"Silakan gunakan nama lain agar tidak konflik dengan item map yang sudah ada.", mapName)
		_ = w.SendReplyToGroup(ctx, chatJID, replyMsg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// 2. Cek apakah nama map sedang dalam proses pemesanan/verifikasi oleh orang lain
	if w.imagemapManager.IsMapNameActivePending(mapName) {
		replyMsg := fmt.Sprintf("Nama map '%s' saat ini sedang dalam proses pemesanan atau verifikasi admin.\n"+
			"Silakan gunakan nama lain atau tunggu beberapa saat.", mapName)
		_ = w.SendReplyToGroup(ctx, chatJID, replyMsg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// 3. Ekstrak dan konversi gambar ke PNG
	downloadCtx, cancelDownload := context.WithTimeout(ctx, 25*time.Second)
	defer cancelDownload()

	imgBytes, err := ExtractImageFromEvent(downloadCtx, w.client, evt, imgURL)
	if err != nil {
		replyMsg := fmt.Sprintf("Gagal memproses gambar: %s", err.Error())
		_ = w.SendReplyToGroup(ctx, chatJID, replyMsg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// 4. Hitung harga map
	totalTiles := width * height
	totalPrice := w.config.CalculateImageMapPrice(width, height)

	// 5. Cek kelengkapan konfigurasi Casaku
	if w.casakuClient == nil || w.casakuClient.LicenseKey == "" || w.casakuClient.QRID == "" {
		replyMsg := "Layanan pembayaran Casaku QRIS belum dikonfigurasi oleh pemilik bot. Silakan hubungi admin."
		_ = w.SendReplyToGroup(ctx, chatJID, replyMsg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// 6. Buat pesanan baru dengan status awaiting_approval
	paymentID := generateOrderPaymentID()
	now := time.Now()

	order := &ImageMapOrder{
		PaymentID:     paymentID,
		SenderPhone:   senderPhone,
		SenderJID:     evt.Info.Sender.String(),
		ChatJID:       chatJID.String(),
		OriginalMsgID: string(evt.Info.ID),
		MapName:       mapName,
		Width:         width,
		Height:        height,
		TotalTiles:    totalTiles,
		Amount:        totalPrice,
		Status:        "awaiting_approval",
		CreatedAt:     now,
		UpdatedAt:     now,
		ImageData:     imgBytes,
	}

	w.imagemapManager.RegisterAwaitingOrder(order)

	// 7. Beritahu pemesan bahwa pesanan sedang menunggu persetujuan admin
	userAckMsg := fmt.Sprintf("Pesanan imagemap telah diterima dan sedang menunggu persetujuan admin.\n\n"+
		"Rincian Pesanan:\n"+
		"Order ID    : %s\n"+
		"Nama Map    : %s\n"+
		"Ukuran      : %dx%d (%d tile)\n"+
		"Total Biaya : Rp %s\n\n"+
		"Mohon tunggu hingga admin menyetujui pesanan Anda sebelum melakukan pembayaran.\n"+
		"Ketik .cancelmap jika ingin membatalkan pesanan ini.",
		order.PaymentID, order.MapName, order.Width, order.Height, order.TotalTiles, formatRupiah(order.Amount))

	_ = w.SendReplyToGroup(ctx, chatJID, userAckMsg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")

	// 8. Kirim notifikasi moderasi ke grup log atau owner
	originName := w.GetGroupName(chatJID)
	if originName == "" || !evt.Info.IsGroup {
		originName = fmt.Sprintf("Chat Pribadi (%s)", senderPhone)
	}

	modTitle := "Ada Order Gambar Baru!"
	modBody := fmt.Sprintf("Rincian Pesanan:\n"+
		"Order ID    : %s\n"+
		"Pengirim    : %s\n"+
		"Nama Map    : %s\n"+
		"Ukuran      : %dx%d (%d tile)\n"+
		"Total Biaya : Rp %s\n"+
		"Asal Chat   : %s\n\n"+
		"Apakah ingin menyetujui (ACC) atau menolak (decline) pesanan gambar ini?\n\n"+
		"Pilihan Tindakan:\n"+
		"- Setujui : .acc %s\n"+
		"- Tolak   : .decline %s [alasan opsional]",
		order.PaymentID, order.SenderPhone, order.MapName,
		order.Width, order.Height, order.TotalTiles,
		formatRupiah(order.Amount), originName,
		order.PaymentID, order.PaymentID)

	modFooter := "Gunakan tombol di bawah atau ketik perintah di atas"
	buttons := []InteractiveButtonDef{
		{Text: "Setujui (ACC)", ID: ".acc " + order.PaymentID},
		{Text: "Tolak (Decline)", ID: ".decline " + order.PaymentID},
	}

	targets := w.getModerationTargets()
	for _, target := range targets {
		if err := w.SendImageInteractiveButtons(ctx, target, imgBytes, modTitle, modBody, modFooter, buttons); err != nil {
			fmt.Printf("[WA-Moderation] Gagal mengirim pesan moderasi ke %s: %v\n", target.String(), err)
		}
	}
}

// HandleApproveImageMap menangani persetujuan pesanan oleh admin (.acc <orderID>).
func (w *WAClient) HandleApproveImageMap(ctx context.Context, evt *events.Message, rawArgs string) {
	chatJID := evt.Info.Chat

	if !w.isSenderOwner(evt) && !w.IsUserGroupAdmin(ctx, chatJID, evt) {
		_ = w.SendReplyToGroup(ctx, chatJID, "Perintah ini hanya dapat dijalankan oleh admin atau pemilik bot.", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	orderID := strings.TrimSpace(rawArgs)
	if orderID == "" {
		orderID = w.extractOrderIDFromEvent(evt)
	}
	if parts := strings.Fields(orderID); len(parts) > 0 {
		orderID = parts[0]
	}

	if orderID == "" {
		_ = w.SendReplyToGroup(ctx, chatJID, "Format salah. Masukkan Order ID yang ingin disetujui.\nContoh: .acc MAP-1234", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	order := w.imagemapManager.GetOrderByPaymentID(orderID)
	if order == nil {
		_ = w.SendReplyToGroup(ctx, chatJID, fmt.Sprintf("Pesanan dengan Order ID '%s' tidak ditemukan.", orderID), string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	if order.Status != "awaiting_approval" {
		_ = w.SendReplyToGroup(ctx, chatJID, fmt.Sprintf("Pesanan '%s' tidak dalam status menunggu persetujuan (status saat ini: %s).", order.PaymentID, order.Status), string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	if w.casakuClient == nil || w.casakuClient.LicenseKey == "" || w.casakuClient.QRID == "" {
		_ = w.SendReplyToGroup(ctx, chatJID, "Layanan pembayaran Casaku QRIS belum dikonfigurasi. Hubungi pemilik bot.", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// Generate QRIS via Casaku
	qrisResp, err := w.casakuClient.GenerateQRIS(ctx, order.Amount)
	if err != nil {
		_ = w.SendReplyToGroup(ctx, chatJID, fmt.Sprintf("Gagal membuat QRIS Casaku: %s", err.Error()), string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// Render QR Code PNG
	qrPNG, err := qrcode.Encode(qrisResp.Data.QRString, qrcode.Medium, 512)
	if err != nil {
		_ = w.SendReplyToGroup(ctx, chatJID, fmt.Sprintf("Gagal membuat gambar QR Code: %s", err.Error()), string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	cancelCh := w.imagemapManager.ApproveOrder(order.PaymentID, qrisResp.Data.TransactionID, qrisResp.Data.TotalAmount)

	// Kirim QRIS dan instruksi pembayaran ke pemesan (disertai tombol Batalkan Pesanan sesuai order-modules.js)
	userChatJID, _ := types.ParseJID(order.ChatJID)
	paymentTitle := "PEMBAYARAN IMAGEMAP"
	paymentCaption := fmt.Sprintf("Pesanan imagemap Anda telah disetujui oleh admin.\n\n"+
		"Rincian Pembayaran:\n"+
		"Nama Map    : %s\n"+
		"Ukuran      : %dx%d (%d tile)\n"+
		"Total Bayar : Rp %s\n"+
		"Payment ID  : %s\n"+
		"Batas Waktu : %d Menit\n\n"+
		"Instruksi Pembayaran:\n"+
		"1. Scan QRIS di atas menggunakan m-Banking atau e-Wallet (BCA, DANA, GoPay, OVO, ShopeePay, dll).\n"+
		"2. Pastikan nominal transfer sesuai dengan total pembayaran di atas.\n"+
		"3. Setelah pembayaran berhasil diverifikasi, sistem akan otomatis menyimpan file gambar ke server.\n\n"+
		"Ketik .cancelmap jika ingin membatalkan pesanan ini.",
		order.MapName, order.Width, order.Height, order.TotalTiles,
		formatRupiah(qrisResp.Data.TotalAmount), order.PaymentID, qrisResp.Data.ExpiredInMinutes)

	userButtons := []InteractiveButtonDef{
		{Text: "Batalkan Pesanan", ID: ".cancelmap"},
	}

	err = w.SendImageButtons(ctx, userChatJID, qrPNG, paymentTitle, paymentCaption, "Scan QRIS di atas untuk membayar", userButtons)
	if err != nil {
		fmt.Printf("[WA-ImageMap] Gagal kirim gambar QR ke %s (%v), kirim teks fallback...\n", order.ChatJID, err)
		_ = w.SendToJID(ctx, userChatJID, paymentCaption)
	}

	// Jalankan background payment watcher
	go w.watchImageMapPayment(order, cancelCh)

	// Konfirmasi ke admin / log grup
	adminConfirm := fmt.Sprintf("Pesanan imagemap %s (%s) telah disetujui.\nQRIS pembayaran sebesar Rp %s telah dikirimkan ke pemesan (%s).",
		order.PaymentID, order.MapName, formatRupiah(qrisResp.Data.TotalAmount), order.SenderPhone)
	_ = w.SendReplyToGroup(ctx, chatJID, adminConfirm, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
}

// HandleDeclineImageMap menangani penolakan pesanan oleh admin (.decline <orderID> [alasan]).
func (w *WAClient) HandleDeclineImageMap(ctx context.Context, evt *events.Message, rawArgs string) {
	chatJID := evt.Info.Chat

	if !w.isSenderOwner(evt) && !w.IsUserGroupAdmin(ctx, chatJID, evt) {
		_ = w.SendReplyToGroup(ctx, chatJID, "Perintah ini hanya dapat dijalankan oleh admin atau pemilik bot.", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	tokens := strings.Fields(strings.TrimSpace(rawArgs))
	var orderID string
	var reason string

	if len(tokens) > 0 && strings.HasPrefix(strings.ToUpper(tokens[0]), "MAP-") {
		orderID = tokens[0]
		if len(tokens) > 1 {
			reason = strings.Join(tokens[1:], " ")
		}
	} else {
		orderID = w.extractOrderIDFromEvent(evt)
		reason = strings.TrimSpace(rawArgs)
	}

	if orderID == "" {
		_ = w.SendReplyToGroup(ctx, chatJID, "Format salah. Masukkan Order ID yang ingin ditolak.\nContoh: .decline MAP-1234 [alasan]", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	if reason == "" {
		reason = "Gambar tidak memenuhi ketentuan server."
	}

	order := w.imagemapManager.GetOrderByPaymentID(orderID)
	if order == nil {
		_ = w.SendReplyToGroup(ctx, chatJID, fmt.Sprintf("Pesanan dengan Order ID '%s' tidak ditemukan.", orderID), string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	if order.Status != "awaiting_approval" {
		_ = w.SendReplyToGroup(ctx, chatJID, fmt.Sprintf("Pesanan '%s' tidak dalam status menunggu persetujuan (status saat ini: %s).", order.PaymentID, order.Status), string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	w.imagemapManager.RejectOrder(order.PaymentID, reason)

	// Konfirmasi ke admin / log grup
	adminConfirm := fmt.Sprintf("Pesanan imagemap %s (%s) telah ditolak.\nAlasan: %s", order.PaymentID, order.MapName, reason)
	_ = w.SendReplyToGroup(ctx, chatJID, adminConfirm, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")

	// Beritahu pemesan
	userChatJID, _ := types.ParseJID(order.ChatJID)
	userMsg := fmt.Sprintf("Pesanan imagemap Anda telah ditolak oleh admin.\n\n"+
		"Order ID : %s\n"+
		"Nama Map : %s\n"+
		"Alasan   : %s\n\n"+
		"Silakan periksa kembali gambar atau ketentuan server sebelum memesan ulang.",
		order.PaymentID, order.MapName, reason)
	_ = w.SendToJID(ctx, userChatJID, userMsg)
}

// watchImageMapPayment memantau status pembayaran di Casaku secara berkala.
func (w *WAClient) watchImageMapPayment(order *ImageMapOrder, cancelCh chan struct{}) {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	timeout := time.After(15 * time.Minute)
	chatJID, _ := types.ParseJID(order.ChatJID)

	for {
		select {
		case <-cancelCh:
			fmt.Printf("[WA-ImageMap] Watcher dihentikan untuk order %s (dibatalkan)\n", order.PaymentID)
			return

		case <-timeout:
			w.imagemapManager.CancelOrder(order.PaymentID, "expired")
			msg := fmt.Sprintf("Waktu Pembayaran Habis\n\n"+
				"Pesanan imagemap '%s' (Payment ID: %s) telah kedaluwarsa karena melebihi batas waktu pembayaran.", order.MapName, order.PaymentID)
			_ = w.SendToJID(context.Background(), chatJID, msg)
			return

		case <-ticker.C:
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			statusResp, err := w.casakuClient.CheckStatus(ctx, order.TransactionID)
			cancel()

			if err != nil {
				continue
			}

			if statusResp == nil || statusResp.Data.Status == "" {
				continue
			}

			status := strings.ToLower(statusResp.Data.Status)
			if status == "paid" || status == "success" {
				uploadDir := w.config.ImageMapUploadDir
				if uploadDir == "" {
					uploadDir = "upload/images"
				}
				_ = os.MkdirAll(uploadDir, 0755)

				fileName := strings.ToLower(order.MapName) + ".png"
				filePath := filepath.Join(uploadDir, fileName)

				if err := os.WriteFile(filePath, order.ImageData, 0644); err != nil {
					fmt.Printf("[WA-ImageMap] Gagal menyimpan file gambar %s: %v\n", filePath, err)
				}

				_ = w.imagemapManager.CompleteOrder(order.PaymentID, fileName)

				successMsg := fmt.Sprintf("Pembayaran Berhasil\n\n"+
					"Rincian Pesanan:\n"+
					"Nama Map    : %s\n"+
					"Ukuran      : %dx%d (%d tile)\n"+
					"Total Bayar : Rp %s\n"+
					"Payment ID  : %s\n"+
					"Status      : Tersimpan di server\n\n"+
					"Terima kasih, pembayaran Anda telah terverifikasi.\n"+
					"Gambar untuk map '%s' sudah berhasil disimpan di server Minecraft.",
					order.MapName, order.Width, order.Height, order.TotalTiles,
					formatRupiah(order.Amount), order.PaymentID, order.MapName)

				_ = w.SendToJID(context.Background(), chatJID, successMsg)

				// Beritahu juga grup log
				logNotice := fmt.Sprintf("Pembayaran Terverifikasi\n\n"+
					"Order ID    : %s\n"+
					"Nama Map    : %s\n"+
					"Pemesan     : %s\n"+
					"Total       : Rp %s\n"+
					"Lokasi File : %s",
					order.PaymentID, order.MapName, order.SenderPhone,
					formatRupiah(order.Amount), filePath)
				for _, target := range w.getModerationTargets() {
					_ = w.SendToJID(context.Background(), target, logNotice)
				}
				return
			} else if status == "cancel" || status == "expired" {
				w.imagemapManager.CancelOrder(order.PaymentID, status)
				msg := fmt.Sprintf("Pembayaran Dibatalkan / Kedaluwarsa\n\n"+
					"Pesanan imagemap '%s' (Payment ID: %s) tidak dapat dilanjutkan.", order.MapName, order.PaymentID)
				_ = w.SendToJID(context.Background(), chatJID, msg)
				return
			}
		}
	}
}

// CancelUserPendingOrder membatalkan pesanan pending milik user.
func (w *WAClient) CancelUserPendingOrder(ctx context.Context, evt *events.Message) {
	chatJID := evt.Info.Chat
	senderPhone := w.resolveSenderPhone(evt)

	order := w.imagemapManager.GetUserPendingOrder(senderPhone)
	if order == nil {
		_ = w.SendReplyToGroup(ctx, chatJID, "Anda tidak memiliki pesanan imagemap yang sedang berjalan.", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	if order.TransactionID != "" && w.casakuClient != nil {
		_ = w.casakuClient.CancelPayment(ctx, order.TransactionID)
	}

	w.imagemapManager.CancelOrder(order.PaymentID, "cancelled_by_user")

	msg := fmt.Sprintf("Pesanan imagemap '%s' (Order ID: %s) berhasil dibatalkan.", order.MapName, order.PaymentID)
	_ = w.SendReplyToGroup(ctx, chatJID, msg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")

	// Jika order dibatalkan saat masih awaiting approval, kabari grup log
	if order.Status == "awaiting_approval" {
		for _, target := range w.getModerationTargets() {
			_ = w.SendToJID(context.Background(), target, fmt.Sprintf("Pesanan imagemap %s (%s) telah dibatalkan oleh pemesan (%s).", order.PaymentID, order.MapName, senderPhone))
		}
	}
}

func formatRupiah(amount int) string {
	str := strconv.Itoa(amount)
	n := len(str)
	if n <= 3 {
		return str
	}
	var sb strings.Builder
	rem := n % 3
	if rem > 0 {
		sb.WriteString(str[:rem])
		if rem < n {
			sb.WriteString(".")
		}
	}
	for i := rem; i < n; i += 3 {
		sb.WriteString(str[i : i+3])
		if i+3 < n {
			sb.WriteString(".")
		}
	}
	return sb.String()
}
