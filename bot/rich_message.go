package main

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/proto/waAICommon"
	"go.mau.fi/whatsmeow/proto/waAICommonDeprecated"
	waProto "go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

// CodeBlockPart merepresentasikan potongan kode beserta jenis highlight-nya.
type CodeBlockPart struct {
	HighlightType int
	Content       string
}

// ── UX Primitive Structs untuk Unified Response ──────────────────────────────

type SectionViewModel struct {
	ViewModel SectionLayout `json:"view_model"`
}

type SectionLayout struct {
	Primitive interface{} `json:"primitive"`
	Typename  string      `json:"__typename"`
}

type MarkdownTextUXPrimitive struct {
	Text           string        `json:"text"`
	InlineEntities []interface{} `json:"inline_entities"`
	Typename       string        `json:"__typename"`
}

type TableUXPrimitive struct {
	Title    string       `json:"title,omitempty"`
	Rows     []TableUXRow `json:"rows"`
	Typename string       `json:"__typename"`
}

type TableUXRow struct {
	IsHeader      bool               `json:"is_header"`
	Cells         []string           `json:"cells"`
	MarkdownCells []MarkdownCellItem `json:"markdown_cells,omitempty"`
}

type MarkdownCellItem struct {
	Text string `json:"text"`
}

type CodeUXPrimitive struct {
	Language   string        `json:"language"`
	CodeBlocks []CodeUXBlock `json:"code_blocks"`
	Typename   string        `json:"__typename"`
}

type CodeUXBlock struct {
	Content string `json:"content"`
	Type    string `json:"type"`
}

type UnifiedResponsePayload struct {
	ResponseID string             `json:"response_id"`
	Sections   []SectionViewModel `json:"sections"`
}

// ── Submessage Constructors ──────────────────────────────────────────────────

// NewRichTextSubMessage membuat submessage tipe TEXT (Markdown).
func NewRichTextSubMessage(markdown string) *waAICommonDeprecated.AIRichResponseSubMessage {
	return &waAICommonDeprecated.AIRichResponseSubMessage{
		MessageType: waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT.Enum(),
		MessageText: proto.String(markdown),
	}
}

// NewRichTableSubMessage membuat submessage tipe TABLE.
func NewRichTableSubMessage(title string, headers []string, rows [][]string) *waAICommonDeprecated.AIRichResponseSubMessage {
	var tableRows []*waAICommonDeprecated.AIRichResponseTableMetadata_AIRichResponseTableRow

	// Header row
	if len(headers) > 0 {
		tableRows = append(tableRows, &waAICommonDeprecated.AIRichResponseTableMetadata_AIRichResponseTableRow{
			Items:     headers,
			IsHeading: proto.Bool(true),
		})
	}

	// Data rows
	for _, r := range rows {
		tableRows = append(tableRows, &waAICommonDeprecated.AIRichResponseTableMetadata_AIRichResponseTableRow{
			Items:     r,
			IsHeading: proto.Bool(false),
		})
	}

	tableMeta := &waAICommonDeprecated.AIRichResponseTableMetadata{
		Rows: tableRows,
	}
	if title != "" {
		tableMeta.Title = proto.String(title)
	}

	return &waAICommonDeprecated.AIRichResponseSubMessage{
		MessageType:   waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TABLE.Enum(),
		TableMetadata: tableMeta,
	}
}

// NewRichCodeBlockSubMessage membuat submessage tipe CODE dengan satu blok kode utuh.
func NewRichCodeBlockSubMessage(language string, code string) *waAICommonDeprecated.AIRichResponseSubMessage {
	codeMeta := &waAICommonDeprecated.AIRichResponseCodeMetadata{
		CodeBlocks: []*waAICommonDeprecated.AIRichResponseCodeMetadata_AIRichResponseCodeBlock{
			{
				CodeContent: proto.String(code),
			},
		},
	}
	if language != "" {
		codeMeta.CodeLanguage = proto.String(language)
	}

	return &waAICommonDeprecated.AIRichResponseSubMessage{
		MessageType:  waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_CODE.Enum(),
		CodeMetadata: codeMeta,
	}
}

// NewRichCodeBlockWithHighlights membuat submessage tipe CODE dengan potongan token syntax highlighting.
func NewRichCodeBlockWithHighlights(language string, blocks []CodeBlockPart) *waAICommonDeprecated.AIRichResponseSubMessage {
	var protoBlocks []*waAICommonDeprecated.AIRichResponseCodeMetadata_AIRichResponseCodeBlock
	for _, b := range blocks {
		ht := waAICommonDeprecated.AIRichResponseCodeMetadata_AIRichResponseCodeHighlightType(b.HighlightType)
		protoBlocks = append(protoBlocks, &waAICommonDeprecated.AIRichResponseCodeMetadata_AIRichResponseCodeBlock{
			HighlightType: ht.Enum(),
			CodeContent:   proto.String(b.Content),
		})
	}

	codeMeta := &waAICommonDeprecated.AIRichResponseCodeMetadata{
		CodeBlocks: protoBlocks,
	}
	if language != "" {
		codeMeta.CodeLanguage = proto.String(language)
	}

	return &waAICommonDeprecated.AIRichResponseSubMessage{
		MessageType:  waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_CODE.Enum(),
		CodeMetadata: codeMeta,
	}
}

// ── Unified Response Builder (UX Primitives) ─────────────────────────────────

// buildUnifiedResponse membangun UX Primitive payload JSON (GenAIMarkdownTextUXPrimitive, GenATableUXPrimitive, GenAICodeUXPrimitive).
func buildUnifiedResponse(submessages []*waAICommonDeprecated.AIRichResponseSubMessage, uuid string) *waAICommon.AIRichResponseUnifiedResponse {
	var sections []SectionViewModel

	for _, sm := range submessages {
		if sm == nil {
			continue
		}

		switch sm.GetMessageType() {
		case waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT:
			sections = append(sections, SectionViewModel{
				ViewModel: SectionLayout{
					Primitive: MarkdownTextUXPrimitive{
						Text:           sm.GetMessageText(),
						InlineEntities: []interface{}{},
						Typename:       "GenAIMarkdownTextUXPrimitive",
					},
					Typename: "GenAISingleLayoutViewModel",
				},
			})

		case waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TABLE:
			meta := sm.GetTableMetadata()
			if meta != nil {
				var tableRows []TableUXRow
				for _, r := range meta.GetRows() {
					var mdCells []MarkdownCellItem
					for _, item := range r.GetItems() {
						mdCells = append(mdCells, MarkdownCellItem{Text: item})
					}
					tableRows = append(tableRows, TableUXRow{
						IsHeader:      r.GetIsHeading(),
						Cells:         r.GetItems(),
						MarkdownCells: mdCells,
					})
				}
				sections = append(sections, SectionViewModel{
					ViewModel: SectionLayout{
						Primitive: TableUXPrimitive{
							Title:    meta.GetTitle(),
							Rows:     tableRows,
							Typename: "GenATableUXPrimitive",
						},
						Typename: "GenAISingleLayoutViewModel",
					},
				})
			}

		case waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_CODE:
			codeMeta := sm.GetCodeMetadata()
			if codeMeta != nil {
				var codeBlocks []CodeUXBlock
				for _, cb := range codeMeta.GetCodeBlocks() {
					codeBlocks = append(codeBlocks, CodeUXBlock{
						Content: cb.GetCodeContent(),
						Type:    "DEFAULT",
					})
				}
				lang := codeMeta.GetCodeLanguage()
				if lang == "" {
					lang = "text"
				}
				sections = append(sections, SectionViewModel{
					ViewModel: SectionLayout{
						Primitive: CodeUXPrimitive{
							Language:   lang,
							CodeBlocks: codeBlocks,
							Typename:   "GenAICodeUXPrimitive",
						},
						Typename: "GenAISingleLayoutViewModel",
					},
				})
			}
		}
	}

	if len(sections) == 0 {
		return nil
	}

	payload := UnifiedResponsePayload{
		ResponseID: uuid,
		Sections:   sections,
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil
	}

	return &waAICommon.AIRichResponseUnifiedResponse{
		Data: jsonBytes,
	}
}

// ── Bot Metadata Certificate & Signature ─────────────────────────────────────


// makeRichContextInfo mengonfigurasi ContextInfo dengan atribut bot Meta AI (forwardOrigin = 4, forwardedAiBotMessageInfo).
func makeRichContextInfo(replyTo *events.Message) *waProto.ContextInfo {
	ctxInfo := &waProto.ContextInfo{
		ForwardingScore: proto.Uint32(1),
		IsForwarded:     proto.Bool(true),
		ForwardOrigin:   waProto.ContextInfo_META_AI.Enum(),
		ForwardedAiBotMessageInfo: &waAICommon.ForwardedAIBotMessageInfo{
			BotJID: proto.String("867051314767696@bot"),
		},
	}

	if replyTo != nil {
		sender := replyTo.Info.Sender.ToNonAD().String()
		id := string(replyTo.Info.ID)
		ctxInfo.StanzaID = proto.String(id)
		ctxInfo.Participant = proto.String(sender)
		ctxInfo.QuotedMessage = replyTo.Message
	}

	return ctxInfo
}

// ── Core Send Function ───────────────────────────────────────────────────────

// SendRichMessage mengirimkan pesan rich AI ke WhatsApp.
// Menggunakan arsitektur native BotForwardedMessage + VerificationMetadata + UnifiedResponse UX Primitives.
func SendRichMessage(
	client *whatsmeow.Client,
	jid types.JID,
	submessages []*waAICommonDeprecated.AIRichResponseSubMessage,
	replyTo *events.Message,
) error {
	if client == nil {
		return ErrWANotConnected
	}

	uuid := generateUUID()
	ctxInfo := makeRichContextInfo(replyTo)
	fallback := FormatSubmessagesAsFallback(submessages)
	unifiedResp := buildUnifiedResponse(submessages, uuid)

	airm := &waProto.AIRichResponseMessage{
		MessageType:     waAICommonDeprecated.AIRichResponseMessageType_AI_RICH_RESPONSE_TYPE_STANDARD.Enum(),
		Submessages:     submessages,
		UnifiedResponse: unifiedResp,
		ContextInfo:     ctxInfo,
	}

	richMsg := &waProto.Message{
		BotForwardedMessage: &waProto.FutureProofMessage{
			Message: &waProto.Message{
				RichResponseMessage: airm,
			},
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := client.SendMessage(ctx, jid, richMsg)
	if err != nil {
		fmt.Printf("[WA-RichMessage] Gagal kirim RichMessage (%v), fallback ke plain text...\n", err)
		return sendFallbackText(client, jid, fallback, ctxInfo)
	}

	fmt.Printf("[WA-RichMessage] Berhasil kirim RichMessage ke %s (ID: %s, ServerID: %d)\n", jid, resp.ID, resp.ServerID)
	return nil
}

// SendRichText mengirimkan satu pesan teks markdown sebagai rich message.
func SendRichText(client *whatsmeow.Client, jid types.JID, markdown string) error {
	return SendRichMessage(client, jid, []*waAICommonDeprecated.AIRichResponseSubMessage{
		NewRichTextSubMessage(markdown),
	}, nil)
}

// SendRichTable mengirimkan tabel rich message standar.
func SendRichTable(client *whatsmeow.Client, jid types.JID, title string, headers []string, rows [][]string) error {
	return SendRichMessage(client, jid, []*waAICommonDeprecated.AIRichResponseSubMessage{
		NewRichTableSubMessage(title, headers, rows),
	}, nil)
}

// SendRichTableWithQuote mengirimkan tabel rich message dengan me-reply pesan target.
func SendRichTableWithQuote(client *whatsmeow.Client, jid types.JID, title string, headers []string, rows [][]string, replyTo *events.Message) error {
	return SendRichMessage(client, jid, []*waAICommonDeprecated.AIRichResponseSubMessage{
		NewRichTableSubMessage(title, headers, rows),
	}, replyTo)
}

// SendRichCodeBlock mengirimkan blok kode sebagai rich message.
func SendRichCodeBlock(client *whatsmeow.Client, jid types.JID, language string, code string) error {
	return SendRichMessage(client, jid, []*waAICommonDeprecated.AIRichResponseSubMessage{
		NewRichCodeBlockSubMessage(language, code),
	}, nil)
}

// sendFallbackText mengirimkan teks via Conversation / ExtendedTextMessage standar.
func sendFallbackText(client *whatsmeow.Client, jid types.JID, text string, ctxInfo *waProto.ContextInfo) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	var msg *waProto.Message
	if ctxInfo != nil {
		msg = &waProto.Message{
			ExtendedTextMessage: &waProto.ExtendedTextMessage{
				Text:        proto.String(text),
				ContextInfo: ctxInfo,
			},
		}
	} else {
		msg = &waProto.Message{
			Conversation: proto.String(text),
		}
	}
	_, err := client.SendMessage(ctx, jid, msg)
	return err
}

// FormatSubmessagesAsFallback merangkai semua submessage menjadi satu teks Markdown/WhatsApp yang rapi.
func FormatSubmessagesAsFallback(submessages []*waAICommonDeprecated.AIRichResponseSubMessage) string {
	var sb strings.Builder
	for i, sub := range submessages {
		if sub == nil {
			continue
		}
		switch sub.GetMessageType() {
		case waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT:
			sb.WriteString(sub.GetMessageText())
		case waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TABLE:
			meta := sub.GetTableMetadata()
			if meta != nil {
				var headers []string
				var rows [][]string
				for _, row := range meta.GetRows() {
					if row.GetIsHeading() {
						headers = row.GetItems()
					} else {
						rows = append(rows, row.GetItems())
					}
				}
				sb.WriteString(FormatTableAsFallbackText(meta.GetTitle(), headers, rows))
			}
		case waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_CODE:
			codeMeta := sub.GetCodeMetadata()
			if codeMeta != nil {
				lang := codeMeta.GetCodeLanguage()
				var codeText strings.Builder
				for _, block := range codeMeta.GetCodeBlocks() {
					codeText.WriteString(block.GetCodeContent())
				}
				sb.WriteString(fmt.Sprintf("```%s\n%s\n```", lang, codeText.String()))
			}
		}
		if i < len(submessages)-1 {
			sb.WriteString("\n\n")
		}
	}
	return sb.String()
}

// FormatTableAsFallbackText memformat tabel menjadi teks pesan WhatsApp yang rapi dan mudah dibaca.
func FormatTableAsFallbackText(title string, headers []string, rows [][]string) string {
	var sb strings.Builder
	if title != "" {
		sb.WriteString(fmt.Sprintf("*%s*\n━━━━━━━━━━━━━━━━━━━━━\n", strings.ToUpper(title)))
	}

	if len(rows) == 0 {
		sb.WriteString("_No data._\n━━━━━━━━━━━━━━━━━━━━━")
		return sb.String()
	}

	for _, row := range rows {
		if len(row) == 2 {
			// Key-Value table (contoh: Server Status, Info Akun)
			sb.WriteString(fmt.Sprintf("• *%s:* %s\n", row[0], row[1]))
		} else if len(row) == 3 && len(headers) == 3 {
			// 3-kolom table (contoh: Rank, Player, Distance)
			sb.WriteString(fmt.Sprintf("• *%s* %s — %s\n", row[0], row[1], row[2]))
		} else {
			sb.WriteString(fmt.Sprintf("• %s\n", strings.Join(row, " | ")))
		}
	}
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━")
	return sb.String()
}

// generateUUID membuat ID acak berbentuk UUID v4.
func generateUUID() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// LeaderboardEntry merepresentasikan satu baris pada leaderboard (Rank, Player, Count).
type LeaderboardEntry struct {
	Rank   string
	Player string
	Count  string // Jumlah Elytra (contoh: "3 elytra")
}
