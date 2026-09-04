package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	_ "image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skip2/go-qrcode"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

func TestParseImageMapArgs(t *testing.T) {
	tests := []struct {
		name        string
		args        string
		hasMedia    bool
		wantURL     string
		wantName    string
		wantW       int
		wantH       int
		expectError bool
	}{
		{
			name:        "Quoted image: name h w",
			args:        "logo 3 2",
			hasMedia:    true,
			wantURL:     "",
			wantName:    "logo",
			wantW:       2,
			wantH:       3,
			expectError: false,
		},
		{
			name:        "Quoted image: cross format 2x2",
			args:        "banner 2x2",
			hasMedia:    true,
			wantURL:     "",
			wantName:    "banner",
			wantW:       2,
			wantH:       2,
			expectError: false,
		},
		{
			name:        "Quoted image: cross format uppercase 3X4",
			args:        "poster 3X4",
			hasMedia:    true,
			wantURL:     "",
			wantName:    "poster",
			wantW:       3,
			wantH:       4,
			expectError: false,
		},
		{
			name:        "URL first: https://... name 1x1",
			args:        "https://example.com/art.jpg my_art 1x1",
			hasMedia:    false,
			wantURL:     "https://example.com/art.jpg",
			wantName:    "my_art",
			wantW:       1,
			wantH:       1,
			expectError: false,
		},
		{
			name:        "URL first: https://... name h w",
			args:        "https://example.com/art.jpg cool_map 4 2",
			hasMedia:    false,
			wantURL:     "https://example.com/art.jpg",
			wantName:    "cool_map",
			wantW:       2,
			wantH:       4,
			expectError: false,
		},
		{
			name:        "URL last: name 2x2 https://...",
			args:        "landscape 2x2 https://example.com/nature.png",
			hasMedia:    false,
			wantURL:     "https://example.com/nature.png",
			wantName:    "landscape",
			wantW:       2,
			wantH:       2,
			expectError: false,
		},
		{
			name:        "Missing media and no URL",
			args:        "test 2x2",
			hasMedia:    false,
			expectError: true,
		},
		{
			name:        "Invalid map name characters",
			args:        "bad name! 2x2",
			hasMedia:    true,
			expectError: true,
		},
		{
			name:        "Too large dimension > 10",
			args:        "huge 11x11",
			hasMedia:    true,
			expectError: true,
		},
		{
			name:        "Zero dimension",
			args:        "zero 0x2",
			hasMedia:    true,
			expectError: true,
		},
		{
			name:        "Empty args",
			args:        "",
			hasMedia:    false,
			expectError: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			url, name, w, h, err := ParseImageMapArgs(tc.args, tc.hasMedia)
			if tc.expectError {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if url != tc.wantURL {
				t.Errorf("expected URL %q, got %q", tc.wantURL, url)
			}
			if name != tc.wantName {
				t.Errorf("expected name %q, got %q", tc.wantName, name)
			}
			if w != tc.wantW {
				t.Errorf("expected width %d, got %d", tc.wantW, w)
			}
			if h != tc.wantH {
				t.Errorf("expected height %d, got %d", tc.wantH, h)
			}
		})
	}
}

func TestCalculateImageMapPrice(t *testing.T) {
	cfg := DefaultConfig()

	// Static image: default flat 5000 regardless of dimension
	if p := cfg.CalculateImageMapPrice(1, 1, false); p != 5000 {
		t.Errorf("expected 1x1 image price 5000, got %d", p)
	}
	if p := cfg.CalculateImageMapPrice(2, 2, false); p != 5000 {
		t.Errorf("expected 2x2 image price 5000, got %d", p)
	}
	if p := cfg.CalculateImageMapPrice(3, 3, false); p != 5000 {
		t.Errorf("expected 3x3 image price 5000, got %d", p)
	}

	// GIF animation: default flat 7000 regardless of dimension
	if p := cfg.CalculateImageMapPrice(1, 1, true); p != 7000 {
		t.Errorf("expected 1x1 gif price 7000, got %d", p)
	}
	if p := cfg.CalculateImageMapPrice(2, 2, true); p != 7000 {
		t.Errorf("expected 2x2 gif price 7000, got %d", p)
	}
	if p := cfg.CalculateImageMapPrice(4, 4, true); p != 7000 {
		t.Errorf("expected 4x4 gif price 7000, got %d", p)
	}

	// Custom configuration
	cfg.ImageMapPriceImage = 6000
	cfg.ImageMapPriceGIF = 9000
	if p := cfg.CalculateImageMapPrice(2, 2, false); p != 6000 {
		t.Errorf("expected custom image price 6000, got %d", p)
	}
	if p := cfg.CalculateImageMapPrice(2, 2, true); p != 9000 {
		t.Errorf("expected custom gif price 9000, got %d", p)
	}
}

func TestImageMapOrderManager(t *testing.T) {
	tmpDir := t.TempDir()
	imagesDir := filepath.Join(tmpDir, "images")
	dbPath := filepath.Join(tmpDir, "transactions.json")

	mgr := NewImageMapOrderManager(imagesDir, dbPath)

	// Test 1: Check map name file existence
	if mgr.IsMapNameFileExists("my_logo") {
		t.Errorf("expected my_logo not to exist yet")
	}

	// Create dummy file in upload dir
	dummyFile := filepath.Join(imagesDir, "my_logo.png")
	if err := os.WriteFile(dummyFile, []byte("fake image"), 0644); err != nil {
		t.Fatalf("failed to create dummy file: %v", err)
	}

	if !mgr.IsMapNameFileExists("my_logo") {
		t.Errorf("expected my_logo to exist as file")
	}
	// Case insensitive check
	if !mgr.IsMapNameFileExists("MY_LOGO") {
		t.Errorf("expected MY_LOGO case-insensitive to exist")
	}

	// Test 2: Active pending and moderation flow
	if mgr.IsMapNameActivePending("spawn_map") {
		t.Errorf("expected spawn_map not to be active pending")
	}

	order := &ImageMapOrder{
		PaymentID:   "MAP-1234",
		SenderPhone: "628123456789",
		SenderJID:   "628123456789@s.whatsapp.net",
		ChatJID:     "group@g.us",
		MapName:     "spawn_map",
		Width:       2,
		Height:      2,
		TotalTiles:  4,
		Amount:      4000,
		Status:      "awaiting_approval",
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	mgr.RegisterAwaitingOrder(order)

	// In awaiting_approval status, name is locked
	if !mgr.IsMapNameActivePending("spawn_map") {
		t.Errorf("expected spawn_map to be active pending while awaiting approval")
	}

	// User pending check
	userPending := mgr.GetUserPendingOrder("628123456789")
	if userPending == nil || userPending.PaymentID != "MAP-1234" {
		t.Errorf("expected to find user's pending order")
	}

	// Fetch by Payment ID
	fetched := mgr.GetOrderByPaymentID("map-1234")
	if fetched == nil || fetched.MapName != "spawn_map" {
		t.Fatalf("expected to fetch order by payment ID case-insensitively")
	}

	// Test ApproveOrder
	cancelCh := mgr.ApproveOrder("MAP-1234", "DEV-T0001000000000000001", 4000)
	if cancelCh == nil {
		t.Fatalf("expected valid cancelCh from ApproveOrder")
	}
	if fetched.Status != "pending" {
		t.Errorf("expected status to be pending after approval, got %s", fetched.Status)
	}
	byTrx := mgr.GetOrderByTransactionID("dev-t0001000000000000001")
	if byTrx == nil || byTrx.PaymentID != "MAP-1234" {
		t.Fatalf("expected GetOrderByTransactionID to find order case-insensitively")
	}

	// Test CompleteOrder
	err := mgr.CompleteOrder("MAP-1234", "spawn_map.png")
	if err != nil {
		t.Fatalf("failed to complete order: %v", err)
	}

	if mgr.IsMapNameActivePending("spawn_map") {
		t.Errorf("expected spawn_map to NO LONGER be pending after completion")
	}

	// Test RejectOrder flow with another order
	rejectOrder := &ImageMapOrder{
		PaymentID:   "MAP-9999",
		SenderPhone: "628999999999",
		MapName:     "bad_art",
		Width:       1,
		Height:      1,
		Amount:      1000,
		Status:      "awaiting_approval",
	}
	mgr.RegisterAwaitingOrder(rejectOrder)
	if !mgr.IsMapNameActivePending("bad_art") {
		t.Errorf("expected bad_art to be active pending")
	}
	mgr.RejectOrder("MAP-9999", "Not suitable")
	if mgr.IsMapNameActivePending("bad_art") {
		t.Errorf("expected bad_art to no longer be active after rejection")
	}
	if rejectOrder.Status != "rejected" {
		t.Errorf("expected status rejected, got %s", rejectOrder.Status)
	}
}

func TestValidateAndConvertToPNG(t *testing.T) {
	// Create a synthetic 100x100 JPEG image
	img := image.NewRGBA(image.Rect(0, 0, 100, 100))
	for y := 0; y < 100; y++ {
		for x := 0; x < 100; x++ {
			img.Set(x, y, color.RGBA{R: 255, G: 100, B: 50, A: 255})
		}
	}

	var jpgBuf bytes.Buffer
	if err := jpeg.Encode(&jpgBuf, img, nil); err != nil {
		t.Fatalf("failed to encode jpeg: %v", err)
	}

	// Test conversion
	pngBytes, err := ValidateAndConvertToPNG(jpgBuf.Bytes())
	if err != nil {
		t.Fatalf("ValidateAndConvertToPNG failed: %v", err)
	}

	// Verify the result is a valid PNG
	decodedImg, format, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("failed to decode converted image: %v", err)
	}
	if format != "png" {
		t.Errorf("expected format png, got %s", format)
	}
	bounds := decodedImg.Bounds()
	if bounds.Dx() != 100 || bounds.Dy() != 100 {
		t.Errorf("expected 100x100, got %dx%d", bounds.Dx(), bounds.Dy())
	}
}

func TestValidateGIF(t *testing.T) {
	// Create synthetic GIF
	pal := color.Palette{color.White, color.Black}
	gifImg := image.NewPaletted(image.Rect(0, 0, 10, 10), pal)
	var buf bytes.Buffer
	g := &gif.GIF{
		Image: []*image.Paletted{gifImg},
		Delay: []int{10},
	}
	if err := gif.EncodeAll(&buf, g); err != nil {
		t.Fatalf("failed to encode gif: %v", err)
	}

	gifBytes := buf.Bytes()
	if !isGIFBytes(gifBytes) {
		t.Errorf("expected isGIFBytes to be true for valid gif")
	}

	if err := ValidateGIF(gifBytes); err != nil {
		t.Errorf("expected ValidateGIF to pass, got: %v", err)
	}

	// Test invalid bytes
	if isGIFBytes([]byte("not a gif")) {
		t.Errorf("expected isGIFBytes to be false for non-gif")
	}
	if err := ValidateGIF([]byte("not a gif")); err == nil {
		t.Errorf("expected ValidateGIF to fail for invalid gif")
	}
}

func TestQRCodeGeneration(t *testing.T) {
	qrString := "00020101021226670016ID.CO.SHOPEE.PAY0114936008980000780210...5802ID"
	pngBytes, err := qrcode.Encode(qrString, qrcode.Medium, 256)
	if err != nil {
		t.Fatalf("failed to generate QR code: %v", err)
	}
	if len(pngBytes) == 0 {
		t.Fatalf("generated QR PNG is empty")
	}

	_, format, err := image.Decode(bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatalf("failed to decode generated QR PNG: %v", err)
	}
	if format != "png" {
		t.Errorf("expected png format for QR code, got %s", format)
	}
}

func TestExtractOrderIDFromRegex(t *testing.T) {
	texts := []struct {
		input    string
		expected string
	}{
		{
			input:    "Order ID: MAP-8F3A\nPengirim: 62812345",
			expected: "MAP-8F3A",
		},
		{
			input:    "Pilihan Tindakan: .acc map-1a2b atau .decline map-1a2b",
			expected: "MAP-1A2B",
		},
		{
			input:    "Halo admin tolong acc ya",
			expected: "",
		},
	}

	for _, tc := range texts {
		match := orderIDRegex.FindString(tc.input)
		if strings.ToUpper(match) != tc.expected {
			t.Errorf("input %q: expected %q, got %q", tc.input, tc.expected, strings.ToUpper(match))
		}
	}
}

func TestButtonsMessageStructure(t *testing.T) {
	buttons := []InteractiveButtonDef{
		{Text: "Setujui (ACC)", ID: ".acc MAP-ABCD"},
		{Text: "Tolak (Decline)", ID: ".decline MAP-ABCD"},
	}

	imageMsg := &waProto.ImageMessage{
		Mimetype: proto.String("image/png"),
	}

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
		ContentText: proto.String("Rincian Pesanan MAP-ABCD"),
		FooterText:  proto.String("Footer"),
		Buttons:     waButtons,
	}

	msg := &waProto.Message{
		ButtonsMessage: buttonsMsg,
	}

	// Verify ButtonsMessage fields
	if msg.GetButtonsMessage() == nil {
		t.Fatalf("expected ButtonsMessage not to be nil")
	}
	bm := msg.GetButtonsMessage()
	if bm.GetHeaderType() != waProto.ButtonsMessage_IMAGE {
		t.Errorf("expected headerType IMAGE, got %v", bm.GetHeaderType())
	}
	if bm.GetImageMessage() == nil {
		t.Errorf("expected ImageMessage in header, got nil")
	}
	if len(bm.GetButtons()) != 2 {
		t.Fatalf("expected 2 buttons, got %d", len(bm.GetButtons()))
	}
	if bm.GetButtons()[0].GetButtonID() != ".acc MAP-ABCD" {
		t.Errorf("expected button 0 id .acc MAP-ABCD, got %s", bm.GetButtons()[0].GetButtonID())
	}
	if bm.GetButtons()[0].GetButtonText().GetDisplayText() != "Setujui (ACC)" {
		t.Errorf("expected button 0 text Setujui (ACC), got %s", bm.GetButtons()[0].GetButtonText().GetDisplayText())
	}
	if bm.GetButtons()[1].GetButtonID() != ".decline MAP-ABCD" {
		t.Errorf("expected button 1 id .decline MAP-ABCD, got %s", bm.GetButtons()[1].GetButtonID())
	}
}

func TestExtractOrderIDFromQuotedButtons(t *testing.T) {
	w := &WAClient{}

	// Test 1: Quoted ButtonsMessage
	evtWithButtonsQuote := &events.Message{
		Message: &waProto.Message{
			ButtonsResponseMessage: &waProto.ButtonsResponseMessage{
				SelectedButtonID: proto.String(".acc MAP-1234"),
				ContextInfo: &waProto.ContextInfo{
					QuotedMessage: &waProto.Message{
						ButtonsMessage: &waProto.ButtonsMessage{
							ContentText: proto.String("Rincian Pesanan:\nOrder ID : MAP-1234\nPengirim : 628123"),
						},
					},
				},
			},
		},
	}
	extracted := w.extractOrderIDFromEvent(evtWithButtonsQuote)
	if extracted != "MAP-1234" {
		t.Errorf("expected MAP-1234 extracted from quoted buttons message, got %s", extracted)
	}

	// Test 2: Quoted TemplateMessage
	evtWithTemplateQuote := &events.Message{
		Message: &waProto.Message{
			TemplateButtonReplyMessage: &waProto.TemplateButtonReplyMessage{
				SelectedID: proto.String(".decline MAP-5678"),
				ContextInfo: &waProto.ContextInfo{
					QuotedMessage: &waProto.Message{
						TemplateMessage: &waProto.TemplateMessage{
							Format: &waProto.TemplateMessage_HydratedFourRowTemplate_{
								HydratedFourRowTemplate: &waProto.TemplateMessage_HydratedFourRowTemplate{
									HydratedContentText: proto.String("Rincian Pesanan:\nOrder ID : MAP-5678"),
								},
							},
						},
					},
				},
			},
		},
	}
	extracted = w.extractOrderIDFromEvent(evtWithTemplateQuote)
	if extracted != "MAP-5678" {
		t.Errorf("expected MAP-5678 extracted from quoted template message, got %s", extracted)
	}

	// Test 3: Quoted ImageMessage (standard moderation image message reply)
	evtWithImageQuote := &events.Message{
		Message: &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String("acc"),
				ContextInfo: &waProto.ContextInfo{
					QuotedMessage: &waProto.Message{
						ImageMessage: &waProto.ImageMessage{
							Caption: proto.String("Ada Order Gambar Baru!\n\nRincian Pesanan:\nOrder ID    : MAP-9ABC\nPengirim    : 628123456\nNama Map    : logo\nUkuran      : 2x2 (4 tile)\nTotal Biaya : Rp 4.000\n\nCara Moderasi:\nReply pesan ini dengan ketik:\n- acc : untuk menyetujui pesanan\n- reject [alasan] : untuk menolak pesanan"),
						},
					},
				},
			},
		},
	}
	extracted = w.extractOrderIDFromEvent(evtWithImageQuote)
	if extracted != "MAP-9ABC" {
		t.Errorf("expected MAP-9ABC extracted from quoted image message caption, got %s", extracted)
	}
}

func TestTripaySignature(t *testing.T) {
	client := NewTripayClient("https://tripay.co.id/api-sandbox", "T1234", "api-key-xyz", "secret-private-key", "QRIS")
	sig := client.GenerateSignature("MAP-TEST-REF", 5000)

	// Signature harus 64 karakter hex (SHA256)
	if len(sig) != 64 {
		t.Fatalf("expected 64 hex characters signature, got %d chars: %s", len(sig), sig)
	}

	// Signature harus konsisten dan reproducible
	sig2 := client.GenerateSignature("MAP-TEST-REF", 5000)
	if sig != sig2 {
		t.Errorf("signature should be deterministic: %s != %s", sig, sig2)
	}

	// Beda amount atau ref harus menghasilkan signature berbeda
	sigDiff := client.GenerateSignature("MAP-TEST-REF", 7000)
	if sig == sigDiff {
		t.Errorf("different amount should produce different signature")
	}
}

func TestTripayValidateCallbackSignature(t *testing.T) {
	secretKey := "my-secret-key"
	client := NewTripayClient("https://tripay.co.id/api-sandbox", "T1234", "api-key-xyz", secretKey, "QRIS")
	body := []byte(`{"reference":"DEV-123","merchant_ref":"MAP-001","status":"PAID"}`)

	// Hitung HMAC-SHA256 yang valid dari raw body
	h := hmac.New(sha256.New, []byte(secretKey))
	h.Write(body)
	validSig := hex.EncodeToString(h.Sum(nil))

	if !client.ValidateCallbackSignature(body, validSig) {
		t.Errorf("expected valid signature to pass validation")
	}

	if client.ValidateCallbackSignature(body, "invalid_signature_hex") {
		t.Errorf("expected invalid signature to fail validation")
	}

	tamperedBody := []byte(`{"reference":"DEV-123","merchant_ref":"MAP-001","status":"FAILED"}`)
	if client.ValidateCallbackSignature(tamperedBody, validSig) {
		t.Errorf("expected tampered body to fail validation with original signature")
	}
}

func TestTripayClientAPI(t *testing.T) {
	// Mock HTTP Server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/transaction/create":
			if r.Method != http.MethodPost {
				t.Errorf("expected POST, got %s", r.Method)
			}
			auth := r.Header.Get("Authorization")
			if auth != "Bearer test-api-key" {
				t.Errorf("expected Bearer test-api-key, got %s", auth)
			}

			resp := TripayCreateResponse{
				Success: true,
				Data: TripayTransactionData{
					Reference:   "DEV-T0001000000000000006",
					MerchantRef: "MAP-ORDER1",
					Amount:      5000,
					Status:      "UNPAID",
					QRString:    "00020101021226590014ID.LINKAJA.WWW01189360000201100000005204581253033605802ID5911Toko Dummy6007BANDUNG61054011562070703A01630467A3",
					ExpiredTime: time.Now().Add(15 * time.Minute).Unix(),
				},
			}
			_ = json.NewEncoder(w).Encode(resp)

		case "/transaction/detail":
			if r.Method != http.MethodGet {
				t.Errorf("expected GET, got %s", r.Method)
			}
			ref := r.URL.Query().Get("reference")
			if ref != "DEV-T0001000000000000006" {
				t.Errorf("expected reference DEV-T0001000000000000006, got %s", ref)
			}

			resp := TripayDetailResponse{
				Success: true,
				Data: TripayTransactionData{
					Reference:   ref,
					MerchantRef: "MAP-ORDER1",
					Amount:      5000,
					Status:      "PAID",
				},
			}
			_ = json.NewEncoder(w).Encode(resp)

		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewTripayClient(server.URL, "T9999", "test-api-key", "test-private-key", "QRIS")

	// 1. Test CreateClosedTransaction
	ctx := context.Background()
	createResp, err := client.CreateClosedTransaction(ctx, "MAP-ORDER1", "logo", 5000, "628123456789")
	if err != nil {
		t.Fatalf("CreateClosedTransaction failed: %v", err)
	}
	if !createResp.Success || createResp.Data.Reference != "DEV-T0001000000000000006" {
		t.Errorf("unexpected create response: %+v", createResp)
	}
	if createResp.Data.QRString == "" {
		t.Errorf("expected qr_string to be populated")
	}

	// 2. Test CheckTransactionStatus
	detailResp, err := client.CheckTransactionStatus(ctx, "DEV-T0001000000000000006")
	if err != nil {
		t.Fatalf("CheckTransactionStatus failed: %v", err)
	}
	if !detailResp.Success || detailResp.Data.Status != "PAID" {
		t.Errorf("unexpected detail response: %+v", detailResp)
	}
}

func TestIsReplyingToUsernamePrompt(t *testing.T) {
	w := &WAClient{}
	order := &ImageMapOrder{
		PaymentID: "MAP-TEST1",
		Status:    "waiting_mc_username",
	}

	// 1. Pesan biasa tanpa reply (ContextInfo nil) -> HARUS FALSE
	evtPlain := &events.Message{
		Message: &waProto.Message{
			Conversation: proto.String("halo semua lagi ngapain"),
		},
	}
	if w.isReplyingToUsernamePrompt(evtPlain, order) {
		t.Errorf("expected false for plain chat without reply, got true")
	}

	// 2. Pesan me-reply chat orang lain di grup (bukan bot prompt) -> HARUS FALSE
	evtReplyFriend := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				IsGroup: true,
			},
		},
		Message: &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String("iya nanti kita main bareng"),
				ContextInfo: &waProto.ContextInfo{
					StanzaID:    proto.String("msg-friend-123"),
					Participant: proto.String("628111111111@s.whatsapp.net"),
					QuotedMessage: &waProto.Message{
						Conversation: proto.String("mabar nanti malam yuk"),
					},
				},
			},
		},
	}
	if w.isReplyingToUsernamePrompt(evtReplyFriend, order) {
		t.Errorf("expected false for replying to another user's chat, got true")
	}

	// 3. Pesan me-reply chat server minecraft (*|Server|* player: halo) -> HARUS FALSE
	evtReplyServer := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				IsGroup: true,
			},
		},
		Message: &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String("halo juga"),
				ContextInfo: &waProto.ContextInfo{
					StanzaID:    proto.String("msg-server-123"),
					Participant: proto.String("628999999999@s.whatsapp.net"),
					QuotedMessage: &waProto.Message{
						Conversation: proto.String("*|Server|* [VIP] Player1: halo semua"),
					},
				},
			},
		},
	}
	if w.isReplyingToUsernamePrompt(evtReplyServer, order) {
		t.Errorf("expected false for replying to Minecraft game chat, got true")
	}

	// 4. Pesan me-reply prompt bot yang meminta Username Minecraft -> HARUS TRUE
	evtReplyPrompt := &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{
				IsGroup: true,
			},
		},
		Message: &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text: proto.String("Steve_Pro"),
				ContextInfo: &waProto.ContextInfo{
					StanzaID:    proto.String("msg-prompt-123"),
					Participant: proto.String("628999999999@s.whatsapp.net"),
					QuotedMessage: &waProto.Message{
						Conversation: proto.String("Pembayaran Berhasil\n\nOrder ID: MAP-TEST1\nSilakan balas pesan ini dengan *Username Minecraft* Anda agar map dapat diklaim di dalam game."),
					},
				},
			},
		},
	}
	if !w.isReplyingToUsernamePrompt(evtReplyPrompt, order) {
		t.Errorf("expected true when replying to username prompt, got false")
	}
}


