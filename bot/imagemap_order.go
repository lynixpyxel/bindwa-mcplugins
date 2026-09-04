package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/gif"
	_ "image/jpeg"
	"image/png"
	"io"
	"net/http"
	netUrl "net/url"
	"os"
	"os/exec"
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

// ── TriPay API Client ────────────────────────────────────────────────────────

type TripayClient struct {
	BaseURL       string
	MerchantCode  string
	APIKey        string
	PrivateKey    string
	PaymentMethod string
	HTTPClient    *http.Client
}

func NewTripayClient(baseURL, merchantCode, apiKey, privateKey, paymentMethod string) *TripayClient {
	if baseURL == "" {
		baseURL = "https://tripay.co.id/api-sandbox"
	}
	if paymentMethod == "" {
		paymentMethod = "QRIS"
	}
	return &TripayClient{
		BaseURL:       strings.TrimRight(baseURL, "/"),
		MerchantCode:  merchantCode,
		APIKey:        apiKey,
		PrivateKey:    privateKey,
		PaymentMethod: paymentMethod,
		HTTPClient:    &http.Client{Timeout: 30 * time.Second},
	}
}

// GenerateSignature menghasilkan signature HMAC-SHA256 untuk closed transaction TriPay.
// Formula resmi: HMAC-SHA256(merchant_code + merchant_ref + amount, private_key)
func (t *TripayClient) GenerateSignature(merchantRef string, amount int) string {
	payload := fmt.Sprintf("%s%s%d", t.MerchantCode, merchantRef, amount)
	h := hmac.New(sha256.New, []byte(t.PrivateKey))
	h.Write([]byte(payload))
	return hex.EncodeToString(h.Sum(nil))
}

// ValidateCallbackSignature memvalidasi signature HMAC-SHA256 webhook callback dari TriPay.
func (t *TripayClient) ValidateCallbackSignature(rawBody []byte, expectedSignature string) bool {
	h := hmac.New(sha256.New, []byte(t.PrivateKey))
	h.Write(rawBody)
	calculated := hex.EncodeToString(h.Sum(nil))
	return hmac.Equal([]byte(calculated), []byte(expectedSignature))
}

type TripayOrderItem struct {
	SKU      string `json:"sku,omitempty"`
	Name     string `json:"name"`
	Price    int    `json:"price"`
	Quantity int    `json:"quantity"`
}

type TripayTransactionData struct {
	Reference      string `json:"reference"`
	MerchantRef    string `json:"merchant_ref"`
	PaymentMethod  string `json:"payment_method"`
	PaymentName    string `json:"payment_name"`
	CustomerName   string `json:"customer_name"`
	Amount         int    `json:"amount"`
	FeeMerchant    int    `json:"fee_merchant"`
	FeeCustomer    int    `json:"fee_customer"`
	TotalFee       int    `json:"total_fee"`
	AmountReceived int    `json:"amount_received"`
	PayCode        string `json:"pay_code,omitempty"`
	PayURL         string `json:"pay_url,omitempty"`
	CheckoutURL    string `json:"checkout_url,omitempty"`
	Status         string `json:"status"` // UNPAID, PAID, EXPIRED, FAILED, REFUND
	ExpiredTime    int64  `json:"expired_time"`
	QRString       string `json:"qr_string,omitempty"`
	QRURL          string `json:"qr_url,omitempty"`
}

type TripayCreateResponse struct {
	Success bool                  `json:"success"`
	Message string                `json:"message,omitempty"`
	Data    TripayTransactionData `json:"data"`
}

type TripayDetailResponse struct {
	Success bool                  `json:"success"`
	Message string                `json:"message,omitempty"`
	Data    TripayTransactionData `json:"data"`
}

func (t *TripayClient) CreateClosedTransaction(ctx context.Context, merchantRef, mapName string, amount int, customerPhone string) (*TripayCreateResponse, error) {
	if t.APIKey == "" || t.PrivateKey == "" || t.MerchantCode == "" {
		return nil, errors.New("konfigurasi TriPay (merchant_code, api_key, private_key) belum lengkap")
	}

	sig := t.GenerateSignature(merchantRef, amount)
	expiry := time.Now().Add(15 * time.Minute).Unix()

	custName := customerPhone
	if custName == "" {
		custName = "Customer"
	}
	custEmail := fmt.Sprintf("%s@customer.bindwa", strings.TrimPrefix(customerPhone, "+"))

	reqBody := map[string]interface{}{
		"method":         t.PaymentMethod,
		"merchant_ref":   merchantRef,
		"amount":         amount,
		"customer_name":  custName,
		"customer_email": custEmail,
		"customer_phone": customerPhone,
		"order_items": []TripayOrderItem{
			{
				SKU:      "MAP-" + mapName,
				Name:     "ImageMap " + mapName,
				Price:    amount,
				Quantity: 1,
			},
		},
		"expired_time": expiry,
		"signature":    sig,
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal tripay request: %w", err)
	}

	url := t.BaseURL + "/transaction/create"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create tripay request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+t.APIKey)

	resp, err := t.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tripay request failed: %w", err)
	}
	defer resp.Body.Close()

	var result TripayCreateResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode tripay response: %w", err)
	}

	if !result.Success {
		msg := result.Message
		if msg == "" {
			msg = fmt.Sprintf("tripay returned HTTP %d", resp.StatusCode)
		}
		return nil, errors.New(msg)
	}

	return &result, nil
}

func (t *TripayClient) CheckTransactionStatus(ctx context.Context, reference string) (*TripayDetailResponse, error) {
	if t.APIKey == "" {
		return nil, errors.New("konfigurasi TriPay (api_key) belum diatur")
	}

	url := fmt.Sprintf("%s/transaction/detail?reference=%s", t.BaseURL, netUrl.QueryEscape(reference))
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create tripay detail request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+t.APIKey)

	resp, err := t.HTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("tripay detail request failed: %w", err)
	}
	defer resp.Body.Close()

	var result TripayDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("failed to decode tripay detail response: %w", err)
	}

	if !result.Success {
		msg := result.Message
		if msg == "" {
			msg = fmt.Sprintf("tripay detail returned HTTP %d", resp.StatusCode)
		}
		return nil, errors.New(msg)
	}

	return &result, nil
}

// ── ImageMap Order Models & Store ────────────────────────────────────────────

type ImageMapOrder struct {
	PaymentID     string    `json:"payment_id"`
	TransactionID string    `json:"transaction_id,omitempty"`
	SenderPhone   string    `json:"sender_phone"`
	SenderJID     string    `json:"sender_jid"`
	ChatJID       string    `json:"chat_jid"`
	OriginalMsgID string    `json:"original_msg_id,omitempty"`
	QRMessageID   string    `json:"qr_message_id,omitempty"`
	MapName       string    `json:"map_name"`
	MediaType     string    `json:"media_type,omitempty"` // "image" or "gif"
	Width         int       `json:"width"`
	Height        int       `json:"height"`
	TotalTiles    int       `json:"total_tiles"`
	Amount        int       `json:"amount"`
	Status        string    `json:"status"` // awaiting_approval, pending, paid, waiting_mc_username, rejected, cancelled_by_user, expired
	PlayerName    string    `json:"player_name,omitempty"`
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

// GetOrderByTransactionID mengambil data order berdasarkan reference / Transaction ID.
func (m *ImageMapOrderManager) GetOrderByTransactionID(trxID string) *ImageMapOrder {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cleanID := strings.TrimSpace(trxID)
	for _, o := range m.byPaymentID {
		if strings.EqualFold(o.TransactionID, cleanID) {
			return o
		}
	}
	return nil
}

// ApproveOrder menandai pesanan disetujui admin dan mendaftarkan transactionID TriPay untuk pemantauan pembayaran.
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

// SetQRMessageID menyimpan ID pesan QR yang dikirimkan ke pemesan untuk keperluan revoke/delete.
func (m *ImageMapOrderManager) SetQRMessageID(paymentID, msgID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanID := strings.ToUpper(strings.TrimSpace(paymentID))
	order, exists := m.byPaymentID[cleanID]
	if exists && order != nil {
		order.QRMessageID = msgID
		go m.saveTransactionRecord(order)
	}
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
		if ch, ok := m.cancelChans[order.TransactionID]; ok {
			close(ch)
			delete(m.cancelChans, order.TransactionID)
		}
	}

	go m.saveTransactionRecord(order)
	return nil
}

// SetOrderWaitingUsername menandai order sedang menunggu input username Minecraft dari pemesan.
func (m *ImageMapOrderManager) SetOrderWaitingUsername(paymentID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanID := strings.ToUpper(strings.TrimSpace(paymentID))
	order, exists := m.byPaymentID[cleanID]
	if exists && order != nil {
		order.Status = "waiting_mc_username"
		order.UpdatedAt = time.Now()
		go m.saveTransactionRecord(order)
	}
}

// GetOrderWaitingUsername mencari order yang sedang menunggu input username Minecraft untuk nomor HP pemesan tertentu.
func (m *ImageMapOrderManager) GetOrderWaitingUsername(phone string) *ImageMapOrder {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cleanPhone := strings.TrimSpace(phone)
	for _, order := range m.byPaymentID {
		if order != nil && order.SenderPhone == cleanPhone && order.Status == "waiting_mc_username" {
			return order
		}
	}
	return nil
}

// AssignPlayerToOrder menetapkan username Minecraft ke pesanan imagemap.
func (m *ImageMapOrderManager) AssignPlayerToOrder(paymentID, playerName string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	cleanID := strings.ToUpper(strings.TrimSpace(paymentID))
	order, exists := m.byPaymentID[cleanID]
	if exists && order != nil {
		order.PlayerName = playerName
		order.Status = "paid"
		order.UpdatedAt = time.Now()
		go m.saveTransactionRecord(order)
	}
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

// isGIFBytes memeriksa apakah byte diawali header magic number GIF (GIF87a / GIF89a).
func isGIFBytes(data []byte) bool {
	return len(data) >= 6 && (string(data[:6]) == "GIF87a" || string(data[:6]) == "GIF89a")
}

// ValidateGIF memvalidasi file GIF dan memastikan dapat dibaca frame-framenya.
func ValidateGIF(data []byte) error {
	if len(data) == 0 {
		return errors.New("file GIF kosong")
	}
	if !isGIFBytes(data) {
		return errors.New("bukan format GIF yang valid")
	}
	_, err := gif.DecodeAll(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("file GIF rusak atau tidak dapat dibaca: %w", err)
	}
	return nil
}

// convertVideoToGIF mengonversi video WhatsApp (MP4) ke file GIF animasi menggunakan ffmpeg.
func convertVideoToGIF(ctx context.Context, vidBytes []byte) ([]byte, error) {
	if len(vidBytes) == 0 {
		return nil, errors.New("data video GIF kosong")
	}

	tmpFile, err := os.CreateTemp("", "wagif_in_*.mp4")
	if err != nil {
		return nil, fmt.Errorf("gagal membuat file temp video: %w", err)
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.Write(vidBytes); err != nil {
		_ = tmpFile.Close()
		return nil, fmt.Errorf("gagal menulis video ke file temp: %w", err)
	}
	_ = tmpFile.Close()

	outPath := filepath.Join(os.TempDir(), fmt.Sprintf("wagif_out_%d.gif", time.Now().UnixNano()))
	defer os.Remove(outPath)

	cmd := exec.CommandContext(ctx, "ffmpeg", "-y", "-i", tmpFile.Name(),
		"-vf", "fps=10,scale=min(iw\\,320):-1:flags=lanczos",
		"-c:v", "gif", outPath)

	if output, err := cmd.CombinedOutput(); err != nil {
		return nil, fmt.Errorf("gagal mengonversi video ke GIF: %w (log: %s)", err, string(output))
	}

	gifBytes, err := os.ReadFile(outPath)
	if err != nil {
		return nil, fmt.Errorf("gagal membaca output GIF: %w", err)
	}
	if len(gifBytes) == 0 {
		return nil, errors.New("hasil konversi animasi GIF kosong")
	}

	return gifBytes, nil
}

// ExtractImageFromEvent mengunduh atau mengekstrak data gambar/GIF dari pesan WhatsApp (caption, quoted, atau URL).
// Mengembalikan (mediaBytes, mediaType ("image"|"gif"), error).
func ExtractImageFromEvent(ctx context.Context, client *whatsmeow.Client, evt *events.Message, urlStr string) ([]byte, string, error) {
	// 1. Jika ada URL gambar langsung
	if urlStr != "" {
		req, err := http.NewRequestWithContext(ctx, "GET", urlStr, nil)
		if err != nil {
			return nil, "", fmt.Errorf("URL tidak valid: %w", err)
		}
		req.Header.Set("User-Agent", "Mozilla/5.0 (compatible; CSMPImageMapBot/1.0)")

		clientHTTP := &http.Client{Timeout: 20 * time.Second}
		resp, err := clientHTTP.Do(req)
		if err != nil {
			return nil, "", fmt.Errorf("gagal mengunduh gambar/GIF dari link: %w", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			return nil, "", fmt.Errorf("link gambar/GIF mengembalikan HTTP %d", resp.StatusCode)
		}

		rawBytes, err := io.ReadAll(io.LimitReader(resp.Body, 15*1024*1024)) // max 15MB
		if err != nil {
			return nil, "", fmt.Errorf("gagal membaca data media dari link: %w", err)
		}

		isGIF := strings.HasSuffix(strings.ToLower(urlStr), ".gif") ||
			strings.Contains(strings.ToLower(resp.Header.Get("Content-Type")), "gif") ||
			isGIFBytes(rawBytes)

		if isGIF {
			if err := ValidateGIF(rawBytes); err != nil {
				return nil, "", err
			}
			return rawBytes, "gif", nil
		}

		pngBytes, err := ValidateAndConvertToPNG(rawBytes)
		if err != nil {
			return nil, "", err
		}
		return pngBytes, "image", nil
	}

	if client == nil || evt == nil || evt.Message == nil {
		return nil, "", errors.New("pesan tidak mengandung gambar ataupun GIF")
	}

	// 2. Cek pesan saat ini (Direct Media)
	if imgMsg := evt.Message.GetImageMessage(); imgMsg != nil {
		imgBytes, err := client.Download(ctx, imgMsg)
		if err != nil {
			return nil, "", fmt.Errorf("gagal mengunduh foto dari WhatsApp: %w", err)
		}
		if isGIFBytes(imgBytes) {
			if err := ValidateGIF(imgBytes); err == nil {
				return imgBytes, "gif", nil
			}
		}
		pngBytes, err := ValidateAndConvertToPNG(imgBytes)
		if err != nil {
			return nil, "", err
		}
		return pngBytes, "image", nil
	}

	if vidMsg := evt.Message.GetVideoMessage(); vidMsg != nil {
		vidBytes, err := client.Download(ctx, vidMsg)
		if err != nil {
			return nil, "", fmt.Errorf("gagal mengunduh GIF/video dari WhatsApp: %w", err)
		}
		gifBytes, err := convertVideoToGIF(ctx, vidBytes)
		if err != nil {
			return nil, "", err
		}
		return gifBytes, "gif", nil
	}

	if docMsg := evt.Message.GetDocumentMessage(); docMsg != nil {
		docBytes, err := client.Download(ctx, docMsg)
		if err != nil {
			return nil, "", fmt.Errorf("gagal mengunduh dokumen dari WhatsApp: %w", err)
		}
		isGIF := strings.HasSuffix(strings.ToLower(docMsg.GetFileName()), ".gif") ||
			strings.Contains(strings.ToLower(docMsg.GetMimetype()), "gif") ||
			isGIFBytes(docBytes)
		if isGIF {
			if err := ValidateGIF(docBytes); err != nil {
				return nil, "", err
			}
			return docBytes, "gif", nil
		}
		pngBytes, err := ValidateAndConvertToPNG(docBytes)
		if err != nil {
			return nil, "", err
		}
		return pngBytes, "image", nil
	}

	// 3. Cek jika me-reply pesan (Quoted Media)
	var ctxInfo *waProto.ContextInfo
	if ext := evt.Message.GetExtendedTextMessage(); ext != nil {
		ctxInfo = ext.GetContextInfo()
	}

	if ctxInfo != nil && ctxInfo.GetQuotedMessage() != nil {
		qm := ctxInfo.GetQuotedMessage()
		if qImg := qm.GetImageMessage(); qImg != nil {
			imgBytes, err := client.Download(ctx, qImg)
			if err != nil {
				return nil, "", fmt.Errorf("gagal mengunduh foto yang di-quote: %w", err)
			}
			if isGIFBytes(imgBytes) {
				if err := ValidateGIF(imgBytes); err == nil {
					return imgBytes, "gif", nil
				}
			}
			pngBytes, err := ValidateAndConvertToPNG(imgBytes)
			if err != nil {
				return nil, "", err
			}
			return pngBytes, "image", nil
		}

		if qVid := qm.GetVideoMessage(); qVid != nil {
			vidBytes, err := client.Download(ctx, qVid)
			if err != nil {
				return nil, "", fmt.Errorf("gagal mengunduh GIF/video yang di-quote: %w", err)
			}
			gifBytes, err := convertVideoToGIF(ctx, vidBytes)
			if err != nil {
				return nil, "", err
			}
			return gifBytes, "gif", nil
		}

		if qDoc := qm.GetDocumentMessage(); qDoc != nil {
			docBytes, err := client.Download(ctx, qDoc)
			if err != nil {
				return nil, "", fmt.Errorf("gagal mengunduh dokumen yang di-quote: %w", err)
			}
			isGIF := strings.HasSuffix(strings.ToLower(qDoc.GetFileName()), ".gif") ||
				strings.Contains(strings.ToLower(qDoc.GetMimetype()), "gif") ||
				isGIFBytes(docBytes)
			if isGIF {
				if err := ValidateGIF(docBytes); err != nil {
					return nil, "", err
				}
				return docBytes, "gif", nil
			}
			pngBytes, err := ValidateAndConvertToPNG(docBytes)
			if err != nil {
				return nil, "", err
			}
			return pngBytes, "image", nil
		}
	}

	return nil, "", errors.New("tidak ditemukan gambar atau GIF pada pesan ataupun pesan yang di-reply")
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

func (w *WAClient) extractContextInfo(msg *waProto.Message) *waProto.ContextInfo {
	if msg == nil {
		return nil
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil && ext.GetContextInfo() != nil {
		return ext.GetContextInfo()
	}
	if img := msg.GetImageMessage(); img != nil && img.GetContextInfo() != nil {
		return img.GetContextInfo()
	}
	if doc := msg.GetDocumentMessage(); doc != nil && doc.GetContextInfo() != nil {
		return doc.GetContextInfo()
	}
	if vid := msg.GetVideoMessage(); vid != nil && vid.GetContextInfo() != nil {
		return vid.GetContextInfo()
	}
	if btn := msg.GetButtonsResponseMessage(); btn != nil && btn.GetContextInfo() != nil {
		return btn.GetContextInfo()
	}
	if tmpl := msg.GetTemplateButtonReplyMessage(); tmpl != nil && tmpl.GetContextInfo() != nil {
		return tmpl.GetContextInfo()
	}
	if inter := msg.GetInteractiveResponseMessage(); inter != nil && inter.GetContextInfo() != nil {
		return inter.GetContextInfo()
	}
	if eph := msg.GetEphemeralMessage(); eph != nil && eph.GetMessage() != nil {
		return w.extractContextInfo(eph.GetMessage())
	}
	if vo := msg.GetViewOnceMessage(); vo != nil && vo.GetMessage() != nil {
		return w.extractContextInfo(vo.GetMessage())
	}
	if voV2 := msg.GetViewOnceMessageV2(); voV2 != nil && voV2.GetMessage() != nil {
		return w.extractContextInfo(voV2.GetMessage())
	}
	return nil
}

func (w *WAClient) extractTextFromMessage(msg *waProto.Message) string {
	if msg == nil {
		return ""
	}
	if conv := msg.GetConversation(); conv != "" {
		return conv
	}
	if ext := msg.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		return ext.GetText()
	}
	if img := msg.GetImageMessage(); img != nil && img.GetCaption() != "" {
		return img.GetCaption()
	}
	if doc := msg.GetDocumentMessage(); doc != nil && doc.GetCaption() != "" {
		return doc.GetCaption()
	}
	if vid := msg.GetVideoMessage(); vid != nil && vid.GetCaption() != "" {
		return vid.GetCaption()
	}
	if bm := msg.GetButtonsMessage(); bm != nil && bm.GetContentText() != "" {
		return bm.GetContentText()
	}
	if tm := msg.GetTemplateMessage(); tm != nil {
		if h := tm.GetHydratedFourRowTemplate(); h != nil && h.GetHydratedContentText() != "" {
			return h.GetHydratedContentText()
		}
	}
	if im := msg.GetInteractiveMessage(); im != nil && im.GetBody() != nil && im.GetBody().GetText() != "" {
		return im.GetBody().GetText()
	}
	if eph := msg.GetEphemeralMessage(); eph != nil && eph.GetMessage() != nil {
		return w.extractTextFromMessage(eph.GetMessage())
	}
	if vo := msg.GetViewOnceMessage(); vo != nil && vo.GetMessage() != nil {
		return w.extractTextFromMessage(vo.GetMessage())
	}
	if voV2 := msg.GetViewOnceMessageV2(); voV2 != nil && voV2.GetMessage() != nil {
		return w.extractTextFromMessage(voV2.GetMessage())
	}
	return ""
}

func (w *WAClient) extractOrderIDFromEvent(evt *events.Message) string {
	if evt == nil || evt.Message == nil {
		return ""
	}
	ctxInfo := w.extractContextInfo(evt.Message)
	if ctxInfo != nil && ctxInfo.GetQuotedMessage() != nil {
		text := w.extractTextFromMessage(ctxInfo.GetQuotedMessage())
		if match := orderIDRegex.FindString(text); match != "" {
			return strings.ToUpper(match)
		}
	}
	return ""
}

// ── WAClient Order Handlers ──────────────────────────────────────────────────

// ProcessImageMapOrder menangani request pemesanan imagemap dari pesan WhatsApp.
func (w *WAClient) ProcessImageMapOrder(ctx context.Context, evt *events.Message, rawArgs string) {
	chatJID := evt.Info.Chat
	senderPhone := w.resolveSenderPhone(evt)

	// Deteksi apakah pesan menyertakan gambar/GIF langsung atau me-reply gambar/GIF
	isDirectImg := evt.Message.GetImageMessage() != nil
	isDirectVid := evt.Message.GetVideoMessage() != nil
	isDirectDoc := evt.Message.GetDocumentMessage() != nil
	isQuotedImg := false
	isQuotedVid := false
	isQuotedDoc := false

	if ext := evt.Message.GetExtendedTextMessage(); ext != nil && ext.GetContextInfo() != nil && ext.GetContextInfo().GetQuotedMessage() != nil {
		qm := ext.GetContextInfo().GetQuotedMessage()
		isQuotedImg = qm.GetImageMessage() != nil
		isQuotedVid = qm.GetVideoMessage() != nil
		isQuotedDoc = qm.GetDocumentMessage() != nil
	}
	hasMedia := isDirectImg || isQuotedImg || isDirectVid || isQuotedVid || isDirectDoc || isQuotedDoc

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

	// 3. Ekstrak dan konversi gambar/GIF
	downloadCtx, cancelDownload := context.WithTimeout(ctx, 40*time.Second)
	defer cancelDownload()

	imgBytes, mediaType, err := ExtractImageFromEvent(downloadCtx, w.client, evt, imgURL)
	if err != nil {
		replyMsg := fmt.Sprintf("Gagal memproses gambar/GIF: %s", err.Error())
		_ = w.SendReplyToGroup(ctx, chatJID, replyMsg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// 4. Hitung harga map (flat: Image 5k, GIF 7k)
	isGIF := mediaType == "gif"
	totalTiles := width * height
	totalPrice := w.config.CalculateImageMapPrice(width, height, isGIF)

	// 5. Cek kelengkapan konfigurasi TriPay
	if w.tripayClient == nil || w.tripayClient.APIKey == "" || w.tripayClient.PrivateKey == "" || w.tripayClient.MerchantCode == "" {
		replyMsg := "Layanan pembayaran TriPay belum dikonfigurasi oleh pemilik bot. Silakan hubungi admin."
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
		MediaType:     mediaType,
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

	typeLabel := "Gambar Biasa"
	if order.MediaType == "gif" {
		typeLabel = "GIF Animasi"
	}

	// 7. Beritahu pemesan bahwa pesanan sedang menunggu persetujuan admin
	userAckMsg := fmt.Sprintf("Pesanan imagemap telah diterima dan sedang menunggu persetujuan admin.\n\n"+
		"Rincian Pesanan:\n"+
		"Order ID    : %s\n"+
		"Nama Map    : %s\n"+
		"Tipe        : %s\n"+
		"Ukuran      : %dx%d (%d tile)\n"+
		"Total Biaya : Rp %s\n\n"+
		"Mohon tunggu hingga admin menyetujui pesanan Anda sebelum melakukan pembayaran.\n"+
		"Ketik .cancelmap jika ingin membatalkan pesanan ini.",
		order.PaymentID, order.MapName, typeLabel, order.Width, order.Height, order.TotalTiles, formatRupiah(order.Amount))

	_ = w.SendReplyToGroup(ctx, chatJID, userAckMsg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")

	// 8. Kirim notifikasi moderasi ke grup log atau owner (menggunakan pesan gambar biasa dengan caption)
	originName := w.GetGroupName(chatJID)
	if originName == "" || !evt.Info.IsGroup {
		originName = fmt.Sprintf("Chat Pribadi (%s)", senderPhone)
	}

	modCaption := fmt.Sprintf("Ada Order Gambar Baru!\n\n"+
		"Rincian Pesanan:\n"+
		"Order ID    : %s\n"+
		"Pengirim    : %s\n"+
		"Nama Map    : %s\n"+
		"Tipe        : %s\n"+
		"Ukuran      : %dx%d (%d tile)\n"+
		"Total Biaya : Rp %s\n"+
		"Asal Chat   : %s\n\n"+
		"Cara Moderasi:\n"+
		"Reply pesan ini dengan ketik:\n"+
		"- acc : untuk menyetujui pesanan\n"+
		"- reject [alasan] : untuk menolak pesanan\n\n"+
		"Atau ketik langsung:\n"+
		"- .acc %s\n"+
		"- .decline %s [alasan]",
		order.PaymentID, order.SenderPhone, order.MapName,
		typeLabel,
		order.Width, order.Height, order.TotalTiles,
		formatRupiah(order.Amount), originName,
		order.PaymentID, order.PaymentID)

	modMime := "image/png"
	if order.MediaType == "gif" {
		modMime = "image/gif"
	}

	targets := w.getModerationTargets()
	for _, target := range targets {
		if err := w.SendImageReply(ctx, target, imgBytes, modMime, modCaption, nil); err != nil {
			fmt.Printf("[WA-Moderation] Gagal mengirim pesan moderasi ke %s: %v\n", target.String(), err)
		}
	}
}

// HandleApproveImageMap menangani persetujuan pesanan oleh admin (.acc <orderID> atau reply 'acc').
func (w *WAClient) HandleApproveImageMap(ctx context.Context, evt *events.Message, rawArgs string) {
	chatJID := evt.Info.Chat

	if !w.isSenderOwner(evt) && !w.IsUserGroupAdmin(ctx, chatJID, evt) {
		_ = w.SendReplyToGroup(ctx, chatJID, "Perintah ini hanya dapat dijalankan oleh admin atau pemilik bot.", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	orderID := strings.TrimSpace(rawArgs)
	if orderID == "" || !strings.HasPrefix(strings.ToUpper(orderID), "MAP-") {
		if fromEvent := w.extractOrderIDFromEvent(evt); fromEvent != "" {
			orderID = fromEvent
		}
	}
	if parts := strings.Fields(orderID); len(parts) > 0 {
		orderID = parts[0]
	}

	if orderID == "" {
		_ = w.SendReplyToGroup(ctx, chatJID, "Format salah. Masukkan Order ID atau reply pesan order dengan 'acc'.\nContoh: reply pesan order dengan 'acc', atau ketik .acc MAP-1234", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
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

	if w.tripayClient == nil || w.tripayClient.APIKey == "" || w.tripayClient.MerchantCode == "" || w.tripayClient.PrivateKey == "" {
		_ = w.SendReplyToGroup(ctx, chatJID, "Layanan pembayaran TriPay belum dikonfigurasi. Hubungi pemilik bot.", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// Generate transaksi tertutup via TriPay
	tripayResp, err := w.tripayClient.CreateClosedTransaction(ctx, order.PaymentID, order.MapName, order.Amount, order.SenderPhone)
	if err != nil {
		_ = w.SendReplyToGroup(ctx, chatJID, fmt.Sprintf("Gagal membuat QRIS TriPay: %s", err.Error()), string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// Render QR Code PNG dari QRString TriPay
	qrString := tripayResp.Data.QRString
	if qrString == "" {
		qrString = tripayResp.Data.QRURL
	}
	if qrString == "" {
		qrString = tripayResp.Data.CheckoutURL
	}

	var qrPNG []byte
	if qrString != "" {
		qrPNG, err = qrcode.Encode(qrString, qrcode.Medium, 512)
		if err != nil {
			_ = w.SendReplyToGroup(ctx, chatJID, fmt.Sprintf("Gagal membuat gambar QR Code: %s", err.Error()), string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
			return
		}
	}

	finalAmount := tripayResp.Data.Amount
	if finalAmount <= 0 {
		finalAmount = order.Amount
	}

	cancelCh := w.imagemapManager.ApproveOrder(order.PaymentID, tripayResp.Data.Reference, finalAmount)

	// Hitung batas waktu dalam menit
	expiredMinutes := 15
	if tripayResp.Data.ExpiredTime > 0 {
		diff := time.Until(time.Unix(tripayResp.Data.ExpiredTime, 0))
		if diff > 0 {
			expiredMinutes = int((diff + 59*time.Second) / time.Minute)
			if expiredMinutes < 1 {
				expiredMinutes = 1
			}
		}
	}

	// Kirim QRIS dan instruksi pembayaran ke pemesan (pesan gambar biasa)
	userChatJID, _ := types.ParseJID(order.ChatJID)
	typeLabel := "Gambar Biasa"
	if order.MediaType == "gif" {
		typeLabel = "GIF Animasi"
	}

	paymentCaption := fmt.Sprintf("Pesanan imagemap Anda telah disetujui oleh admin.\n\n"+
		"Rincian Pembayaran:\n"+
		"Nama Map    : %s\n"+
		"Tipe        : %s\n"+
		"Ukuran      : %dx%d (%d tile)\n"+
		"Total Bayar : Rp %s\n"+
		"Payment ID  : %s\n"+
		"Batas Waktu : %d Menit\n\n"+
		"Instruksi Pembayaran:\n"+
		"1. Scan QRIS di atas menggunakan m-Banking atau e-Wallet (BCA, DANA, GoPay, OVO, ShopeePay, dll).\n"+
		"2. Pastikan nominal transfer sesuai dengan total pembayaran di atas.\n"+
		"3. Setelah pembayaran berhasil diverifikasi, sistem akan otomatis menyimpan file media ke server.\n\n"+
		"Ketik .cancelmap jika ingin membatalkan pesanan ini.",
		order.MapName, typeLabel, order.Width, order.Height, order.TotalTiles,
		formatRupiah(finalAmount), order.PaymentID, expiredMinutes)

	if len(qrPNG) > 0 {
		qrMsgID, err := w.SendImageReplyWithID(ctx, userChatJID, qrPNG, "image/png", paymentCaption, nil)
		if err != nil {
			fmt.Printf("[WA-ImageMap] Gagal kirim gambar QR ke %s (%v), fallback teks...\n", order.ChatJID, err)
			textMsgID, textErr := w.SendToJIDWithID(ctx, userChatJID, paymentCaption)
			if textErr == nil {
				order.QRMessageID = textMsgID
				w.imagemapManager.SetQRMessageID(order.PaymentID, textMsgID)
			}
		} else {
			order.QRMessageID = qrMsgID
			w.imagemapManager.SetQRMessageID(order.PaymentID, qrMsgID)
		}
	} else {
		textMsgID, textErr := w.SendToJIDWithID(ctx, userChatJID, paymentCaption)
		if textErr == nil {
			order.QRMessageID = textMsgID
			w.imagemapManager.SetQRMessageID(order.PaymentID, textMsgID)
		}
	}

	fmt.Printf("[WA-ImageMap] QR dikirimkan ke %s untuk order %s (QRMessageID: %s)\n", order.ChatJID, order.PaymentID, order.QRMessageID)

	// Jalankan background payment watcher
	go w.watchImageMapPayment(order, cancelCh)

	// Konfirmasi ke admin / log grup
	adminConfirm := fmt.Sprintf("Pesanan imagemap %s (%s) telah disetujui.\nQRIS pembayaran sebesar Rp %s telah dikirimkan ke pemesan (%s).",
		order.PaymentID, order.MapName, formatRupiah(finalAmount), order.SenderPhone)
	_ = w.SendReplyToGroup(ctx, chatJID, adminConfirm, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
}

// HandleDeclineImageMap menangani penolakan pesanan oleh admin (.decline <orderID> [alasan] atau reply 'reject [alasan]').
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

	// Bersihkan reason jika berisi kata command decline/reject/tolak
	cleanedReason := strings.TrimSpace(reason)
	for _, prefix := range []string{"reject", "decline", "tolak", ".reject", ".decline", ".tolak"} {
		if strings.HasPrefix(strings.ToLower(cleanedReason), prefix) {
			cleanedReason = strings.TrimSpace(cleanedReason[len(prefix):])
		}
	}
	if cleanedReason == "" {
		cleanedReason = "Gambar tidak memenuhi ketentuan server."
	}
	reason = cleanedReason

	if orderID == "" {
		_ = w.SendReplyToGroup(ctx, chatJID, "Format salah. Masukkan Order ID atau reply pesan order dengan 'reject [alasan]'.\nContoh: reply pesan order dengan 'reject', atau ketik .decline MAP-1234 [alasan]", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
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

// processImageMapPaymentSuccess mengeksekusi penyelesaian pesanan imagemap yang sudah terbayar (idempotent).
func (w *WAClient) processImageMapPaymentSuccess(order *ImageMapOrder) bool {
	if order == nil {
		return false
	}

	// Ambil data terbaru dari store
	current := w.imagemapManager.GetOrderByPaymentID(order.PaymentID)
	if current == nil {
		current = order
	}
	if current.Status == "paid" || current.Status == "waiting_mc_username" {
		// Sudah diproses sebelumnya
		return false
	}

	chatJID, _ := types.ParseJID(current.ChatJID)

	// Hapus pesan QR yang dikirimkan ke pemesan karena pembayaran sudah berhasil
	qrMsgID := current.QRMessageID
	if qrMsgID == "" && order.QRMessageID != "" {
		qrMsgID = order.QRMessageID
	}
	if qrMsgID != "" {
		fmt.Printf("[WA-ImageMap] Pembayaran sukses! Menghapus pesan QR (ID: %s, chat: %s)...\n", qrMsgID, chatJID)
		if delErr := w.DeleteMessage(context.Background(), chatJID, qrMsgID); delErr != nil {
			fmt.Printf("[WA-ImageMap] Gagal menghapus pesan QR: %v\n", delErr)
		} else {
			fmt.Printf("[WA-ImageMap] Pesan QR berhasil dihapus dari %s\n", chatJID)
		}
	} else {
		fmt.Printf("[WA-ImageMap] Peringatan: QRMessageID tidak ditemukan untuk order %s, tidak ada pesan yang dihapus\n", current.PaymentID)
	}

	uploadDir := w.config.ImageMapUploadDir
	if uploadDir == "" {
		uploadDir = "upload/images"
	}
	_ = os.MkdirAll(uploadDir, 0755)

	ext := ".png"
	if current.MediaType == "gif" {
		ext = ".gif"
	}
	fileName := strings.ToLower(current.MapName) + ext
	filePath := filepath.Join(uploadDir, fileName)

	if len(current.ImageData) > 0 {
		if err := os.WriteFile(filePath, current.ImageData, 0644); err != nil {
			fmt.Printf("[WA-ImageMap] Gagal menyimpan file %s: %v\n", filePath, err)
		}
	}

	_ = w.imagemapManager.CompleteOrder(current.PaymentID, fileName)

	// Kirim event WebSocket ke server Minecraft
	publicURL := w.config.PublicURL
	if publicURL == "" {
		publicURL = fmt.Sprintf("http://192.168.18.67:%d", w.config.HTTPPort)
	}
	imageURL := fmt.Sprintf("%s/images/%s", strings.TrimSuffix(publicURL, "/"), fileName)

	broadcasted := false
	if w.broadcastCallback != nil {
		broadcasted = w.broadcastCallback(map[string]interface{}{
			"type":         "imagemap_paid",
			"order_id":     current.PaymentID,
			"map_name":     current.MapName,
			"media_type":   current.MediaType,
			"width":        current.Width,
			"height":       current.Height,
			"sender_phone": current.SenderPhone,
			"image_path":   "/images/" + fileName,
			"image_url":    imageURL,
		})
	}

	typeLabel := "Gambar Biasa"
	if current.MediaType == "gif" {
		typeLabel = "GIF Animasi"
	}

	if !broadcasted {
		// Fallback jika belum tersambung ke WebSocket Minecraft plugin
		w.imagemapManager.SetOrderWaitingUsername(current.PaymentID)
		fallbackMsg := fmt.Sprintf("Pembayaran Berhasil\n\n"+
			"Rincian Pesanan:\n"+
			"Nama Map    : %s\n"+
			"Tipe        : %s\n"+
			"Ukuran      : %dx%d (%d tile)\n"+
			"Total Bayar : Rp %s\n"+
			"Payment ID  : %s\n\n"+
			"Media telah tersimpan di server. Silakan balas (reply) pesan ini dengan *Username Minecraft* Anda (atau ketik *.setuser <username>*) agar map dapat diklaim di dalam game.\n"+
			"(Catatan: Untuk pemain Bedrock, wajib diawali tanda titik, contoh: *.NamaPlayer*)",
			current.MapName, typeLabel, current.Width, current.Height, current.TotalTiles,
			formatRupiah(current.Amount), current.PaymentID)
		_ = w.SendToJID(context.Background(), chatJID, fallbackMsg)
	}

	// Beritahu juga grup log
	logNotice := fmt.Sprintf("Pembayaran Terverifikasi\n\n"+
		"Order ID    : %s\n"+
		"Nama Map    : %s\n"+
		"Tipe        : %s\n"+
		"Ukuran      : %dx%d (%d tile)\n"+
		"Pemesan     : %s\n"+
		"Total       : Rp %s",
		current.PaymentID, current.MapName,
		typeLabel,
		current.Width, current.Height, current.TotalTiles,
		current.SenderPhone,
		formatRupiah(current.Amount))
	for _, target := range w.getModerationTargets() {
		_ = w.SendToJID(context.Background(), target, logNotice)
	}

	return true
}

// HandleTripayPaymentCallback menangani notifikasi webhook pembayaran dari TriPay.
func (w *WAClient) HandleTripayPaymentCallback(merchantRef, reference string) error {
	var order *ImageMapOrder
	if merchantRef != "" {
		order = w.imagemapManager.GetOrderByPaymentID(merchantRef)
	}
	if order == nil && reference != "" {
		order = w.imagemapManager.GetOrderByTransactionID(reference)
	}

	if order == nil {
		return fmt.Errorf("order not found for ref=%s reference=%s", merchantRef, reference)
	}

	fmt.Printf("[WA-ImageMap] Webhook TriPay callback diterima untuk order %s (Reference: %s)\n", order.PaymentID, reference)
	w.processImageMapPaymentSuccess(order)
	return nil
}

// GetTripayClient mengembalikan pointer instance TripayClient.
func (w *WAClient) GetTripayClient() *TripayClient {
	return w.tripayClient
}

// watchImageMapPayment memantau status pembayaran di TriPay secara berkala.
func (w *WAClient) watchImageMapPayment(order *ImageMapOrder, cancelCh chan struct{}) {
	ticker := time.NewTicker(4 * time.Second)
	defer ticker.Stop()

	timeout := time.After(16 * time.Minute)
	chatJID, _ := types.ParseJID(order.ChatJID)

	for {
		select {
		case <-cancelCh:
			fmt.Printf("[WA-ImageMap] Watcher dihentikan untuk order %s\n", order.PaymentID)
			return

		case <-timeout:
			// Hapus pesan QR jika masih ada saat kedaluwarsa
			qrMsgID := order.QRMessageID
			if qrMsgID == "" {
				if latest := w.imagemapManager.GetOrderByPaymentID(order.PaymentID); latest != nil {
					qrMsgID = latest.QRMessageID
				}
			}
			if qrMsgID != "" {
				fmt.Printf("[WA-ImageMap] Timeout pembayaran, menghapus pesan QR (ID: %s, chat: %s)...\n", qrMsgID, chatJID)
				if delErr := w.DeleteMessage(context.Background(), chatJID, qrMsgID); delErr != nil {
					fmt.Printf("[WA-ImageMap] Gagal menghapus pesan QR: %v\n", delErr)
				}
			}
			w.imagemapManager.CancelOrder(order.PaymentID, "expired")
			msg := fmt.Sprintf("Waktu Pembayaran Habis\n\n"+
				"Pesanan imagemap '%s' (Payment ID: %s) telah kedaluwarsa karena melebihi batas waktu pembayaran.", order.MapName, order.PaymentID)
			_ = w.SendToJID(context.Background(), chatJID, msg)
			return

		case <-ticker.C:
			if w.tripayClient == nil || order.TransactionID == "" {
				continue
			}

			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			statusResp, err := w.tripayClient.CheckTransactionStatus(ctx, order.TransactionID)
			cancel()

			if err != nil {
				continue
			}

			if statusResp == nil || statusResp.Data.Status == "" {
				continue
			}

			status := strings.ToUpper(strings.TrimSpace(statusResp.Data.Status))
			if status == "PAID" {
				w.processImageMapPaymentSuccess(order)
				return
			} else if status == "EXPIRED" || status == "FAILED" || status == "REFUND" {
				// Hapus pesan QR jika dibatalkan atau kedaluwarsa
				qrMsgID := order.QRMessageID
				if qrMsgID == "" {
					if latest := w.imagemapManager.GetOrderByPaymentID(order.PaymentID); latest != nil {
						qrMsgID = latest.QRMessageID
					}
				}
				if qrMsgID != "" {
					fmt.Printf("[WA-ImageMap] Status pembayaran TriPay %s, menghapus pesan QR (ID: %s, chat: %s)...\n", status, qrMsgID, chatJID)
					if delErr := w.DeleteMessage(context.Background(), chatJID, qrMsgID); delErr != nil {
						fmt.Printf("[WA-ImageMap] Gagal menghapus pesan QR: %v\n", delErr)
					}
				}

				w.imagemapManager.CancelOrder(order.PaymentID, strings.ToLower(status))
				msg := fmt.Sprintf("Pembayaran Dibatalkan / Kedaluwarsa\n\n"+
					"Pesanan imagemap '%s' (Payment ID: %s) tidak dapat dilanjutkan (Status: %s).", order.MapName, order.PaymentID, status)
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

	// Hapus pesan QR jika masih ada
	qrMsgID := order.QRMessageID
	if qrMsgID == "" {
		if latest := w.imagemapManager.GetOrderByPaymentID(order.PaymentID); latest != nil {
			qrMsgID = latest.QRMessageID
		}
	}
	if qrMsgID != "" {
		userChatJID, _ := types.ParseJID(order.ChatJID)
		fmt.Printf("[WA-ImageMap] Order dibatalkan user, menghapus pesan QR (ID: %s, chat: %s)...\n", qrMsgID, userChatJID)
		if delErr := w.DeleteMessage(ctx, userChatJID, qrMsgID); delErr != nil {
			fmt.Printf("[WA-ImageMap] Gagal menghapus pesan QR: %v\n", delErr)
		}
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
