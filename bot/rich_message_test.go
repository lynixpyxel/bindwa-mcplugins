package main

import (
	"strings"
	"testing"

	"go.mau.fi/whatsmeow/proto/waAICommon"
	"go.mau.fi/whatsmeow/proto/waAICommonDeprecated"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"google.golang.org/protobuf/proto"
)

func TestFormatTableAsFallbackText(t *testing.T) {
	title := "Server Status"
	headers := []string{"Parameter", "Nilai"}
	rows := [][]string{
		{"Server", "Survival SMP"},
		{"Status", "ONLINE"},
		{"TPS", "20.00"},
	}

	formatted := FormatTableAsFallbackText(title, headers, rows)
	if !strings.Contains(formatted, "SERVER STATUS") {
		t.Errorf("Expected title in uppercase, got: %s", formatted)
	}
	if !strings.Contains(formatted, "• *Server:* Survival SMP") {
		t.Errorf("Expected row formatted with header, got: %s", formatted)
	}
	if !strings.Contains(formatted, "• *TPS:* 20.00") {
		t.Errorf("Expected TPS row, got: %s", formatted)
	}
}

func TestAIRichResponseProtobufStructure(t *testing.T) {
	// 1. Test Text SubMessage
	textSubMsg := &waAICommonDeprecated.AIRichResponseSubMessage{
		MessageType: waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT.Enum(),
		MessageText: proto.String("**Bold Text** and list:\n* item 1\n* item 2"),
	}

	if textSubMsg.GetMessageType() != waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT {
		t.Errorf("Expected AI_RICH_RESPONSE_TEXT message type")
	}
	if !strings.Contains(textSubMsg.GetMessageText(), "Bold Text") {
		t.Errorf("Expected message text content")
	}

	// 2. Test Table SubMessage
	headers := []string{"Rank", "Player", "Distance"}
	rows := [][]string{
		{"#1", "Steve", "5 elytra"},
		{"#2", "Alex", "3 elytra"},
	}

	var tableRows []*waAICommonDeprecated.AIRichResponseTableMetadata_AIRichResponseTableRow
	tableRows = append(tableRows, &waAICommonDeprecated.AIRichResponseTableMetadata_AIRichResponseTableRow{
		Items:     headers,
		IsHeading: proto.Bool(true),
	})
	for _, r := range rows {
		tableRows = append(tableRows, &waAICommonDeprecated.AIRichResponseTableMetadata_AIRichResponseTableRow{
			Items:     r,
			IsHeading: proto.Bool(false),
		})
	}

	tableSubMsg := &waAICommonDeprecated.AIRichResponseSubMessage{
		MessageType: waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TABLE.Enum(),
		TableMetadata: &waAICommonDeprecated.AIRichResponseTableMetadata{
			Title: proto.String("Leaderboard Elytra"),
			Rows:  tableRows,
		},
	}

	if tableSubMsg.GetMessageType() != waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TABLE {
		t.Errorf("Expected AI_RICH_RESPONSE_TABLE message type")
	}
	if len(tableSubMsg.GetTableMetadata().GetRows()) != 3 {
		t.Errorf("Expected 3 rows (1 header + 2 data), got %d", len(tableSubMsg.GetTableMetadata().GetRows()))
	}
	if !tableSubMsg.GetTableMetadata().GetRows()[0].GetIsHeading() {
		t.Errorf("Expected first row to have isHeading = true")
	}

	// 3. Test Code Block SubMessage
	codeSubMsg := &waAICommonDeprecated.AIRichResponseSubMessage{
		MessageType: waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_CODE.Enum(),
		CodeMetadata: &waAICommonDeprecated.AIRichResponseCodeMetadata{
			CodeLanguage: proto.String("json"),
			CodeBlocks: []*waAICommonDeprecated.AIRichResponseCodeMetadata_AIRichResponseCodeBlock{
				{
					CodeContent: proto.String(`{"status": "ok"}`),
				},
			},
		},
	}

	if codeSubMsg.GetMessageType() != waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_CODE {
		t.Errorf("Expected AI_RICH_RESPONSE_CODE message type")
	}
	if codeSubMsg.GetCodeMetadata().GetCodeLanguage() != "json" {
		t.Errorf("Expected language 'json'")
	}

	// 4. Test Envelope in waProto.Message
	msg := &waProto.Message{
		RichResponseMessage: &waProto.AIRichResponseMessage{
			MessageType: waAICommonDeprecated.AIRichResponseMessageType_AI_RICH_RESPONSE_TYPE_STANDARD.Enum(),
			Submessages: []*waAICommonDeprecated.AIRichResponseSubMessage{
				textSubMsg,
				tableSubMsg,
				codeSubMsg,
			},
		},
	}

	if msg.GetRichResponseMessage() == nil {
		t.Fatalf("Expected RichResponseMessage to not be nil")
	}
	if msg.GetRichResponseMessage().GetMessageType() != waAICommonDeprecated.AIRichResponseMessageType_AI_RICH_RESPONSE_TYPE_STANDARD {
		t.Errorf("Expected AI_RICH_RESPONSE_TYPE_STANDARD")
	}
	if len(msg.GetRichResponseMessage().GetSubmessages()) != 3 {
		t.Errorf("Expected 3 submessages, got %d", len(msg.GetRichResponseMessage().GetSubmessages()))
	}
}

func TestUpdateLeaderboardFromText(t *testing.T) {
	client := &WAClient{}
	input := "*Leaderboard Elytra*\n1. Steve — 5 elytra\n2. Alex — 3 elytra\n3. Bob — 1 elytra"

	client.UpdateLeaderboardFromText(input)

	if len(client.latestLeaderboard) != 3 {
		t.Fatalf("Expected 3 leaderboard entries, got %d", len(client.latestLeaderboard))
	}

	e1 := client.latestLeaderboard[0]
	if e1.Rank != "#1" || e1.Player != "Steve" || e1.Count != "5 elytra" {
		t.Errorf("Entry 1 mismatch: %+v", e1)
	}

	e2 := client.latestLeaderboard[1]
	if e2.Rank != "#2" || e2.Player != "Alex" || e2.Count != "3 elytra" {
		t.Errorf("Entry 2 mismatch: %+v", e2)
	}
}

func TestSendRichMessageMultiSubmessagesAndFallback(t *testing.T) {
	submessages := []*waAICommonDeprecated.AIRichResponseSubMessage{
		NewRichTextSubMessage("# 🤖 *AI Response*\n\nIni contoh teks **markdown** yang dirender native."),
		NewRichTableSubMessage("Harga", []string{"Item", "Harga"}, [][]string{
			{"Baileys Pro", "$49"},
		}),
		NewRichCodeBlockWithHighlights("python", []CodeBlockPart{
			{HighlightType: 1, Content: "print"},
			{HighlightType: 0, Content: `("Hello")`},
		}),
		NewRichTextSubMessage("> _Powered by Baileys_"),
	}

	if len(submessages) != 4 {
		t.Fatalf("Expected 4 submessages, got %d", len(submessages))
	}

	fallback := FormatSubmessagesAsFallback(submessages)
	if !strings.Contains(fallback, "AI Response") {
		t.Errorf("Expected fallback to contain AI Response, got:\n%s", fallback)
	}
	if !strings.Contains(fallback, "HARGA") {
		t.Errorf("Expected fallback to contain HARGA, got:\n%s", fallback)
	}
	if !strings.Contains(fallback, "```python") {
		t.Errorf("Expected fallback to contain python code block, got:\n%s", fallback)
	}
	if !strings.Contains(fallback, "> _Powered by Baileys_") {
		t.Errorf("Expected fallback to contain footer, got:\n%s", fallback)
	}
}

func TestBuildDinoMessage(t *testing.T) {
	msg := BuildDinoMessage(nil)
	if msg == nil {
		t.Fatalf("BuildDinoMessage returned nil")
	}

	// 1. Verify MessageContextInfo
	ctxInfo := msg.GetMessageContextInfo()
	if ctxInfo == nil {
		t.Fatalf("Expected MessageContextInfo to not be nil")
	}
	if ctxInfo.GetDeviceListMetadataVersion() != 2 {
		t.Errorf("Expected DeviceListMetadataVersion 2, got %d", ctxInfo.GetDeviceListMetadataVersion())
	}
	if ctxInfo.GetDeviceListMetadata() == nil {
		t.Errorf("Expected DeviceListMetadata to not be nil")
	}

	botMeta := ctxInfo.GetBotMetadata()
	if botMeta == nil {
		t.Fatalf("Expected BotMetadata to not be nil")
	}
	if botMeta.GetBotResponseID() != "b2e40280-433c-45d8-9c1a-270bec558860" {
		t.Errorf("Unexpected BotResponseID: %s", botMeta.GetBotResponseID())
	}

	// 2. Verify Verification Metadata & Proofs
	verifMeta := botMeta.GetVerificationMetadata()
	if verifMeta == nil {
		t.Fatalf("Expected VerificationMetadata to not be nil")
	}
	proofs := verifMeta.GetProofs()
	if len(proofs) != 1 {
		t.Fatalf("Expected 1 proof, got %d", len(proofs))
	}
	p := proofs[0]
	if p.GetVersion() != 1 {
		t.Errorf("Expected proof version 1, got %d", p.GetVersion())
	}
	if p.GetUseCase() != waAICommon.BotSignatureVerificationUseCaseProof_WA_BOT_MSG {
		t.Errorf("Expected proof useCase WA_BOT_MSG, got %v", p.GetUseCase())
	}
	if len(p.GetSignature()) != 64 {
		t.Errorf("Expected signature length 64 bytes, got %d", len(p.GetSignature()))
	}
	if len(p.GetCertificateChain()) != 2 {
		t.Errorf("Expected 2 certificates in chain, got %d", len(p.GetCertificateChain()))
	}

	// 3. Verify BotForwardedMessage & RichResponseMessage
	bfm := msg.GetBotForwardedMessage()
	if bfm == nil || bfm.GetMessage() == nil {
		t.Fatalf("Expected BotForwardedMessage.Message to not be nil")
	}
	richMsg := bfm.GetMessage().GetRichResponseMessage()
	if richMsg == nil {
		t.Fatalf("Expected RichResponseMessage to not be nil")
	}
	if richMsg.GetMessageType() != waAICommonDeprecated.AIRichResponseMessageType_AI_RICH_RESPONSE_TYPE_STANDARD {
		t.Errorf("Expected message type AI_RICH_RESPONSE_TYPE_STANDARD, got %v", richMsg.GetMessageType())
	}
	if len(richMsg.GetSubmessages()) != 1 {
		t.Fatalf("Expected 1 submessage, got %d", len(richMsg.GetSubmessages()))
	}
	if richMsg.GetSubmessages()[0].GetMessageText() != "Fiora Sylvie" {
		t.Errorf("Unexpected submessage text: %s", richMsg.GetSubmessages()[0].GetMessageText())
	}

	// 4. Verify UnifiedResponse HTML Primitive
	unified := richMsg.GetUnifiedResponse()
	if unified == nil || len(unified.GetData()) == 0 {
		t.Fatalf("Expected UnifiedResponse Data to not be empty")
	}
	dataStr := string(unified.GetData())
	if !strings.Contains(dataStr, "GenAIaeacdsnwHtmlPrimitive") {
		t.Errorf("Expected UnifiedResponse Data to contain GenAIaeacdsnwHtmlPrimitive")
	}
	if !strings.Contains(dataStr, "Dino Runner") {
		t.Errorf("Expected UnifiedResponse Data to contain Dino Runner")
	}
	if !strings.Contains(dataStr, "trusted_sources") {
		t.Errorf("Expected UnifiedResponse Data to contain trusted_sources")
	}

	// 5. Verify ContextInfo
	rcCtx := richMsg.GetContextInfo()
	if rcCtx == nil {
		t.Fatalf("Expected ContextInfo on RichResponseMessage to not be nil")
	}
	if !rcCtx.GetIsForwarded() {
		t.Errorf("Expected IsForwarded = true")
	}
	if rcCtx.GetForwardOrigin() != waProto.ContextInfo_META_AI {
		t.Errorf("Expected ForwardOrigin META_AI, got %v", rcCtx.GetForwardOrigin())
	}
	if rcCtx.GetForwardedAiBotMessageInfo().GetBotJID() != "867051314767696@bot" {
		t.Errorf("Unexpected BotJID: %s", rcCtx.GetForwardedAiBotMessageInfo().GetBotJID())
	}
}
