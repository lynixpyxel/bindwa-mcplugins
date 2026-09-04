package main

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	_ "image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skip2/go-qrcode"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
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
	cfg.ImageMapPricing = map[string]int{
		"1x1": 1000,
		"3x3": 2500,
	}
	cfg.ImageMapPricePerTile = 1000

	// 1x1 tiered
	if p := cfg.CalculateImageMapPrice(1, 1); p != 1000 {
		t.Errorf("expected 1x1 price 1000, got %d", p)
	}

	// 3x3 tiered
	if p := cfg.CalculateImageMapPrice(3, 3); p != 2500 {
		t.Errorf("expected 3x3 price 2500, got %d", p)
	}

	// 2x2 fallback: 4 * 1000 = 4000
	if p := cfg.CalculateImageMapPrice(2, 2); p != 4000 {
		t.Errorf("expected 2x2 price 4000, got %d", p)
	}

	// 1x2 fallback: 2 * 1000 = 2000
	if p := cfg.CalculateImageMapPrice(1, 2); p != 2000 {
		t.Errorf("expected 1x2 price 2000, got %d", p)
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
	cancelCh := mgr.ApproveOrder("MAP-1234", "trx-casaku-123", 4000)
	if cancelCh == nil {
		t.Fatalf("expected valid cancelCh from ApproveOrder")
	}
	if fetched.Status != "pending" {
		t.Errorf("expected status to be pending after approval, got %s", fetched.Status)
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


