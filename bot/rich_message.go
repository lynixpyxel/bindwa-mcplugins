package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
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

const (
	dinoSignatureB64 = "TklYRUwuTWVzc2FnZUJ1aWxkZXJWNC43LVZlcmlmaWNhdGlvblNpZ25hdHVyZS5NZXRhZGF0YeN55YRyad2+ZA=="
	dinoCert1B64     = "TklYRUwuTWVzc2FnZUJ1aWxkZXJWNC43LUNlcnRpZmljYXRlQ2hhaW4uTWV0YWRhdGEOvtJr968bbpKdZreOTwkk9aPN++XPE60RfuzNLkXXc7LE8BOkJOWRpo2oNXaRJ3uCNJ43HY3A+oetnvHSfcxWqmvvTSrBOI5V1NOD6RMsZ/st1XVPUx83AGps1l5jYBOYzqMNy6un2tToJ2Bt9bXRo29tWLZTu8m7TNY/hISwVpVc5tjSet5U7btPN+dMIx2UvykB1jcbWGsdklheeuz8RXSStNXzeaGvsf1lpZ/ugLE4b2BdmlRNKrY6zLE4qFtRYQoS7axOyQX+4QUyN2m9bfm7urQmn+QRSXJwMO7X5kAJJLbkVGJFt9Pm9VXPwQVrK2aaqiXlpusj+7DfDw00OULmYMmZDTqXM0nUVLxj13z0LhMQoQhhNG8utdUn4uKOFceliTZ/xiP+A54GnX9620641bqw3ctfh9NNXPsTEK8hAUD7FDqUhVntHmoEYYEHq8X1tHHZYP49/f2iezTiE8AUaoZo42/jIWQIKohOGNUib2hEqMkW8NsR8vPihvNuqPc0zKZcl6359YFQdjiiW8kCRD/rsDOr9v1eYLFZKYloFyzFqEgj+jcG/V47elOjShJ5CCPwatXwP6HIloVwtgygFsnOFmCg6Ojoivfoz8Nw1qxFwg5OU2cq/1WbWNELKnaFg4eUWCAIJ/3ZIJsEPkgemZxGhE+hdiNn9dkQYBJs1kx2BxdIkJmQ9vJSKkrMz6lTxZM3IJ9mhmKS6zYdU1ppeAao0/ayte997DQParb/AHLN79g0iW1ad0z8ir5jAl0q3a+UZPTSa4YiSqC2PZ/gfxG5wvL2mKmeKowG0RXjmEp5iNxrni+T/HRLZOoH7y0DQ24nMCPg"
	dinoCert2B64     = "TklYRUwuTWVzc2FnZUJ1aWxkZXJWNC43LUNlcnRpZmljYXRlQ2hhaW4uTWV0YWRhdGHsL0Ccm0ELINFZ2IaBhKaeWnVuh0o6nZLCioCn9xpSADzwIS5VCWO+1eVXT2atJOyf7FYlpB0/JA3Us+aQtekuIkHu/zBXijORZ4ClF4+sF3cSTNg6gY/+6iwLK/zs3bMg+GeJrcI65vXfs95Shxlb2Rd5GRT2/2yBmR6Zkf5QwMJuptUHWtM26WY7/xlkEKGFYDZVqOSylusiOzSALa815zC6dCiHoJNLBEKMlaZZQOk57/+OYoU5zzTaEgLhyvNFHSyAlyLQ3SGFtVHAaJZHSmmSPyJowCOB+92Gkk6SWVMsk6FbU8QJWFtlhzV/W/gZ7WzUlS/AKgN0th9/cq20ToFkW7X9c+rtYavufmuieqFhXgaMD8AGsoN9QC/HzNC9D1nydPfFYEUr9BHVy2nF5gM58Y59r2rT8p5LPARIkUp8g+5DLhyW0tdZFZ1305o4AHCayZnp5rjcU2Xi/c1Qf/djBGakmijlMs4aMzKJYD0c4Q8jdI7sNyd876K2wRD+L6KeD2QB3PtCS4P7BWAl5gh5CJ6ZBrwcaKXZqcSjEwm52MqVCgYZdapAaNYUy/QndttjLOG0wxxwuX1hIhMjPnIKZR1kwnqD5EqlHpilrnojRZvjVGN4zEKmilS8rNstt4HHs/D849W+Q6LRVWiWMs0cT2IugrX+Skxd8En7Gq52UEmuVBrSTpN+UpIu20NsVb9lsvuYh3XO441606tOEY2eKcZJdTtqrOTNqbbTk0zVn1yhbOCvmfctBNDhTwaC5QMi0P9wjU5XI9SBtkdQLizc5oqpoiHeqgb8+aJHVLcbgIJ/KLZKtRWFDfzRNM02Csx4etUUapVd2NA/L0oMs/O5T9sVj9FBJ7q99GWr3PVmxJb36mHZLXC4k1gGN9swE0LtzYsUdT5tUo9ri/hS3W/SM+F1p4Kh4QIgRcG3ciIHGN44bnDh3HDCz0fDnzKYw0bclMxZPctEyJ5gEOPF6OAkjD9dEaRGq/tEPf1k9Aub+v2dEjnfrYWAm4E5Zfhs2Xh0CT0k+SzhgKd0K/46ChJ20G5+blwpIvahvTVS68+aVIX6CwXs4tcVx6FnmVsMOOkIasfaqQLZYbNBkuLoZnQAq4j8yRekrQ=="
	dinoResponseID   = "b2e40280-433c-45d8-9c1a-270bec558860"
	dinoUnifiedB64   = "eyJyZXNwb25zZV9pZCI6IjRkYjU3YjJjLTgzOTMtNDg0ZC04YjlhLThlNmQxYTE0YjM0OSIsInNlY3Rpb25zIjpbeyJ2aWV3X21vZGVsIjp7InByaW1pdGl2ZSI6eyJfX3R5cGVuYW1lIjoiR2VuQUlhZWFjZHNud0h0bWxQcmltaXRpdmUiLCJwYXlsb2FkIjoiPHN0eWxlPip7LXdlYmtpdC10YXAtaGlnaGxpZ2h0LWNvbG9yOnRyYW5zcGFyZW50Oy13ZWJraXQtdXNlci1zZWxlY3Q6bm9uZTt1c2VyLXNlbGVjdDpub25lOy13ZWJraXQtdG91Y2gtY2FsbG91dDpub25lfTwvc3R5bGU+XG48Ym9keSBzdHlsZT1cIm1hcmdpbjowO2JhY2tncm91bmQ6dHJhbnNwYXJlbnQ7Zm9udC1mYW1pbHk6QXJpYWwsc2Fucy1zZXJpZjtjb2xvcjojZWVlO3RvdWNoLWFjdGlvbjptYW5pcHVsYXRpb247Y3Vyc29yOnBvaW50ZXJcIj5cbjxkaXYgc3R5bGU9XCJ3aWR0aDoxMDAlO21heC13aWR0aDo2MjBweDttYXJnaW46YXV0bztwYWRkaW5nOjE2cHg7Ym94LXNpemluZzpib3JkZXItYm94XCI+XG48ZGl2IHN0eWxlPVwiYmFja2dyb3VuZDpyZ2JhKDI1NSwyNTUsMjU1LC4wNik7YmFja2Ryb3AtZmlsdGVyOmJsdXIoMTRweCk7LXdlYmtpdC1iYWNrZHJvcC1maWx0ZXI6Ymx1cigxNHB4KTtib3JkZXI6MXB4IHNvbGlkIHJnYmEoMjU1LDI1NSwyNTUsLjE1KTtib3JkZXItcmFkaXVzOjE2cHg7b3ZlcmZsb3c6aGlkZGVuO2JveC1zaGFkb3c6MCA4cHggMzJweCByZ2JhKDAsMCwwLC4zNSlcIj5cbjxkaXYgc3R5bGU9XCJwYWRkaW5nOjE4cHggMjBweDtib3JkZXItYm90dG9tOjFweCBzb2xpZCByZ2JhKDI1NSwyNTUsMjU1LC4xMik7ZGlzcGxheTpmbGV4O2p1c3RpZnktY29udGVudDpzcGFjZS1iZXR3ZWVuO2FsaWduLWl0ZW1zOmNlbnRlclwiPlxuPGRpdj48ZGl2IHN0eWxlPVwiZm9udC1zaXplOjExcHg7bGV0dGVyLXNwYWNpbmc6MS41cHg7Y29sb3I6cmdiYSgyNTUsMjU1LDI1NSwuNDUpXCI+RElOTyBHQU1FPC9kaXY+PGRpdiBzdHlsZT1cImZvbnQtc2l6ZToyMXB4O2ZvbnQtd2VpZ2h0OmJvbGQ7Y29sb3I6I2ZmZlwiPkRpbm8gUnVubmVyPC9kaXY+PC9kaXY+XG48ZGl2IHN0eWxlPVwidGV4dC1hbGlnbjpyaWdodFwiPjxkaXYgaWQ9XCJzY29yZVwiIHN0eWxlPVwiZm9udC1zaXplOjE4cHg7Zm9udC13ZWlnaHQ6Ym9sZDtjb2xvcjojZmZmO3RleHQtc2hhZG93OjAgMCAxMHB4IHJnYmEoMTA4LDkyLDIzMSwuODUpO3RyYW5zaXRpb246dHJhbnNmb3JtIC4xNXNcIj4wMDAwMDwvZGl2PjxkaXYgaWQ9XCJiZXN0XCIgc3R5bGU9XCJmb250LXNpemU6MTBweDtjb2xvcjpyZ2JhKDI1NSwyNTUsMjU1LC40KTttYXJnaW4tdG9wOjJweFwiPkJFU1QgMDAwMDA8L2Rpdj48L2Rpdj5cbjwvZGl2PlxuPGRpdiBzdHlsZT1cInBhZGRpbmc6MThweFwiPlxuPGNhbnZhcyBpZD1cImdhbWVcIiB3aWR0aD1cIjU2MFwiIGhlaWdodD1cIjE5MFwiIHN0eWxlPVwid2lkdGg6MTAwJTtoZWlnaHQ6YXV0bztiYWNrZ3JvdW5kOnJnYmEoMjU1LDI1NSwyNTUsLjAzKTtib3JkZXI6MXB4IHNvbGlkIHJnYmEoMjU1LDI1NSwyNTUsLjEyKTtib3JkZXItcmFkaXVzOjEycHg7ZGlzcGxheTpibG9ja1wiPjwvY2FudmFzPlxuPGRpdiBpZD1cInN0YXR1c1wiIHN0eWxlPVwidGV4dC1hbGlnbjpjZW50ZXI7bWFyZ2luLXRvcDoxMHB4O2ZvbnQtc2l6ZToxMnB4O2NvbG9yOnJnYmEoMjU1LDI1NSwyNTUsLjU1KVwiPlNwZWVkIDUuMHg8L2Rpdj5cbjwvZGl2PjwvZGl2PjwvZGl2PlxuPHNjcmlwdD5cbmNvbnN0IGM9ZG9jdW1lbnQuZ2V0RWxlbWVudEJ5SWQoJ2dhbWUnKSx4PWMuZ2V0Q29udGV4dCgnMmQnKSxzY29yZUVsPWRvY3VtZW50LmdldEVsZW1lbnRCeUlkKCdzY29yZScpLGJlc3RFbD1kb2N1bWVudC5nZXRFbGVtZW50QnlJZCgnYmVzdCcpLHN0YXR1c0VsPWRvY3VtZW50LmdldEVsZW1lbnRCeUlkKCdzdGF0dXMnKTtcbmNvbnN0IEdZPTE3MDtcbmxldCBkLG8sY2xvdWRzLHBhcnRpY2xlcyxhbWJpZW50LHRyYWlsLHNjb3JlLGJlc3Q9MCxzcGVlZCxnYW1lT3ZlcixsYXN0LHNoYWtlLGZsYXNoLHJ1blQsc3Bhd25UaW1lcixtaWxlc3RvbmUsc3F1YXNoO1xuZnVuY3Rpb24gbG9hZEJlc3QoKXtcbmxldCB2YWxzPVtdO1xudHJ5e2xldCB2PWxvY2FsU3RvcmFnZS5nZXRJdGVtKCdkaW5vX2Jlc3QnKTtpZih2KXZhbHMucHVzaChwYXJzZUludCh2LDEwKSl9Y2F0Y2goZSl7fVxudHJ5e2xldCB2PXNlc3Npb25TdG9yYWdlLmdldEl0ZW0oJ2Rpbm9fYmVzdCcpO2lmKHYpdmFscy5wdXNoKHBhcnNlSW50KHYsMTApKX1jYXRjaChlKXt9XG50cnl7bGV0IG09ZG9jdW1lbnQuY29va2llLm1hdGNoKC8oPzpefDtzKilkaW5vX2Jlc3Q9KGQrKS8pO2lmKG0pdmFscy5wdXNoKHBhcnNlSW50KG1bMV0sMTApKX1jYXRjaChlKXt9XG5yZXR1cm4gdmFscy5sZW5ndGg/TWF0aC5tYXgoLi4udmFscy5maWx0ZXIodj0+IWlzTmFOKHYpKSk6MFxufVxuZnVuY3Rpb24gc2F2ZUJlc3Qodil7XG5sZXQgdmFsPVN0cmluZyhNYXRoLmZsb29yKHYpKTtcbnRyeXtsb2NhbFN0b3JhZ2Uuc2V0SXRlbSgnZGlub19iZXN0Jyx2YWwpfWNhdGNoKGUpe31cbnRyeXtzZXNzaW9uU3RvcmFnZS5zZXRJdGVtKCdkaW5vX2Jlc3QnLHZhbCl9Y2F0Y2goZSl7fVxudHJ5e2RvY3VtZW50LmNvb2tpZT0nZGlub19iZXN0PScrdmFsKyc7bWF4LWFnZT0zMTUzNjAwMDtwYXRoPS8nfWNhdGNoKGUpe31cbnRyeXtcbmxldCBycT1pbmRleGVkREIub3BlbignZGlub19kYicsMSk7XG5ycS5vbnVwZ3JhZGVuZWVkZWQ9KCk9PntycS5yZXN1bHQuY3JlYXRlT2JqZWN0U3RvcmUoJ2t2Jyl9O1xucnEub25zdWNjZXNzPSgpPT57dHJ5e3JxLnJlc3VsdC50cmFuc2FjdGlvbigna3YnLCdyZWFkd3JpdGUnKS5vYmplY3RTdG9yZSgna3YnKS5wdXQodmFsLCdkaW5vX2Jlc3QnKX1jYXRjaChlKXt9fVxufWNhdGNoKGUpe31cbn1cbmZ1bmN0aW9uIGxvYWRCZXN0QXN5bmMoY2Ipe1xudHJ5e1xubGV0IHJxPWluZGV4ZWREQi5vcGVuKCdkaW5vX2RiJywxKTtcbnJxLm9udXBncmFkZW5lZWRlZD0oKT0+e3JxLnJlc3VsdC5jcmVhdGVPYmplY3RTdG9yZSgna3YnKX07XG5ycS5vbnN1Y2Nlc3M9KCk9PntcbnRyeXtcbmxldCBncj1ycS5yZXN1bHQudHJhbnNhY3Rpb24oJ2t2JywncmVhZG9ubHknKS5vYmplY3RTdG9yZSgna3YnKS5nZXQoJ2Rpbm9fYmVzdCcpO1xuZ3Iub25zdWNjZXNzPSgpPT57aWYoZ3IucmVzdWx0KWNiKHBhcnNlSW50KGdyLnJlc3VsdCwxMCkpfVxufWNhdGNoKGUpe31cbn1cbn1jYXRjaChlKXt9XG59XG5iZXN0PWxvYWRCZXN0KCk7XG5sb2FkQmVzdEFzeW5jKHY9PntpZighaXNOYU4odikmJnY+YmVzdCl7YmVzdD12O2Jlc3RFbC50ZXh0Q29udGVudD0nQkVTVCAnK1N0cmluZyhNYXRoLmZsb29yKGJlc3QpKS5wYWRTdGFydCg1LCcwJyl9fSk7XG5mdW5jdGlvbiByZXNldCgpe1xuZD17eDo1NSx5OjEzMix3OjI3LGg6MzAsdnk6MCxqdW1waW5nOmZhbHNlfTtcbm89W107XG5jbG91ZHM9W3t4OjEyMCx5OjMyLHc6NDQsczouMzV9LHt4OjMwMCx5OjUyLHc6NjAsczouMjJ9LHt4OjQ2MCx5OjI2LHc6MzYsczouNH0se3g6NTYwLHk6NzAsdzo1MCxzOi4xOH1dO1xucGFydGljbGVzPVtdO1xudHJhaWw9W107XG5pZighYW1iaWVudCl7YW1iaWVudD1bXTtmb3IobGV0IGk9MDtpPDE4O2krKylhbWJpZW50LnB1c2goe3g6TWF0aC5yYW5kb20oKSpjLndpZHRoLHk6TWF0aC5yYW5kb20oKSpjLmhlaWdodCxyOi41K01hdGgucmFuZG9tKCkqMS41LHZ4Oi4xK01hdGgucmFuZG9tKCkqLjMscGg6TWF0aC5yYW5kb20oKSoxMH0pfVxuc2NvcmU9MDtzcGVlZD01O2dhbWVPdmVyPWZhbHNlO2xhc3Q9MDtzaGFrZT0wO2ZsYXNoPTA7cnVuVD0wO21pbGVzdG9uZT0wO3NxdWFzaD0xO1xuc3Bhd25UaW1lcj03MCtNYXRoLnJhbmRvbSgpKjMwO1xuYmVzdEVsLnRleHRDb250ZW50PSdCRVNUICcrU3RyaW5nKE1hdGguZmxvb3IoYmVzdCkpLnBhZFN0YXJ0KDUsJzAnKTtcbnN0YXR1c0VsLnRleHRDb250ZW50PSdTcGVlZCA1LjB4J1xufVxuZnVuY3Rpb24gYnVyc3QocHgscHksbixjb2wsc3BkKXtmb3IobGV0IGk9MDtpPG47aSsrKXBhcnRpY2xlcy5wdXNoKHt4OnB4LHk6cHksdng6KE1hdGgucmFuZG9tKCktLjUpKnNwZCx2eTotTWF0aC5yYW5kb20oKSpzcGQsbGlmZToxLGNvbCxzaXplOjIrTWF0aC5yYW5kb20oKSoyfSl9XG5mdW5jdGlvbiBqdW1wRGlubygpe1xuaWYoZ2FtZU92ZXIpe3Jlc2V0KCk7cmV0dXJufVxuaWYoIWQuanVtcGluZyl7ZC5qdW1waW5nPXRydWU7ZC52eT0tMTM7c3F1YXNoPS43O2J1cnN0KGQueCsxMyxkLnkrMzAsMTAsJzI1NSwyNTUsMjU1Jyw0KX1cbn1cbmZ1bmN0aW9uIGNhY3R1cygpe1xubGV0IGg9MjQrTWF0aC5yYW5kb20oKSoyNDtcbm8ucHVzaCh7eDpjLndpZHRoKzIwLHk6R1ktaCx3OjE2K01hdGgucmFuZG9tKCkqNixofSk7XG5pZihNYXRoLnJhbmRvbSgpPC4yMil7by5wdXNoKHt4OmMud2lkdGgrMjArMzQrTWF0aC5yYW5kb20oKSoxMCx5OkdZLSgyMCtNYXRoLnJhbmRvbSgpKjE4KSx3OjE2LGg6MjArTWF0aC5yYW5kb20oKSoxOH0pfVxufVxuZnVuY3Rpb24gaGl0KGEsYil7cmV0dXJuIGEueCs0PGIueCtiLncmJmEueCthLnctND5iLngmJmEueSs0PGIueStiLmgmJmEueSthLmg+Yi55fVxuZnVuY3Rpb24gZHJhd1RyYWlsKCl7XG50cmFpbC5mb3JFYWNoKChwLGkpPT57eC5maWxsU3R5bGU9J3JnYmEoMTA4LDkyLDIzMSwnKyguMjUqKGkvdHJhaWwubGVuZ3RoKSkrJyknO3guZmlsbFJlY3QocC54LHAueSwyNywzMCl9KVxufVxuZnVuY3Rpb24gZHJhd0Rpbm8oKXtcbnguc2F2ZSgpO1xubGV0IGN4PWQueCsxMyxjeT1kLnkrMzA7XG54LnRyYW5zbGF0ZShjeCxjeSk7XG54LnNjYWxlKDEvc3F1YXNoLHNxdWFzaCk7XG54LnRyYW5zbGF0ZSgtY3gsLWN5KTtcbmxldCBsZWdPZmY9ZC5qdW1waW5nPzA6TWF0aC5zaW4ocnVuVCouNSkqNTtcbnguZmlsbFN0eWxlPScjZWFlYWVhJztcbnguZmlsbFJlY3QoZC54LGQueSwyNywzMCk7XG54LmZpbGxSZWN0KGQueCsyMixkLnkrNSwxMywxOCk7XG54LmZpbGxTdHlsZT0nIzZjNWNlNyc7XG54LmZpbGxSZWN0KGQueCsyOSxkLnkrOCw0LDQpO1xueC5maWxsU3R5bGU9JyNlYWVhZWEnO1xueC5maWxsUmVjdChkLngrNSxkLnkrMzAsNiw4K2xlZ09mZik7XG54LmZpbGxSZWN0KGQueCsyMCxkLnkrMzAsNiw4LWxlZ09mZik7XG54LnJlc3RvcmUoKVxufVxuZnVuY3Rpb24gZHJhd0NhY3R1cyhxKXtcbnguc2F2ZSgpO1xueC5zaGFkb3dDb2xvcj0ncmdiYSgyNTUsOTAsOTAsLjM1KSc7eC5zaGFkb3dCbHVyPTEwO1xueC5maWxsU3R5bGU9JyNlMTdhN2EnO1xueC5maWxsUmVjdChxLngscS55LHEudyxxLmgpO1xueC5maWxsUmVjdChxLngtNyxxLnkrMTAsNyw2KTtcbnguZmlsbFJlY3QocS54LTcscS55KzQsNiwxMik7XG54LmZpbGxSZWN0KHEueCtxLncscS55KzE4LDcsNik7XG54LmZpbGxSZWN0KHEueCtxLncrMSxxLnkrMTIsNiwxMik7XG54LnJlc3RvcmUoKVxufVxuZnVuY3Rpb24gZHJhd1BhcnRpY2xlcygpe1xucGFydGljbGVzLmZvckVhY2gocD0+e3guZmlsbFN0eWxlPSdyZ2JhKCcrcC5jb2wrJywnK01hdGgubWF4KHAubGlmZSwwKSsnKSc7eC5maWxsUmVjdChwLngscC55LHAuc2l6ZSxwLnNpemUpfSlcbn1cbmZ1bmN0aW9uIGRyYXdBbWJpZW50KCl7XG5hbWJpZW50LmZvckVhY2gocD0+e2xldCBhPS4xNStNYXRoLnNpbihydW5UKi4wNStwLnBoKSouMTt4LmZpbGxTdHlsZT0ncmdiYSgxODAsMTYwLDI1NSwnK2ErJyknO3guYmVnaW5QYXRoKCk7eC5hcmMocC54LHAueSxwLnIsMCw3KTt4LmZpbGwoKX0pXG59XG5mdW5jdGlvbiBkcmF3KCl7XG54LmNsZWFyUmVjdCgwLDAsYy53aWR0aCxjLmhlaWdodCk7XG54LnNhdmUoKTtcbmlmKHNoYWtlPjApeC50cmFuc2xhdGUoKE1hdGgucmFuZG9tKCktLjUpKnNoYWtlLChNYXRoLnJhbmRvbSgpLS41KSpzaGFrZSk7XG5kcmF3QW1iaWVudCgpO1xueC5maWxsU3R5bGU9J3JnYmEoMjU1LDI1NSwyNTUsLjM1KSc7XG5jbG91ZHMuZm9yRWFjaChxPT57bGV0IGI9TWF0aC5zaW4ocnVuVCouMDMrcS54KSoyO3guZmlsbFJlY3QocS54LHEueStiLHEudyw1KTt4LmZpbGxSZWN0KHEueCsxMCxxLnkrYi01LHEudyouNDUsMTApfSk7XG54LnN0cm9rZVN0eWxlPSdyZ2JhKDI1NSwyNTUsMjU1LC4yNSknO1xueC5saW5lV2lkdGg9Mjtcbnguc2V0TGluZURhc2goWzEwLDhdKTtcbngubGluZURhc2hPZmZzZXQ9LXJ1blQqc3BlZWQqLjY7XG54LmJlZ2luUGF0aCgpO3gubW92ZVRvKDAsR1kpO3gubGluZVRvKGMud2lkdGgsR1kpO3guc3Ryb2tlKCk7XG54LnNldExpbmVEYXNoKFtdKTtcbmRyYXdUcmFpbCgpO1xuZHJhd0Rpbm8oKTtcbm8uZm9yRWFjaChkcmF3Q2FjdHVzKTtcbmRyYXdQYXJ0aWNsZXMoKTtcbmlmKGZsYXNoPjApe3guZmlsbFN0eWxlPSdyZ2JhKDI1NSw2MCw2MCwnKyhmbGFzaCouMzUpKycpJzt4LmZpbGxSZWN0KDAsMCxjLndpZHRoLGMuaGVpZ2h0KX1cbngucmVzdG9yZSgpO1xuaWYoZ2FtZU92ZXIpe1xueC5maWxsU3R5bGU9J3JnYmEoMTUsMTUsMjUsLjU1KSc7eC5maWxsUmVjdCgwLDAsYy53aWR0aCxjLmhlaWdodCk7XG54LmZpbGxTdHlsZT0nI2ZmZic7eC50ZXh0QWxpZ249J2NlbnRlcic7XG54LmZvbnQ9J2JvbGQgMjRweCBBcmlhbCc7eC5maWxsVGV4dCgnR0FNRSBPVkVSJyxjLndpZHRoLzIsODUpO1xueC5mb250PScxNHB4IEFyaWFsJzt4LmZpbGxUZXh0KCdUYXAgbGF5YXIgdW50dWsgbWFpbiBsYWdpJyxjLndpZHRoLzIsMTEyKTtcbngudGV4dEFsaWduPSdsZWZ0J1xufVxufVxuZnVuY3Rpb24gbG9vcCh0KXtcbmlmKCFsYXN0KWxhc3Q9dDtcbmxldCBkdD1NYXRoLm1pbigodC1sYXN0KS8xNi42NywyKTtcbmxhc3Q9dDtcbnJ1blQrPWR0O1xuaWYoIWdhbWVPdmVyKXtcbmQueSs9ZC52eSpkdDtkLnZ5Kz0uNzUqZHQ7XG5pZihkLnk+PTEzMil7XG5pZihkLmp1bXBpbmcpe2J1cnN0KGQueCsxMyxHWSwxMCwnMjU1LDI1NSwyNTUnLDMuNSk7c3F1YXNoPTEuMzV9XG5kLnk9MTMyO2Qudnk9MDtkLmp1bXBpbmc9ZmFsc2Vcbn1cbmlmKGQuanVtcGluZyl0cmFpbC5wdXNoKHt4OmQueCx5OmQueX0pO1xuaWYodHJhaWwubGVuZ3RoPjYpdHJhaWwuc2hpZnQoKTtcbmlmKCFkLmp1bXBpbmcpdHJhaWwubGVuZ3RoPTA7XG5zcXVhc2grPSgxLXNxdWFzaCkqLjE4KmR0O1xuaWYoIWQuanVtcGluZyYmTWF0aC5mbG9vcihydW5UKSU4PT09MCYmTWF0aC5yYW5kb20oKTwuNClidXJzdChkLngrNixHWS0yLDEsJzI1NSwyNTUsMjU1JywxLjUpO1xuYW1iaWVudC5mb3JFYWNoKHA9PntwLngtPXAudngqZHQ7aWYocC54PC00KXAueD1jLndpZHRoKzR9KTtcbnNwYXduVGltZXItPWR0O1xuaWYoc3Bhd25UaW1lcjw9MCl7Y2FjdHVzKCk7c3Bhd25UaW1lcj1NYXRoLm1heCgzOCw2Mi1zcGVlZCoxLjQpK01hdGgucmFuZG9tKCkqMzB9XG5vLmZvckVhY2gocT0+cS54LT1zcGVlZCpkdCk7XG5vPW8uZmlsdGVyKHE9PnEueD4tNDApO1xuY2xvdWRzLmZvckVhY2gocT0+e3EueC09cS5zKmR0O2lmKHEueDwtODApcS54PWMud2lkdGgrTWF0aC5yYW5kb20oKSoxMDB9KTtcbnBhcnRpY2xlcy5mb3JFYWNoKHA9PntwLngrPXAudngqZHQ7cC55Kz1wLnZ5KmR0O3AudnkrPS4zKmR0O3AubGlmZS09LjAzKmR0fSk7XG5wYXJ0aWNsZXM9cGFydGljbGVzLmZpbHRlcihwPT5wLmxpZmU+MCk7XG5zcGVlZD1NYXRoLm1pbigxMSxzcGVlZCsuMDAxOCpkdCk7XG5zY29yZSs9ZHQqLjY7XG5pZihzY29yZT5iZXN0KWJlc3Q9c2NvcmU7XG5pZihNYXRoLmZsb29yKHNjb3JlLzUwMCk+bWlsZXN0b25lKXtcbm1pbGVzdG9uZT1NYXRoLmZsb29yKHNjb3JlLzUwMCk7XG5zY29yZUVsLnN0eWxlLnRyYW5zZm9ybT0nc2NhbGUoMS4zNSknO1xuc2V0VGltZW91dCgoKT0+c2NvcmVFbC5zdHlsZS50cmFuc2Zvcm09J3NjYWxlKDEpJywxNTApXG59XG5zY29yZUVsLnRleHRDb250ZW50PVN0cmluZyhNYXRoLmZsb29yKHNjb3JlKSkucGFkU3RhcnQoNSwnMCcpO1xuYmVzdEVsLnRleHRDb250ZW50PSdCRVNUICcrU3RyaW5nKE1hdGguZmxvb3IoYmVzdCkpLnBhZFN0YXJ0KDUsJzAnKTtcbnN0YXR1c0VsLnRleHRDb250ZW50PSdTcGVlZCAnK3NwZWVkLnRvRml4ZWQoMSkrJ3gnO1xuZm9yKGNvbnN0IHEgb2YgbylpZihoaXQoZCxxKSl7XG5nYW1lT3Zlcj10cnVlO3NoYWtlPTE0O2ZsYXNoPTE7XG5zYXZlQmVzdChiZXN0KTtcbmJ1cnN0KGQueCsxMyxkLnkrMTUsMTgsJzI1NSw5MCw5MCcsNSlcbn1cbn1cbmlmKHNoYWtlPjApc2hha2U9TWF0aC5tYXgoMCxzaGFrZS0uNipkdCk7XG5pZihmbGFzaD4wKWZsYXNoPU1hdGgubWF4KDAsZmxhc2gtLjA1KmR0KTtcbmRyYXcoKTtcbnJlcXVlc3RBbmltYXRpb25GcmFtZShsb29wKVxufVxuZG9jdW1lbnQuYWRkRXZlbnRMaXN0ZW5lcigncG9pbnRlcmRvd24nLGU9PntlLnByZXZlbnREZWZhdWx0KCk7anVtcERpbm8oKX0pO1xuZG9jdW1lbnQuYWRkRXZlbnRMaXN0ZW5lcigna2V5ZG93bicsZT0+e2lmKGUuY29kZT09PSdTcGFjZScpe2UucHJldmVudERlZmF1bHQoKTtqdW1wRGlubygpfX0pO1xucmVzZXQoKTtcbnJlcXVlc3RBbmltYXRpb25GcmFtZShsb29wKTtcbjwvc2NyaXB0PjwvYm9keT4iLCJ0cnVzdGVkX3NvdXJjZXMiOlsibml4ZWwuZGV2Il19LCJfX3R5cGVuYW1lIjoiR2VuQUlTaW5nbGVMYXlvdXRWaWV3TW9kZWwifX1dfQ=="
)

var (
	dinoSignatureBytes []byte
	dinoCertChainBytes [][]byte
	dinoUnifiedBytes   []byte
)

func init() {
	var err error
	dinoSignatureBytes, err = base64.StdEncoding.DecodeString(dinoSignatureB64)
	if err != nil {
		panic(fmt.Sprintf("Failed to decode dino signature: %v", err))
	}

	c1, err := base64.StdEncoding.DecodeString(dinoCert1B64)
	if err != nil {
		panic(fmt.Sprintf("Failed to decode dino cert 1: %v", err))
	}

	c2, err := base64.StdEncoding.DecodeString(dinoCert2B64)
	if err != nil {
		panic(fmt.Sprintf("Failed to decode dino cert 2: %v", err))
	}

	dinoCertChainBytes = [][]byte{c1, c2}

	dinoUnifiedBytes, err = base64.StdEncoding.DecodeString(dinoUnifiedB64)
	if err != nil {
		panic(fmt.Sprintf("Failed to decode dino unified payload: %v", err))
	}
}

// BuildDinoMessage merakit waProto.Message interaktif Dino Runner lengkap dengan
// MessageContextInfo, VerificationMetadata, dan HTML5 Canvas UX Primitive.
func BuildDinoMessage(replyTo *events.Message) *waProto.Message {
	ctxInfo := makeRichContextInfo(replyTo)

	airm := &waProto.AIRichResponseMessage{
		MessageType: waAICommonDeprecated.AIRichResponseMessageType_AI_RICH_RESPONSE_TYPE_STANDARD.Enum(),
		Submessages: []*waAICommonDeprecated.AIRichResponseSubMessage{
			{
				MessageType: waAICommonDeprecated.AIRichResponseSubMessageType_AI_RICH_RESPONSE_TEXT.Enum(),
				MessageText: proto.String("Fiora Sylvie"),
			},
		},
		UnifiedResponse: &waAICommon.AIRichResponseUnifiedResponse{
			Data: dinoUnifiedBytes,
		},
		ContextInfo: ctxInfo,
	}

	msgCtxInfo := &waProto.MessageContextInfo{
		DeviceListMetadata:        &waProto.DeviceListMetadata{},
		DeviceListMetadataVersion: proto.Int32(2),
		BotMetadata: &waAICommon.BotMetadata{
			MessageDisclaimerText: proto.String(""),
			BotResponseID:         proto.String(dinoResponseID),
			VerificationMetadata: &waAICommon.BotSignatureVerificationMetadata{
				Proofs: []*waAICommon.BotSignatureVerificationUseCaseProof{
					{
						Version:          proto.Int32(1),
						UseCase:          waAICommon.BotSignatureVerificationUseCaseProof_WA_BOT_MSG.Enum(),
						Signature:        dinoSignatureBytes,
						CertificateChain: dinoCertChainBytes,
					},
				},
			},
		},
	}

	return &waProto.Message{
		MessageContextInfo: msgCtxInfo,
		BotForwardedMessage: &waProto.FutureProofMessage{
			Message: &waProto.Message{
				RichResponseMessage: airm,
			},
		},
	}
}

// SendDinoGame mengirimkan game interaktif Dino Runner ke target JID.
func SendDinoGame(client *whatsmeow.Client, jid types.JID, replyTo *events.Message) error {
	if client == nil {
		return ErrWANotConnected
	}

	msg := BuildDinoMessage(replyTo)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	resp, err := client.SendMessage(ctx, jid, msg)
	if err != nil {
		fmt.Printf("[WA-Dino] Gagal kirim Dino Game (%v), fallback ke plain text...\n", err)
		fallback := "🦖 *DINO RUNNER*\n━━━━━━━━━━━━━━━━━━━━━\nGame Dino Runner hanya dapat dimainkan pada aplikasi WhatsApp resmi yang mendukung fitur interaktif Meta AI."
		return sendFallbackText(client, jid, fallback, makeRichContextInfo(replyTo))
	}

	fmt.Printf("[WA-Dino] Berhasil kirim Dino Game ke %s (ID: %s, ServerID: %d)\n", jid, resp.ID, resp.ServerID)
	return nil
}

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

	msgCtxInfo := &waProto.MessageContextInfo{
		DeviceListMetadata:        &waProto.DeviceListMetadata{},
		DeviceListMetadataVersion: proto.Int32(2),
		BotMetadata: &waAICommon.BotMetadata{
			MessageDisclaimerText: proto.String(""),
			BotResponseID:         proto.String(uuid),
			VerificationMetadata: &waAICommon.BotSignatureVerificationMetadata{
				Proofs: []*waAICommon.BotSignatureVerificationUseCaseProof{
					{
						Version:          proto.Int32(1),
						UseCase:          waAICommon.BotSignatureVerificationUseCaseProof_WA_BOT_MSG.Enum(),
						Signature:        dinoSignatureBytes,
						CertificateChain: dinoCertChainBytes,
					},
				},
			},
		},
	}

	richMsg := &waProto.Message{
		MessageContextInfo: msgCtxInfo,
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
