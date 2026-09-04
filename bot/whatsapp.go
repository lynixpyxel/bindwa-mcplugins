package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
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
	phoneRegexDigits  = regexp.MustCompile(`\d{9,16}`)
)

type ServerStatus struct {
	Online      bool      `json:"online"`
	PlayerCount int       `json:"player_count"`
	MaxPlayers  int       `json:"max_players"`
	PlayerList  []string  `json:"player_list"`
	TPS         float64   `json:"tps"`
	LastUpdated time.Time `json:"last_updated"`
}

type WAMessageData struct {
	MsgID        string `json:"msg_id"`
	GroupJID     string `json:"group"`
	GroupName    string `json:"group_name"`
	SenderPhone  string `json:"sender"`
	SenderJID    string `json:"sender_jid"`
	PushName     string `json:"push_name"`
	Text         string `json:"text"`
	QuotedAuthor string `json:"quoted_author,omitempty"`
	QuotedText   string `json:"quoted_text,omitempty"`
}

type WAMessageCallback func(data WAMessageData) bool
type WABroadcastCallback func(payload interface{}) bool

type cachedGroup struct {
	name      string
	info      *types.GroupInfo
	updatedAt time.Time
}

type WAClient struct {
	client            *whatsmeow.Client
	container         *sqlstore.Container
	config            Config
	configPath        string
	mu                sync.RWMutex
	isLoggedIn        bool
	serverStatus      ServerStatus
	messageCallback   WAMessageCallback
	broadcastCallback WABroadcastCallback
	groupNames        map[string]cachedGroup
	groupMu           sync.RWMutex
	rulesManager      *RulesManager
	warnManager       *WarnManager
	welcomeManager    *WelcomeManager
	participantMap    map[string]string
	participantMu     sync.RWMutex
	latestLeaderboard []LeaderboardEntry
	leaderboardMu     sync.RWMutex
	imagemapManager   *ImageMapOrderManager
	tripayClient      *TripayClient
}

func NewWAClient(ctx context.Context, dbPath string, cfg Config, configPath string) (*WAClient, error) {
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

	uploadDir := cfg.ImageMapUploadDir
	if uploadDir == "" {
		uploadDir = "upload/images"
	}
	imManager := NewImageMapOrderManager(uploadDir, "upload/imagemap_transactions.json")
	tripayCfg := cfg.GetTripayConfig()
	tripay := NewTripayClient(tripayCfg.GetBaseURL(), tripayCfg.MerchantCode, tripayCfg.APIKey, tripayCfg.PrivateKey, tripayCfg.PaymentMethod)

	w := &WAClient{
		client:          client,
		container:       container,
		config:          cfg,
		configPath:      configPath,
		groupNames:      make(map[string]cachedGroup),
		rulesManager:    NewRulesManager("group_rules.json"),
		warnManager:     NewWarnManager("group_warns.json"),
		welcomeManager:  NewWelcomeManager("group_welcome.json"),
		participantMap:  make(map[string]string),
		imagemapManager: imManager,
		tripayClient:    tripay,
	}

	client.AddEventHandler(w.eventHandler)

	return w, nil
}

func (w *WAClient) SetMessageCallback(cb WAMessageCallback) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.messageCallback = cb
}

func (w *WAClient) SetBroadcastCallback(cb WABroadcastCallback) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.broadcastCallback = cb
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

func (w *WAClient) GetGroupInfoCached(ctx context.Context, groupJID types.JID) *types.GroupInfo {
	if groupJID.IsEmpty() || groupJID.Server != types.GroupServer {
		return nil
	}

	jidStr := groupJID.String()

	w.groupMu.RLock()
	cached, exists := w.groupNames[jidStr]
	w.groupMu.RUnlock()

	// Jika cache masih segar (< 3 menit), gunakan langsung
	if exists && cached.info != nil && time.Since(cached.updatedAt) < 3*time.Minute {
		return cached.info
	}

	if w.client == nil || !w.IsReady() {
		if exists && cached.info != nil {
			return cached.info
		}
		return nil
	}

	fetchCtx, cancel := context.WithTimeout(ctx, 7*time.Second)
	defer cancel()

	info, err := w.client.GetGroupInfo(fetchCtx, groupJID)
	if err != nil {
		fmt.Printf("[WA-Group] GetGroupInfo gagal untuk %s: %v\n", jidStr, err)
		if exists && cached.info != nil {
			return cached.info
		}
		return nil
	}

	if info != nil {
		w.groupMu.Lock()
		w.groupNames[jidStr] = cachedGroup{
			name:      info.Name,
			info:      info,
			updatedAt: time.Now(),
		}
		w.groupMu.Unlock()
		return info
	}

	return nil
}

func (w *WAClient) GetGroupName(jid types.JID) string {
	info := w.GetGroupInfoCached(context.Background(), jid)
	if info != nil && info.Name != "" {
		return info.Name
	}

	w.groupMu.RLock()
	cached, exists := w.groupNames[jid.String()]
	w.groupMu.RUnlock()

	if exists && cached.name != "" {
		return cached.name
	}

	return "Grup WA"
}

func (w *WAClient) isGroupLinked(groupJID string) bool {
	for _, j := range w.config.GroupJIDs {
		if strings.EqualFold(strings.TrimSpace(j), groupJID) {
			return true
		}
	}
	return false
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
	case *events.GroupInfo:
		w.handleGroupParticipantsChange(evt)
	case *events.Message:
		w.handleIncomingMessage(evt)
	}
}

func (w *WAClient) handleGroupParticipantsChange(evt *events.GroupInfo) {
	chatJID := evt.JID.String()

	// Cek apakah grup terhubung ke bot
	if !w.isGroupLinked(chatJID) {
		return
	}

	// Update cache nama grup secara instan jika ada event update nama grup (subject change) dari WhatsApp
	if evt.Name != nil && evt.Name.Name != "" {
		w.groupMu.Lock()
		w.groupNames[chatJID] = cachedGroup{
			name:      evt.Name.Name,
			updatedAt: time.Now(),
		}
		w.groupMu.Unlock()
		fmt.Printf("[WA-Group] Group name updated for %s: %s\n", chatJID, evt.Name.Name)
	}

	groupName := w.GetGroupName(evt.JID)

	// 1. Sambutan Member Baru Join (Dengan Foto Profil User)
	if len(evt.Join) > 0 {
		for _, member := range evt.Join {
			template := w.welcomeManager.GetWelcomeTemplate(chatJID)
			welcomeText := w.welcomeManager.FormatMessage(template, member.User, groupName)

			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			imgBytes := w.fetchUserProfilePicture(ctx, member)
			cancel()

			var err error
			if len(imgBytes) > 0 {
				err = w.SendImageWithMentions(context.Background(), evt.JID, imgBytes, welcomeText, []string{member.String()})
			}
			if len(imgBytes) == 0 || err != nil {
				_ = w.SendWithMentions(context.Background(), evt.JID, welcomeText, []string{member.String()})
			}
			fmt.Printf("[WA-Welcome] Member %s joined %s (With Photo: %v)\n", member.User, groupName, len(imgBytes) > 0)
		}
	}

	// 2. Notifikasi Member Keluar / Leave (Tanpa Foto Sesuai Permintaan)
	if len(evt.Leave) > 0 {
		for _, member := range evt.Leave {
			template := w.welcomeManager.GetLeaveTemplate(chatJID)
			leaveText := w.welcomeManager.FormatMessage(template, member.User, groupName)

			_ = w.SendWithMentions(context.Background(), evt.JID, leaveText, []string{member.String()})
			fmt.Printf("[WA-Goodbye] Member %s left %s\n", member.User, groupName)
		}
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

func normalizePhone(phone string) string {
	p := strings.TrimSpace(phone)
	p = strings.TrimPrefix(p, "+")
	if strings.HasPrefix(p, "0") {
		p = "62" + p[1:]
	}
	return p
}

func isPhoneMatch(p1, p2 string) bool {
	n1 := normalizePhone(p1)
	n2 := normalizePhone(p2)
	if n1 == "" || n2 == "" {
		return false
	}
	return n1 == n2
}

func (w *WAClient) resolveSenderPhone(evt *events.Message) string {
	// 1. Cek SenderAlt jika ada nomor HP
	if !evt.Info.SenderAlt.IsEmpty() && evt.Info.SenderAlt.Server == types.DefaultUserServer {
		return evt.Info.SenderAlt.User
	}

	// 2. Cek Sender langsung jika bukan @lid
	if evt.Info.Sender.Server == types.DefaultUserServer {
		return evt.Info.Sender.User
	}

	// 3. Jika sender adalah @lid di grup, cari nomor asli di metadata peserta grup
	if evt.Info.IsGroup {
		senderJID := evt.Info.Sender.ToNonAD()
		info := w.GetGroupInfoCached(context.Background(), evt.Info.Chat)
		if info != nil {
			for _, p := range info.Participants {
				pJID := p.JID.ToNonAD()
				pLID := p.LID.ToNonAD()
				if (pJID == senderJID || pLID == senderJID || pJID.User == senderJID.User || pLID.User == senderJID.User) && !p.PhoneNumber.IsEmpty() {
					return p.PhoneNumber.User
				}
			}
		}
	}

	return evt.Info.Sender.User
}

func (w *WAClient) isSenderOwner(evt *events.Message) bool {
	// 1. Pesan dari akun bot sendiri (jika owner scan QR dengan nomornya)
	if evt.Info.IsFromMe {
		return true
	}

	ownerNum := w.config.OwnerNumber
	if ownerNum == "" {
		return false
	}

	// 2. Cek Sender JID
	if isPhoneMatch(evt.Info.Sender.User, ownerNum) {
		return true
	}

	// 3. Cek SenderAlt JID
	if !evt.Info.SenderAlt.IsEmpty() && isPhoneMatch(evt.Info.SenderAlt.User, ownerNum) {
		return true
	}

	// 4. Cek nomor yang di-resolve dari grup metadata
	resolved := w.resolveSenderPhone(evt)
	if isPhoneMatch(resolved, ownerNum) {
		return true
	}

	return false
}

func (w *WAClient) IsUserGroupAdmin(ctx context.Context, groupJID types.JID, evt *events.Message) bool {
	if evt == nil {
		return false
	}

	if w.isSenderOwner(evt) {
		return true
	}

	senderJID := evt.Info.Sender.ToNonAD()
	senderAlt := evt.Info.SenderAlt.ToNonAD()
	senderPhone := w.resolveSenderPhone(evt)

	// 1. Cek di grup saat ini jika merupakan group chat
	if groupJID.Server == types.GroupServer {
		info := w.GetGroupInfoCached(ctx, groupJID)
		if info != nil && w.checkParticipantAdmin(info, senderJID, senderAlt, senderPhone) {
			return true
		}
	}

	// 2. Jika bukan admin di grup saat ini atau jika perintah dijalankan di PM/DM,
	// periksa apakah pengirim adalah admin di salah satu grup terkonfigurasi (GroupJIDs atau LogGroupJIDs)
	var allGroups []string
	allGroups = append(allGroups, w.config.GroupJIDs...)
	allGroups = append(allGroups, w.config.LogGroupJIDs...)

	for _, g := range allGroups {
		clean := strings.TrimSpace(g)
		if clean == "" {
			continue
		}
		var gJID types.JID
		if strings.Contains(clean, "@") {
			gJID, _ = types.ParseJID(clean)
		} else {
			gJID = types.NewJID(clean, types.GroupServer)
		}
		if gJID.IsEmpty() || gJID == groupJID {
			continue
		}
		info := w.GetGroupInfoCached(ctx, gJID)
		if info != nil && w.checkParticipantAdmin(info, senderJID, senderAlt, senderPhone) {
			return true
		}
	}

	return false
}

func (w *WAClient) checkParticipantAdmin(info *types.GroupInfo, senderJID, senderAlt types.JID, senderPhone string) bool {
	if info == nil {
		return false
	}

	for _, p := range info.Participants {
		if !p.IsAdmin && !p.IsSuperAdmin {
			continue
		}

		pJID := p.JID.ToNonAD()
		pLID := p.LID.ToNonAD()
		pPhone := p.PhoneNumber.ToNonAD()

		// A. Direct ToNonAD match (menghilangkan perbedaan Device ID multi-device)
		if (!pJID.IsEmpty() && pJID == senderJID) ||
			(!pLID.IsEmpty() && pLID == senderJID) ||
			(!pPhone.IsEmpty() && pPhone == senderJID) {
			return true
		}

		// B. Match senderAlt
		if !senderAlt.IsEmpty() {
			if (!pJID.IsEmpty() && pJID == senderAlt) ||
				(!pLID.IsEmpty() && pLID == senderAlt) ||
				(!pPhone.IsEmpty() && pPhone == senderAlt) {
				return true
			}
		}

		// C. Match by User identifier string (LID atau Nomor HP)
		if (!pJID.IsEmpty() && pJID.User == senderJID.User) ||
			(!pLID.IsEmpty() && pLID.User == senderJID.User) ||
			(!pPhone.IsEmpty() && pPhone.User == senderJID.User) {
			return true
		}
		if !senderAlt.IsEmpty() {
			if (!pJID.IsEmpty() && pJID.User == senderAlt.User) ||
				(!pLID.IsEmpty() && pLID.User == senderAlt.User) ||
				(!pPhone.IsEmpty() && pPhone.User == senderAlt.User) {
				return true
			}
		}

		// D. Match resolved phone number
		if senderPhone != "" {
			if (!pPhone.IsEmpty() && isPhoneMatch(pPhone.User, senderPhone)) ||
				(!pJID.IsEmpty() && isPhoneMatch(pJID.User, senderPhone)) {
				return true
			}
		}
	}

	return false
}

func (w *WAClient) IsBotGroupAdmin(ctx context.Context, groupJID types.JID) bool {
	if w.client == nil || w.client.Store == nil || w.client.Store.ID == nil {
		return false
	}
	botUser := w.client.Store.ID.User

	info := w.GetGroupInfoCached(ctx, groupJID)
	if info == nil {
		return false
	}

	for _, p := range info.Participants {
		pJID := p.JID.ToNonAD()
		pLID := p.LID.ToNonAD()
		pPhone := p.PhoneNumber.ToNonAD()
		isBot := pJID.User == botUser || pPhone.User == botUser || pLID.User == botUser
		if isBot && (p.IsAdmin || p.IsSuperAdmin) {
			return true
		}
	}
	return false
}

func (w *WAClient) extractTargetUser(evt *events.Message, args string) (types.JID, string) {
	ctxInfo := evt.Message.GetExtendedTextMessage().GetContextInfo()

	// 1. Cek dari mentions (@user)
	if ctxInfo != nil && len(ctxInfo.GetMentionedJID()) > 0 {
		targetStr := ctxInfo.GetMentionedJID()[0]
		targetJID, _ := types.ParseJID(targetStr)
		// Bersihkan mention dari args untuk alasan
		reason := args
		for _, m := range ctxInfo.GetMentionedJID() {
			userNum := strings.Split(m, "@")[0]
			reason = strings.ReplaceAll(reason, "@"+userNum, "")
		}
		return targetJID, strings.TrimSpace(reason)
	}

	// 2. Cek dari pesan yang di-reply/quote
	if ctxInfo != nil && ctxInfo.GetParticipant() != "" {
		targetJID, _ := types.ParseJID(ctxInfo.GetParticipant())
		return targetJID, strings.TrimSpace(args)
	}

	// 3. Cek dari nomor telepon di argumen
	matches := phoneRegexDigits.FindStringSubmatch(args)
	if len(matches) > 0 {
		targetJID := types.NewJID(matches[0], types.DefaultUserServer)
		reason := strings.TrimSpace(strings.Replace(args, matches[0], "", 1))
		return targetJID, reason
	}

	return types.EmptyJID, args
}

func (w *WAClient) handleIncomingMessage(evt *events.Message) {
	var text string
	if msg := evt.Message.GetConversation(); msg != "" {
		text = msg
	} else if ext := evt.Message.GetExtendedTextMessage(); ext != nil && ext.GetText() != "" {
		text = ext.GetText()
	} else if img := evt.Message.GetImageMessage(); img != nil && img.GetCaption() != "" {
		text = img.GetCaption()
	} else if doc := evt.Message.GetDocumentMessage(); doc != nil && doc.GetCaption() != "" {
		text = doc.GetCaption()
	} else if btnResp := evt.Message.GetButtonsResponseMessage(); btnResp != nil {
		text = btnResp.GetSelectedButtonID()
		if text == "" {
			text = btnResp.GetSelectedDisplayText()
		}
	} else if tmplResp := evt.Message.GetTemplateButtonReplyMessage(); tmplResp != nil {
		text = tmplResp.GetSelectedID()
		if text == "" {
			text = tmplResp.GetSelectedDisplayText()
		}
	} else if interResp := evt.Message.GetInteractiveResponseMessage(); interResp != nil && interResp.GetNativeFlowResponseMessage() != nil {
		paramsJSON := interResp.GetNativeFlowResponseMessage().GetParamsJSON()
		var parsed struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal([]byte(paramsJSON), &parsed); err == nil && parsed.ID != "" {
			text = parsed.ID
		} else {
			text = paramsJSON
		}
	}

	text = strings.TrimSpace(text)
	if text == "" {
		return
	}

	chatJID := evt.Info.Chat.String()
	senderPhone := w.resolveSenderPhone(evt)
	pushName := evt.Info.PushName
	if pushName == "" {
		pushName = senderPhone
	}

	w.recordParticipant(pushName, senderPhone, evt.Info.Sender.String())

	// Deteksi apakah pesan adalah tindakan moderasi ImageMap (reply atau perintah langsung dengan/tanpa prefix)
	orderID := w.extractOrderIDFromEvent(evt)
	fields := strings.Fields(text)
	var prefix, cmd, args string
	var isCmd bool

	if len(fields) > 0 {
		firstWord := strings.ToLower(fields[0])
		firstWordClean := strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(firstWord, "."), "/"), "!")

		switch firstWordClean {
		case "acc", "approve", "setujui":
			isCmd = true
			cmd = "acc"
			prefix = "."
			if len(fields) > 1 && strings.HasPrefix(strings.ToUpper(fields[1]), "MAP-") {
				args = fields[1]
			} else if orderID != "" {
				args = orderID
			} else if len(fields) > 1 {
				args = fields[1]
			}

		case "reject", "decline", "tolak":
			isCmd = true
			cmd = "decline"
			prefix = "."
			if len(fields) > 1 && strings.HasPrefix(strings.ToUpper(fields[1]), "MAP-") {
				args = fields[1]
				if len(fields) > 2 {
					args += " " + strings.Join(fields[2:], " ")
				}
			} else if orderID != "" {
				args = orderID
				if len(fields) > 1 {
					args += " " + strings.Join(fields[1:], " ")
				}
			} else {
				args = strings.Join(fields[1:], " ")
			}
		}
	}

	if !isCmd {
		prefix, cmd, args, isCmd = w.parseCommand(text)
	}

	if prefix == "" {
		prefix = "."
	}

	if !isCmd {
		// Cek apakah user sedang ditunggu input Minecraft username untuk order imagemap
		// PENTING: Hanya tanggapi jika user me-reply (mengutip) pesan prompt bot, bukan saat ngechat biasa
		senderPhone := w.resolveSenderPhone(evt)
		if order := w.imagemapManager.GetOrderWaitingUsername(senderPhone); order != nil {
			if w.isReplyingToUsernamePrompt(evt, order) {
				go w.HandleAssignImageMapUsername(context.Background(), evt, order, text)
				return
			}
		}

		// Jika pesan bukan command, cek apakah me-reply pesan dari bot di grup yang terhubung
		if evt.Info.IsGroup && w.isGroupLinked(chatJID) && w.isReplyingToBot(evt) {
			w.forwardChatMessageToMC(evt, text)
			return
		}
		return
	}

	ctx := context.Background()

	switch cmd {
	case "help", "menu":
		w.replyMenu(evt.Info.Chat, evt)

	case "setuser", "claimuser", "setmc":
		senderPhone := w.resolveSenderPhone(evt)
		if order := w.imagemapManager.GetOrderWaitingUsername(senderPhone); order != nil {
			go w.HandleAssignImageMapUsername(ctx, evt, order, args)
		} else {
			_ = w.SendReplyToGroup(ctx, evt.Info.Chat, "Anda tidak memiliki pesanan imagemap yang sedang menunggu input username Minecraft.", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		}

	case "imagemap", "ordermap", "buymap", "orderimg":
		go w.ProcessImageMapOrder(ctx, evt, args)

	case "cancelmap", "cancelorder":
		go w.CancelUserPendingOrder(ctx, evt)

	case "acc", "approve", "setujui":
		go w.HandleApproveImageMap(ctx, evt, args)

	case "decline", "reject", "tolak":
		go w.HandleDeclineImageMap(ctx, evt, args)

	case "mccmdlist", "mccmd", "cmdlist", "mccommands", "commandlist":
		msg := "*MINECRAFT SERVER COMMAND LIST*\n" +
			"━━━━━━━━━━━━━━━━━━━━━\n\n" +
			"*Essentials Commands:*\n" +
			"• `/pay <player>` transfer money to someone\n" +
			"• `/pay <player> <player>` multiple or infinite transfer\n" +
			"• `/balance` check your balance\n" +
			"• `/balance <player>` check player balance\n" +
			"• `/baltop` check balance leaderboard\n" +
			"• `/sell <item>` sell an item\n" +
			"• `/sell <item> <amount>` bulk sell\n" +
			"• `/sell hand` sell item in your hand\n" +
			"• `/tpa <player>` teleport to someone\n" +
			"• `/tpaccept` accept teleport request\n" +
			"• `/tpahere` teleport someone to you\n" +
			"• `/tpacancel` cancel teleport request\n" +
			"• `/tpdeny` ignore teleport request\n" +
			"• `/tpaall` teleport all player to you\n" +
			"• `/back` back to your last location/death point\n" +
			"• `/home` teleport to your respawn point\n" +
			"• `/home <name>` teleport to your home\n" +
			"• `/sethome <name>` set your home\n" +
			"• `/renamehome <home> <newname>` rename home\n" +
			"• `/delhome <name>` delete your home\n" +
			"• `/time` check current time\n" +
			"• `/playtime` check your playtime\n" +
			"• `/playtime <player>` check player playtime\n" +
			"• `/nick` set your nickname\n" +
			"• `/hat` set your hat\n" +
			"• `/msg <player> <text>` whisper to player\n" +
			"• `/afk` afk\n" +
			"• `/spawn` or `/lobby` go back to lobby\n" +
			"• `/warp <warp>` to teleport to warp locations\n" +
			"• `/warps` show warp list\n\n" +
			"*Shop Commands:*\n" +
			"• `/shop` buy some items\n" +
			"• `/sellall <item>` sell all item you want\n" +
			"• `/sellallhand` sell all item in your hand\n" +
			"• `/sellgui` open gui to sell some items\n\n" +
			"*Other Commands:*\n" +
			"• `/bindwa` link your whatsapp account\n" +
			"• `/elytraboard` check elytra leaderboard\n" +
			"• `/chat` or `/chat <text>` send/reply chat to whatsapp group\n" +
			"━━━━━━━━━━━━━━━━━━━━━"

		_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "📜")
		_ = w.SendToJID(ctx, evt.Info.Chat, msg)

	case "cekserver", "status", "server", "info":
		go w.replyServerStatus(evt.Info.Chat, evt)

	case "top", "leaderboard", "elytratop", "elytraboard":
		go w.replyLeaderboard(evt.Info.Chat, evt)

	case "dino", "dinorunner":
		go func() {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "🦖")
			_ = w.SendDinoGame(evt.Info.Chat, evt)
		}()

	case "rules", "rule", "peraturan":
		groupName := w.GetGroupName(evt.Info.Chat)
		rulesText := w.rulesManager.GetRules(chatJID)
		replyMsg := fmt.Sprintf("*PERATURAN & RULES: %s*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"%s\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"_Patuhi rules untuk kenyamanan bersama._",
			groupName, rulesText)
		_ = w.SendToJID(ctx, evt.Info.Chat, replyMsg)

	case "setrules", "editrules":
		if !evt.Info.IsGroup {
			_ = w.SendToJID(ctx, evt.Info.Chat, "⚠️ Perintah ini hanya dapat digunakan di dalam grup WhatsApp!")
			return
		}

		if !w.IsUserGroupAdmin(ctx, evt.Info.Chat, evt) {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "❌")
			_ = w.SendToJID(ctx, evt.Info.Chat, "⛔ Akses ditolak! Hanya Admin grup atau Owner bot yang dapat mengatur rules.")
			return
		}

		if strings.TrimSpace(args) == "" {
			_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("⚠️ Format salah! Gunakan: *%ssetrules <isi rules baru>*", prefix))
			return
		}

		_ = w.rulesManager.SetRules(chatJID, args)
		groupName := w.GetGroupName(evt.Info.Chat)
		_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "✅")
		_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("*Rules Berhasil Diperbarui untuk Grup %s!*\nKetik *%srules* untuk melihat peraturan terbaru.", groupName, prefix))

	case "setwelcome", "setwellcome":
		if !evt.Info.IsGroup {
			_ = w.SendToJID(ctx, evt.Info.Chat, "Perintah ini hanya dapat digunakan di dalam grup WhatsApp!")
			return
		}

		if !w.IsUserGroupAdmin(ctx, evt.Info.Chat, evt) {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "❌")
			_ = w.SendToJID(ctx, evt.Info.Chat, "Akses ditolak! Hanya Admin grup atau Owner bot yang dapat mengatur pesan welcome.")
			return
		}

		if strings.TrimSpace(args) == "" {
			_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("*Format Salah!*\nGunakan: *%ssetwelcome <teks welcome>*\n\nVariabel yang tersedia:\n• *%%name* : Tag member (@user)\n• *%%groupname* : Nama grup\n• *%%date* : Tanggal join\n• *%%time* : Jam join\n\nContoh: *%ssetwelcome Halo %%name selamat datang di %%groupname!*", prefix, prefix))
			return
		}

		_ = w.welcomeManager.SetWelcomeTemplate(chatJID, args)
		groupName := w.GetGroupName(evt.Info.Chat)
		_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "✅")
		_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("✅ *Template Welcome Berhasil Diperbarui untuk Grup %s!*\n\n*Preview:* \n%s", groupName, w.welcomeManager.FormatMessage(args, "user", groupName)))

	case "setgoodbye", "setleave", "setout":
		if !evt.Info.IsGroup {
			_ = w.SendToJID(ctx, evt.Info.Chat, "Perintah ini hanya dapat digunakan di dalam grup WhatsApp!")
			return
		}

		if !w.IsUserGroupAdmin(ctx, evt.Info.Chat, evt) {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "❌")
			_ = w.SendToJID(ctx, evt.Info.Chat, "Akses ditolak! Hanya Admin grup atau Owner bot yang dapat mengatur pesan goodbye.")
			return
		}

		if strings.TrimSpace(args) == "" {
			_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("*Format Salah!*\nGunakan: *%ssetgoodbye <teks goodbye>*\n\nVariabel yang tersedia:\n• *%%name* : Tag member (@user)\n• *%%groupname* : Nama grup\n• *%%date* : Tanggal\n• *%%time* : Jam\n\nContoh: *%ssetgoodbye %%name telah keluar dari %%groupname. Sampai jumpa!*", prefix, prefix))
			return
		}

		_ = w.welcomeManager.SetLeaveTemplate(chatJID, args)
		groupName := w.GetGroupName(evt.Info.Chat)
		_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "✅")
		_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("*Template Goodbye Berhasil Diperbarui untuk Grup %s!*\n\n*Preview:* \n%s", groupName, w.welcomeManager.FormatMessage(args, "user", groupName)))

	case "cekwelcome", "templatewelcome":
		groupName := w.GetGroupName(evt.Info.Chat)
		wTpl := w.welcomeManager.GetWelcomeTemplate(chatJID)
		lTpl := w.welcomeManager.GetLeaveTemplate(chatJID)

		replyMsg := fmt.Sprintf("*TEMPLATE WELCOME & GOODBYE: %s*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"*Template Welcome:*\n%s\n\n"+
			"*Template Goodbye:*\n%s\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"_Gunakan %ssetwelcome dan %ssetgoodbye untuk mengubah (Khusus Admin)._",
			groupName, wTpl, lTpl, prefix, prefix)
		_ = w.SendToJID(ctx, evt.Info.Chat, replyMsg)

	case "warn", "warning":
		if !evt.Info.IsGroup {
			_ = w.SendToJID(ctx, evt.Info.Chat, "⚠️ Perintah ini hanya dapat digunakan di dalam grup WhatsApp!")
			return
		}

		if !w.IsUserGroupAdmin(ctx, evt.Info.Chat, evt) {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "❌")
			_ = w.SendToJID(ctx, evt.Info.Chat, "⛔ Akses ditolak! Hanya Admin grup atau Owner bot yang dapat memberikan peringatan.")
			return
		}

		targetJID, reason := w.extractTargetUser(evt, args)
		if targetJID.IsEmpty() {
			_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("⚠️ Tag atau reply user yang ingin di-warn!\nContoh: *%swarn @user toxic*", prefix))
			return
		}

		// Cegah warn terhadap bot sendiri atau owner
		if (w.client.Store.ID != nil && targetJID.User == w.client.Store.ID.User) || isPhoneMatch(targetJID.User, w.config.OwnerNumber) {
			_ = w.SendToJID(ctx, evt.Info.Chat, "⚠️ Tidak dapat memberikan peringatan kepada Bot atau Owner.")
			return
		}

		if reason == "" {
			reason = "Melanggar peraturan grup"
		}

		warnCount := w.warnManager.AddWarn(chatJID, targetJID.User)

		if warnCount < 3 {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "⚠️")
			warnMsg := fmt.Sprintf("⚠️ *PERINGATAN DIBERIKAN! [%d/3]*\n"+
				"━━━━━━━━━━━━━━━━━━━━━\n"+
				"• Target: @%s\n"+
				"• Oleh Admin: @%s\n"+
				"• Alasan: *%s*\n"+
				"• Status Warn: *[%d/3]*\n"+
				"━━━━━━━━━━━━━━━━━━━━━\n"+
				"_Maksimal peringatan adalah 3x. Jika mencapai 3x akan otomatis dikeluarkan dari grup._",
				warnCount, targetJID.User, senderPhone, reason, warnCount)

			_ = w.SendWithMentions(ctx, evt.Info.Chat, warnMsg, []string{targetJID.String(), evt.Info.Sender.String()})
		} else {
			// Batas 3x tercapai -> Kick user
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "🚫")

			isBotAdmin := w.IsBotGroupAdmin(ctx, evt.Info.Chat)
			if isBotAdmin {
				_, kickErr := w.client.UpdateGroupParticipants(ctx, evt.Info.Chat, []types.JID{targetJID}, whatsmeow.ParticipantChangeRemove)
				if kickErr == nil {
					w.warnManager.ResetWarn(chatJID, targetJID.User)
					kickMsg := fmt.Sprintf("*BATAS PERINGATAN [3/3] TERCAPAI!*\n"+
						"━━━━━━━━━━━━━━━━━━━━━\n"+
						"• Target: @%s\n"+
						"• Alasan: *%s*\n"+
						"• Tindakan: *Dikeluarkan dari grup (Kick)* \n"+
						"━━━━━━━━━━━━━━━━━━━━━",
						targetJID.User, reason)
					_ = w.SendWithMentions(ctx, evt.Info.Chat, kickMsg, []string{targetJID.String()})
					return
				}
			}

			// Jika bot bukan admin
			alertMsg := fmt.Sprintf("*BATAS PERINGATAN [3/3] TERCAPAI!*\n"+
				"━━━━━━━━━━━━━━━━━━━━━\n"+
				"• Target: @%s\n"+
				"• Alasan: *%s*\n"+
				"• Status: *Telah mencapai 3x peringatan!*\n"+
				"• Catatan: _Bot bukan admin grup. Mohon Admin mengeluarkan user ini._\n"+
				"━━━━━━━━━━━━━━━━━━━━━",
				targetJID.User, reason)
			_ = w.SendWithMentions(ctx, evt.Info.Chat, alertMsg, []string{targetJID.String()})
		}

	case "resetwarn", "unwarn", "clearwarn":
		if !evt.Info.IsGroup {
			_ = w.SendToJID(ctx, evt.Info.Chat, "Perintah ini hanya dapat digunakan di dalam grup WhatsApp!")
			return
		}

		if !w.IsUserGroupAdmin(ctx, evt.Info.Chat, evt) {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "❌")
			_ = w.SendToJID(ctx, evt.Info.Chat, "Akses ditolak! Hanya Admin grup atau Owner bot yang dapat me-reset peringatan.")
			return
		}

		targetJID, _ := w.extractTargetUser(evt, args)
		if targetJID.IsEmpty() {
			_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("Tag atau reply user yang ingin di-reset warn!\nContoh: *%sresetwarn @user*", prefix))
			return
		}

		w.warnManager.ResetWarn(chatJID, targetJID.User)
		_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "✅")
		resetMsg := fmt.Sprintf("*PERINGATAN DI-RESET! [0/3]*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"• Target: @%s\n"+
			"• Oleh Admin: @%s\n"+
			"• Status Peringatan: *[0/3] (Bersih)*\n"+
			"━━━━━━━━━━━━━━━━━━━━━",
			targetJID.User, senderPhone)
		_ = w.SendWithMentions(ctx, evt.Info.Chat, resetMsg, []string{targetJID.String(), evt.Info.Sender.String()})

	case "cekwarn", "warns", "mywarn":
		targetJID, _ := w.extractTargetUser(evt, args)
		if targetJID.IsEmpty() {
			targetJID = evt.Info.Sender
		}

		count := w.warnManager.GetWarn(chatJID, targetJID.User)
		cekMsg := fmt.Sprintf("*STATUS PERINGATAN*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"• User: @%s\n"+
			"• Jumlah Warn: *[%d/3]*\n"+
			"━━━━━━━━━━━━━━━━━━━━━",
			targetJID.User, count)
		_ = w.SendWithMentions(ctx, evt.Info.Chat, cekMsg, []string{targetJID.String()})

	case "linkthisgroup", "linkgroup", "addgroup":
		if !evt.Info.IsGroup {
			_ = w.SendToJID(ctx, evt.Info.Chat, "Perintah ini hanya dapat digunakan di dalam grup WhatsApp!")
			return
		}

		if !w.IsUserGroupAdmin(ctx, evt.Info.Chat, evt) {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "❌")
			_ = w.SendToJID(ctx, evt.Info.Chat, "Akses ditolak! Perintah ini hanya untuk atmin")
			return
		}

		groupName := w.GetGroupName(evt.Info.Chat)

		// Cek apakah mode link khusus logs
		if strings.EqualFold(args, "logs") || strings.EqualFold(args, "log") {
			alreadyLinked := false
			for _, j := range w.config.LogGroupJIDs {
				if strings.EqualFold(strings.TrimSpace(j), chatJID) {
					alreadyLinked = true
					break
				}
			}
			if !alreadyLinked {
				w.config.LogGroupJIDs = append(w.config.LogGroupJIDs, chatJID)
				if w.configPath != "" {
					_ = SaveConfig(w.configPath, w.config)
				}
			}
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "✅")
			replyMsg := fmt.Sprintf("*Grup Berhasil Dihubungkan sebagai Grup Logs!*\n"+
				"━━━━━━━━━━━━━━━━━━━━━\n"+
				"• Nama Grup: *%s*\n"+
				"• JID: `%s`\n"+
				"• Tipe: *Logs & Audit (Verifikasi OTP, Akun)*\n"+
				"━━━━━━━━━━━━━━━━━━━━━\n"+
				"_Notifikasi audit verifikasi akun/OTP pemain akan dikirimkan khusus ke grup ini._",
				groupName, chatJID)
			_ = w.SendToJID(ctx, evt.Info.Chat, replyMsg)
			fmt.Printf("[WA-Admin] Log group linked by %s: %s (%s)\n", senderPhone, groupName, chatJID)
			return
		}

		alreadyLinked := false
		for _, j := range w.config.GroupJIDs {
			if strings.EqualFold(strings.TrimSpace(j), chatJID) {
				alreadyLinked = true
				break
			}
		}

		if !alreadyLinked {
			w.config.GroupJIDs = append(w.config.GroupJIDs, chatJID)
			if w.configPath != "" {
				_ = SaveConfig(w.configPath, w.config)
			}
		}

		_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "✅")
		replyMsg := fmt.Sprintf("*Grup Berhasil Dihubungkan ke Minecraft!*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"• Nama Grup: *%s*\n"+
			"• JID: `%s`\n"+
			"• Status: *Aktif & Tersimpan*\n"+
			"━━━━━━━━━━━━━━━━━━━━━\n"+
			"_Anggota grup ini sekarang dapat menggunakan perintah %schat <pesan>, %scekserver, %srules, dan %swarn._\n\n"+
			"_Tip: Gunakan `%slinkgroup logs` jika ingin menghubungkan grup khusus log verifikasi OTP._",
			groupName, chatJID, prefix, prefix, prefix, prefix, prefix)
		_ = w.SendToJID(ctx, evt.Info.Chat, replyMsg)
		fmt.Printf("[WA-Admin] Group linked by owner %s: %s (%s)\n", senderPhone, groupName, chatJID)

	case "unlinkthisgroup", "unlinkgroup":
		if !evt.Info.IsGroup {
			_ = w.SendToJID(ctx, evt.Info.Chat, "Perintah ini hanya dapat digunakan di dalam grup WhatsApp!")
			return
		}

		if !w.IsUserGroupAdmin(ctx, evt.Info.Chat, evt) {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "❌")
			_ = w.SendToJID(ctx, evt.Info.Chat, "Akses ditolak! Perintah ini hanya untuk atmin")
			return
		}

		groupName := w.GetGroupName(evt.Info.Chat)

		// Cek jika unlink khusus logs
		if strings.EqualFold(args, "logs") || strings.EqualFold(args, "log") {
			newLogs := make([]string, 0, len(w.config.LogGroupJIDs))
			for _, j := range w.config.LogGroupJIDs {
				if !strings.EqualFold(strings.TrimSpace(j), chatJID) {
					newLogs = append(newLogs, j)
				}
			}
			w.config.LogGroupJIDs = newLogs
			if w.configPath != "" {
				_ = SaveConfig(w.configPath, w.config)
			}
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "✅")
			_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("Grup *%s* telah diputuskan dari grup logs Minecraft.", groupName))
			return
		}

		newGroups := make([]string, 0, len(w.config.GroupJIDs))
		for _, j := range w.config.GroupJIDs {
			if !strings.EqualFold(strings.TrimSpace(j), chatJID) {
				newGroups = append(newGroups, j)
			}
		}
		w.config.GroupJIDs = newGroups
		if w.configPath != "" {
			_ = SaveConfig(w.configPath, w.config)
		}

		_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "✅")
		_ = w.SendToJID(ctx, evt.Info.Chat, fmt.Sprintf("Grup *%s* telah diputuskan hubungannya dari server Minecraft.", groupName))

	case "chat", "mc", "game":
		if args == "" {
			_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "❌")
			return
		}

		w.forwardChatMessageToMC(evt, args)
	}
}

func (w *WAClient) forwardChatMessageToMC(evt *events.Message, text string) {
	chatJID := evt.Info.Chat.String()
	senderPhone := w.resolveSenderPhone(evt)
	pushName := evt.Info.PushName
	if pushName == "" {
		pushName = senderPhone
	}

	groupName := "Grup WA"
	if evt.Info.IsGroup {
		groupName = w.GetGroupName(evt.Info.Chat)
	}

	w.recordParticipant(pushName, senderPhone, evt.Info.Sender.String())

	quotedAuthor, quotedText := w.extractQuotedContext(evt)

	msgData := WAMessageData{
		MsgID:        string(evt.Info.ID),
		GroupJID:     chatJID,
		GroupName:    groupName,
		SenderPhone:  senderPhone,
		SenderJID:    evt.Info.Sender.String(),
		PushName:     pushName,
		Text:         text,
		QuotedAuthor: quotedAuthor,
		QuotedText:   quotedText,
	}

	w.mu.RLock()
	cb := w.messageCallback
	w.mu.RUnlock()

	success := false
	if cb != nil {
		success = cb(msgData)
	}

	// Reaksi emoji ke pesan WhatsApp
	if success {
		_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "✅")
		fmt.Printf("[WA -> MC] [%s] %s (Quoted: %s): %s (Reacted ✅)\n", groupName, pushName, quotedAuthor, text)
	} else {
		_ = w.ReactMessage(evt.Info.Chat, evt.Info.Sender, evt.Info.ID, "❌")
		fmt.Printf("[WA -> MC] Gagal forward pesan dari %s (Server offline / disconnected) (Reacted ❌)\n", senderPhone)
	}
}

func (w *WAClient) isReplyingToBot(evt *events.Message) bool {
	var ctxInfo *waProto.ContextInfo
	if ext := evt.Message.GetExtendedTextMessage(); ext != nil {
		ctxInfo = ext.GetContextInfo()
	} else if img := evt.Message.GetImageMessage(); img != nil {
		ctxInfo = img.GetContextInfo()
	} else if vid := evt.Message.GetVideoMessage(); vid != nil {
		ctxInfo = vid.GetContextInfo()
	}

	if ctxInfo == nil || ctxInfo.GetStanzaID() == "" {
		return false
	}

	// 1. Cek apakah participant yang di-quote adalah nomor bot sendiri
	if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
		botUser := w.client.Store.ID.User
		if strings.Contains(ctxInfo.GetParticipant(), botUser) {
			return true
		}
	}

	// 2. Cek apakah teks pesan yang di-quote berasal dari bot / server Minecraft
	if qm := ctxInfo.GetQuotedMessage(); qm != nil {
		qText := ""
		if qm.GetConversation() != "" {
			qText = qm.GetConversation()
		} else if qm.GetExtendedTextMessage() != nil {
			qText = qm.GetExtendedTextMessage().GetText()
		} else if qm.GetImageMessage() != nil {
			qText = qm.GetImageMessage().GetCaption()
		}

		if strings.HasPrefix(qText, "*|Server|*") ||
			strings.HasPrefix(qText, "*Ender Dragon*") ||
			strings.HasPrefix(qText, "*Dragon Egg*") ||
			strings.HasPrefix(qText, "⚡ *") ||
			strings.HasPrefix(qText, "*Leaderboard") ||
			strings.HasPrefix(qText, "👋 *SELAMAT") ||
			strings.HasPrefix(qText, "*PERATURAN") {
			return true
		}
	}

	return false
}

// isReplyingToUsernamePrompt memeriksa apakah pesan me-reply (mengutip) pesan prompt permintaan username dari bot.
func (w *WAClient) isReplyingToUsernamePrompt(evt *events.Message, order *ImageMapOrder) bool {
	if evt == nil || evt.Message == nil {
		return false
	}

	ctxInfo := w.extractContextInfo(evt.Message)
	if ctxInfo == nil || ctxInfo.GetStanzaID() == "" {
		// Pesan biasa tanpa me-reply apapun
		return false
	}

	// 1. Cek isi teks dari pesan yang di-quote
	qm := ctxInfo.GetQuotedMessage()
	if qm != nil {
		qText := w.extractTextFromMessage(qm)
		if qText != "" {
			if strings.Contains(qText, "Username Minecraft") ||
				strings.Contains(qText, "balas pesan ini") ||
				strings.Contains(qText, "balas (reply) pesan ini") ||
				strings.Contains(qText, "/getimagemap") ||
				(order != nil && order.PaymentID != "" && strings.Contains(qText, order.PaymentID)) {
				return true
			}
		}
	}

	// 2. Cek apakah me-reply pesan dari bot
	isFromBot := false
	if w.client != nil && w.client.Store != nil && w.client.Store.ID != nil {
		botUser := w.client.Store.ID.User
		if botUser != "" && strings.Contains(ctxInfo.GetParticipant(), botUser) {
			isFromBot = true
		}
	}

	// Di private chat dengan bot (DM), jika participant kosong atau sama dengan bot
	if !evt.Info.IsGroup && (ctxInfo.GetParticipant() == "" || isFromBot) {
		isFromBot = true
	}

	if isFromBot {
		// Pastikan pesan yang di-quote bukan broadcast game atau chat Minecraft
		if qm != nil {
			qText := w.extractTextFromMessage(qm)
			if strings.HasPrefix(qText, "*|Server|*") ||
				strings.HasPrefix(qText, "*Ender Dragon*") ||
				strings.HasPrefix(qText, "*Dragon Egg*") ||
				strings.HasPrefix(qText, "⚡ *") ||
				strings.HasPrefix(qText, "*Leaderboard") ||
				strings.HasPrefix(qText, "👋 *SELAMAT") ||
				strings.HasPrefix(qText, "*PERATURAN") {
				return false
			}
		}
		return true
	}

	return false
}

func (w *WAClient) recordParticipant(pushName, phone, jid string) {
	w.participantMu.Lock()
	defer w.participantMu.Unlock()

	if w.participantMap == nil {
		w.participantMap = make(map[string]string)
	}

	cleanJID := jid
	if cleanJID == "" && phone != "" {
		cleanJID = types.NewJID(normalizePhone(phone), types.DefaultUserServer).String()
	}

	if cleanJID != "" {
		normPhone := normalizePhone(phone)
		if len(normPhone) >= 8 {
			w.participantMap[normPhone] = cleanJID
			w.participantMap[phone] = cleanJID
		}
		if pushName != "" {
			nameKey := strings.ToLower(strings.ReplaceAll(pushName, " ", ""))
			// Abaikan nama yang terlalu pendek (< 3 karakter) atau kata umum
			if len(nameKey) >= 3 && nameKey != "all" && nameKey != "everyone" && nameKey != "admin" {
				w.participantMap[nameKey] = cleanJID
			}
		}
	}
}

var mentionRegex = regexp.MustCompile(`@([a-zA-Z0-9_\-]+)`)

func (w *WAClient) resolveMentionsInText(ctx context.Context, groupJID types.JID, text string) []string {
	matches := mentionRegex.FindAllStringSubmatch(text, -1)
	if len(matches) == 0 {
		return nil
	}

	var validTags []string
	for _, m := range matches {
		tag := m[1]
		tagLower := strings.ToLower(tag)
		// Abaikan selector bawaan Minecraft (@a, @p, @r, @s, @e)
		if tagLower == "a" || tagLower == "p" || tagLower == "r" || tagLower == "s" || tagLower == "e" {
			continue
		}
		// Abaikan tag all / hidetag agar tidak disalahgunakan untuk spam tag seisi grup
		if tagLower == "all" || tagLower == "everyone" || tagLower == "here" || tagLower == "hidetag" || tagLower == "tagall" {
			continue
		}
		// Abaikan kata terlalu pendek (< 3 karakter)
		if len(tagLower) < 3 {
			continue
		}
		validTags = append(validTags, tag)
	}

	if len(validTags) == 0 {
		return nil
	}

	mentionsMap := make(map[string]bool)

	// 1. Cek dari member grup WhatsApp via whatsmeow jika groupJID valid
	if !groupJID.IsEmpty() {
		info, err := w.client.GetGroupInfo(ctx, groupJID)
		if err == nil && info != nil {
			for _, p := range info.Participants {
				phone := p.PhoneNumber.User
				if phone == "" && p.JID.Server == types.DefaultUserServer {
					phone = p.JID.User
				}
				targetJID := p.JID.String()
				if targetJID == "" && phone != "" {
					targetJID = types.NewJID(phone, types.DefaultUserServer).String()
				}
				if targetJID != "" && phone != "" {
					for _, tag := range validTags {
						if isPhoneMatch(tag, phone) {
							mentionsMap[targetJID] = true
						}
					}
				}
			}
		}
	}

	// 2. Cek dari cache participant yang sudah tercatat
	w.participantMu.RLock()
	for _, tag := range validTags {
		tagClean := strings.ToLower(strings.ReplaceAll(tag, " ", ""))
		if targetJID, exists := w.participantMap[tagClean]; exists {
			mentionsMap[targetJID] = true
		} else if phoneRegexDigits.MatchString(tag) && len(tag) >= 8 {
			normalized := normalizePhone(tag)
			j := types.NewJID(normalized, types.DefaultUserServer).String()
			mentionsMap[j] = true
		}
	}
	w.participantMu.RUnlock()

	if len(mentionsMap) == 0 {
		return nil
	}

	mentions := make([]string, 0, len(mentionsMap))
	for j := range mentionsMap {
		mentions = append(mentions, j)
	}

	return mentions
}

func (w *WAClient) extractQuotedContext(evt *events.Message) (string, string) {
	var quotedAuthor string
	var quotedText string

	var ctxInfo *waProto.ContextInfo
	if ext := evt.Message.GetExtendedTextMessage(); ext != nil {
		ctxInfo = ext.GetContextInfo()
	} else if img := evt.Message.GetImageMessage(); img != nil {
		ctxInfo = img.GetContextInfo()
	} else if vid := evt.Message.GetVideoMessage(); vid != nil {
		ctxInfo = vid.GetContextInfo()
	}

	if ctxInfo != nil && ctxInfo.GetStanzaID() != "" {
		if qm := ctxInfo.GetQuotedMessage(); qm != nil {
			if qm.GetConversation() != "" {
				quotedText = qm.GetConversation()
			} else if qm.GetExtendedTextMessage() != nil && qm.GetExtendedTextMessage().GetText() != "" {
				quotedText = qm.GetExtendedTextMessage().GetText()
			} else if qm.GetImageMessage() != nil {
				quotedText = "[Foto]"
				if qm.GetImageMessage().GetCaption() != "" {
					quotedText = "[Foto] " + qm.GetImageMessage().GetCaption()
				}
			} else if qm.GetVideoMessage() != nil {
				quotedText = "[Video]"
			} else if qm.GetDocumentMessage() != nil {
				quotedText = "[Dokumen]"
			} else if qm.GetStickerMessage() != nil {
				quotedText = "[Stiker]"
			}
		}

		// 1. Cek apakah yang di-quote adalah pesan server/MC (*|Server|* <PlayerName>: ...)
		if strings.HasPrefix(quotedText, "*|Server|* <") {
			endIdx := strings.Index(quotedText, ">:")
			if endIdx > 12 {
				quotedAuthor = quotedText[12:endIdx]
				if len(quotedText) > endIdx+2 {
					quotedText = strings.TrimSpace(quotedText[endIdx+2:])
				}
			}
		}

		// 2. Jika bukan dari server MC, gunakan participant info
		if quotedAuthor == "" && ctxInfo.GetParticipant() != "" {
			partJID, err := types.ParseJID(ctxInfo.GetParticipant())
			if err == nil {
				quotedAuthor = partJID.User
				if evt.Info.IsGroup {
					info, err := w.client.GetGroupInfo(context.Background(), evt.Info.Chat)
					if err == nil && info != nil {
						for _, p := range info.Participants {
							if (p.JID.User == partJID.User || p.LID.User == partJID.User) && !p.PhoneNumber.IsEmpty() {
								quotedAuthor = p.PhoneNumber.User
								break
							}
						}
					}
				}
			}
		}

		// Batasi panjang preview quoted text agar rapi di chat in-game
		if len(quotedText) > 35 {
			quotedText = quotedText[:35] + "..."
		}
	}

	return quotedAuthor, quotedText
}

func (w *WAClient) replyServerStatus(target types.JID, replyTo *events.Message) {
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

	replyToID := ""
	replySender := ""
	if replyTo != nil {
		replyToID = string(replyTo.Info.ID)
		replySender = replyTo.Info.Sender.ToNonAD().String()
	}

	_ = w.SendReplyToGroup(context.Background(), target, replyText, replyToID, replySender, "")
}

func (w *WAClient) UpdateLeaderboardFromText(text string) {
	lines := strings.Split(text, "\n")
	var entries []LeaderboardEntry
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "*") {
			continue
		}
		// Format: "1. Steve — 5 elytra" or "1. Steve - 5000m"
		parts := strings.SplitN(line, ".", 2)
		if len(parts) == 2 {
			rank := strings.TrimSpace(parts[0])
			rest := strings.TrimSpace(parts[1])

			var player, count string
			foundSep := false
			for _, sep := range []string{" — ", " - ", " : ", "—", "-", ":"} {
				if idx := strings.Index(rest, sep); idx != -1 {
					player = strings.TrimSpace(rest[:idx])
					count = strings.TrimSpace(rest[idx+len(sep):])
					foundSep = true
					break
				}
			}

			if !foundSep {
				fields := strings.Fields(rest)
				if len(fields) >= 2 {
					player = fields[0]
					count = strings.Join(fields[1:], " ")
				} else {
					player = rest
					count = "-"
				}
			}
			entries = append(entries, LeaderboardEntry{
				Rank:   "#" + rank,
				Player: player,
				Count:  count,
			})
		}
	}

	w.leaderboardMu.Lock()
	if len(entries) > 0 {
		w.latestLeaderboard = entries
	}
	w.leaderboardMu.Unlock()
}

func (w *WAClient) formatLeaderboardText(entries []LeaderboardEntry, isUpdate bool) string {
	title := "*ELYTRA LEADERBOARD*"
	if isUpdate {
		title = "*ELYTRA LEADERBOARD UPDATE*"
	}

	var sb strings.Builder
	sb.WriteString(title + "\n━━━━━━━━━━━━━━━━━━━━━\n")

	if len(entries) == 0 {
		sb.WriteString("No data available.\n")
	} else {
		for _, e := range entries {
			rankStr := strings.TrimPrefix(e.Rank, "#")
			sb.WriteString(fmt.Sprintf("%s. %s — %s elytra\n", rankStr, e.Player, e.Count))
		}
	}
	sb.WriteString("━━━━━━━━━━━━━━━━━━━━━")
	return sb.String()
}

func (w *WAClient) replyLeaderboard(target types.JID, replyTo *events.Message) {
	w.leaderboardMu.RLock()
	entries := make([]LeaderboardEntry, len(w.latestLeaderboard))
	copy(entries, w.latestLeaderboard)
	w.leaderboardMu.RUnlock()

	text := w.formatLeaderboardText(entries, false)

	replyToID := ""
	replySender := ""
	if replyTo != nil {
		replyToID = string(replyTo.Info.ID)
		replySender = replyTo.Info.Sender.ToNonAD().String()
	}

	_ = w.SendReplyToGroup(context.Background(), target, text, replyToID, replySender, "")
}

func (w *WAClient) SendDinoGame(target types.JID, replyTo *events.Message) error {
	if !w.IsReady() {
		return ErrWANotConnected
	}
	return SendDinoGame(w.client, target, replyTo)
}

func (w *WAClient) BroadcastRichLeaderboard(ctx context.Context) int {
	return w.BroadcastLeaderboard(ctx)
}

func (w *WAClient) BroadcastLeaderboard(ctx context.Context) int {
	if !w.IsReady() {
		return 0
	}

	w.leaderboardMu.RLock()
	entries := make([]LeaderboardEntry, len(w.latestLeaderboard))
	copy(entries, w.latestLeaderboard)
	w.leaderboardMu.RUnlock()

	text := w.formatLeaderboardText(entries, true)
	return w.SendToGroups(ctx, text)
}

func (w *WAClient) SendToLogGroups(ctx context.Context, message string) int {
	if !w.IsReady() {
		return 0
	}

	w.mu.RLock()
	logGroups := make([]string, len(w.config.LogGroupJIDs))
	copy(logGroups, w.config.LogGroupJIDs)
	w.mu.RUnlock()

	if len(logGroups) == 0 {
		return 0
	}

	successCount := 0
	for _, rawJid := range logGroups {
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
			fmt.Printf("[WA-Client] Gagal kirim ke grup logs %s: %v\n", cleanJid, err)
		}
	}

	return successCount
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

	cleanPhoneStr := strings.TrimPrefix(phone, "+")
	jid := types.NewJID(cleanPhoneStr, types.DefaultUserServer)
	return w.SendToJID(ctx, jid, message)
}

func (w *WAClient) SendToJID(ctx context.Context, jid types.JID, message string) error {
	_, err := w.SendToJIDWithID(ctx, jid, message)
	return err
}

// SendToJIDWithID mengirim pesan teks ke JID tertentu dan mengembalikan ID pesan yang terkirim.
func (w *WAClient) SendToJIDWithID(ctx context.Context, jid types.JID, message string) (string, error) {
	if !w.IsReady() {
		return "", ErrWANotConnected
	}

	msg := &waProto.Message{
		Conversation: proto.String(message),
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := w.client.SendMessage(sendCtx, jid, msg)
	if err != nil {
		return "", fmt.Errorf("failed to send wa message: %w", err)
	}

	return string(resp.ID), nil
}

func (w *WAClient) SendWithMentions(ctx context.Context, jid types.JID, message string, mentions []string) error {
	if !w.IsReady() {
		return ErrWANotConnected
	}

	msg := &waProto.Message{
		ExtendedTextMessage: &waProto.ExtendedTextMessage{
			Text: proto.String(message),
			ContextInfo: &waProto.ContextInfo{
				MentionedJID: mentions,
			},
		},
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	_, err := w.client.SendMessage(sendCtx, jid, msg)
	return err
}

func (w *WAClient) SendImageWithMentions(ctx context.Context, jid types.JID, imageBytes []byte, caption string, mentions []string) error {
	if !w.IsReady() {
		return ErrWANotConnected
	}

	uploadResp, err := w.client.Upload(ctx, imageBytes, whatsmeow.MediaImage)
	if err != nil {
		return fmt.Errorf("failed to upload image: %w", err)
	}

	imageMsg := &waProto.ImageMessage{
		Caption:       proto.String(caption),
		Mimetype:      proto.String("image/jpeg"),
		URL:           &uploadResp.URL,
		DirectPath:    &uploadResp.DirectPath,
		MediaKey:      uploadResp.MediaKey,
		FileEncSHA256: uploadResp.FileEncSHA256,
		FileSHA256:    uploadResp.FileSHA256,
		FileLength:    &uploadResp.FileLength,
		ContextInfo: &waProto.ContextInfo{
			MentionedJID: mentions,
		},
	}

	sendCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	_, err = w.client.SendMessage(sendCtx, jid, &waProto.Message{
		ImageMessage: imageMsg,
	})
	return err
}

// SendImageReplyWithID mengirim gambar dan mengembalikan ID pesan yang terkirim.
func (w *WAClient) SendImageReplyWithID(ctx context.Context, jid types.JID, imageBytes []byte, mimeType string, caption string, replyEvt *events.Message) (string, error) {
	if !w.IsReady() {
		return "", ErrWANotConnected
	}

	uploadResp, err := w.client.Upload(ctx, imageBytes, whatsmeow.MediaImage)
	if err != nil {
		return "", fmt.Errorf("failed to upload image: %w", err)
	}

	if mimeType == "" {
		mimeType = "image/png"
	}

	var ctxInfo *waProto.ContextInfo
	if replyEvt != nil {
		ctxInfo = &waProto.ContextInfo{
			StanzaID: proto.String(string(replyEvt.Info.ID)),
		}
		sender := replyEvt.Info.Sender.ToNonAD().String()
		if sender != "" {
			ctxInfo.Participant = proto.String(sender)
		}
	}

	imageMsg := &waProto.ImageMessage{
		Caption:       proto.String(caption),
		Mimetype:      proto.String(mimeType),
		URL:           &uploadResp.URL,
		DirectPath:    &uploadResp.DirectPath,
		MediaKey:      uploadResp.MediaKey,
		FileEncSHA256: uploadResp.FileEncSHA256,
		FileSHA256:    uploadResp.FileSHA256,
		FileLength:    &uploadResp.FileLength,
		ContextInfo:   ctxInfo,
	}

	sendCtx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()

	resp, err := w.client.SendMessage(sendCtx, jid, &waProto.Message{
		ImageMessage: imageMsg,
	})
	if err != nil {
		return "", err
	}
	return string(resp.ID), nil
}

// SendImageReply mengirim gambar disertai caption.
func (w *WAClient) SendImageReply(ctx context.Context, jid types.JID, imageBytes []byte, mimeType string, caption string, replyEvt *events.Message) error {
	_, err := w.SendImageReplyWithID(ctx, jid, imageBytes, mimeType, caption, replyEvt)
	return err
}

// DeleteMessage menghapus atau me-revoke pesan yang pernah dikirimkan oleh bot.
func (w *WAClient) DeleteMessage(ctx context.Context, chat types.JID, messageID string) error {
	if !w.IsReady() || messageID == "" {
		return ErrWANotConnected
	}

	targetChat := chat.ToNonAD()
	var botJID types.JID
	if w.client != nil && w.client.Store != nil {
		botJID = w.client.Store.GetJID().ToNonAD()
	}

	revokeMsg := w.client.BuildRevoke(targetChat, types.EmptyJID, types.MessageID(messageID))

	// Jika pesan berada di dalam grup WhatsApp, MessageKey untuk me-revoke pesan bot HARUS menyertakan Participant (JID bot).
	// Tanpa participant, aplikasi WhatsApp di HP anggota grup tidak dapat menemukan pesan di database lokal grup.
	// Sama seperti implementasi Baileys di order-modules.js:
	// ...(isGroup ? { participant: botJid } : {})
	if targetChat.Server == types.GroupServer && !botJID.IsEmpty() {
		if revokeMsg.GetProtocolMessage() != nil && revokeMsg.GetProtocolMessage().GetKey() != nil {
			revokeMsg.ProtocolMessage.Key.Participant = proto.String(botJID.String())
		}
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	resp, err := w.client.SendMessage(sendCtx, targetChat, revokeMsg)
	if err != nil {
		fmt.Printf("[WA-Revoke] Gagal me-revoke pesan %s di %s: %v\n", messageID, targetChat, err)
		return err
	}
	fmt.Printf("[WA-Revoke] Berhasil mengirim revoke pesan %s di %s (revokeMsgID: %s)\n", messageID, targetChat, resp.ID)
	return nil
}

func (w *WAClient) replyMenu(chatJID types.JID, evt *events.Message) {
	serverName := w.config.ServerName
	if serverName == "" {
		serverName = "CSMP Minecraft Server"
	}

	menu := fmt.Sprintf("*MENU BOT MINECRAFT*\n"+
		"━━━━━━━━━━━━━━━━━━━━━\n"+
		"Server: *%s*\n"+
		"Prefix: `.` `!` `#` `?`\n"+
		"━━━━━━━━━━━━━━━━━━━━━\n\n"+
		"🎮 *MINECRAFT & SERVER*\n"+
		"• *.status* / *.cekserver* : Cek status server & pemain online\n"+
		"• *.top* / *.elytratop* : Leaderboard perolehan elytra\n"+
		"• *.chat <pesan>* : Kirim chat ke dalam game Minecraft\n"+
		"• *.mccmdlist* : Daftar perintah command in-game Minecraft\n\n"+
		"🖼️ *IMAGE MAP (CUSTOM PICTURE / GIF)*\n"+
		"  _Tarif: Gambar Rp 5.000 | GIF Rp 7.000 (Flat)_\n"+
		"• *.imagemap <nama> <tinggi> <lebar>*\n"+
		"  _Beli custom map dengan reply atau caption foto/GIF._\n"+
		"  Contoh: `.imagemap logo 2 2`\n"+
		"• *.imagemap <nama> <lebar>x<tinggi>*\n"+
		"  _Format cross lebih ringkas._\n"+
		"  Contoh: `.imagemap logo 2x2`\n"+
		"• *.imagemap <url> <nama> <lebar>x<tinggi>*\n"+
		"  _Pesan map langsung menggunakan URL link gambar/GIF._\n"+
		"• *.cancelmap* : Batalkan order imagemap yang aktif\n"+
		"• *.acc <order_id>* : Setujui pesanan imagemap (Admin)\n"+
		"• *.decline <order_id> [alasan]* : Tolak pesanan imagemap (Admin)\n\n"+
		"🛡️ *PENGATURAN GRUP*\n"+
		"• *.rules* : Lihat peraturan grup\n"+
		"• *.setrules <teks>* : Ubah peraturan grup (Admin)\n"+
		"• *.warn @user [alasan]* : Beri peringatan member (Admin)\n"+
		"• *.cekwarn* : Cek sisa peringatan akun kamu\n"+
		"• *.resetwarn @user* : Reset peringatan member (Admin)\n"+
		"• *.setwelcome <teks>* : Atur template welcome (Admin)\n"+
		"• *.setgoodbye <teks>* : Atur template goodbye (Admin)\n"+
		"• *.cekwelcome* : Cek template sambutan grup\n"+
		"• *.linkgroup [logs]* : Hubungkan grup ini ke server (Owner)\n"+
		"• *.unlinkgroup [logs]* : Putus tautan grup (Owner)\n\n"+
		"🎲 *HIBURAN*\n"+
		"• *.dino* : Mainkan game Dino Runner di WhatsApp\n\n"+
		"━━━━━━━━━━━━━━━━━━━━━\n"+
		"_Ketik perintah di atas untuk menggunakan fitur._", serverName)

	_ = w.ReactMessage(chatJID, evt.Info.Sender, evt.Info.ID, "📖")
	_ = w.SendReplyToGroup(context.Background(), chatJID, menu, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
}

func (w *WAClient) fetchUserProfilePicture(ctx context.Context, jid types.JID) []byte {
	picInfo, err := w.client.GetProfilePictureInfo(ctx, jid, &whatsmeow.GetProfilePictureParams{})
	if err != nil || picInfo == nil || picInfo.URL == "" {
		return nil
	}

	httpClient := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", picInfo.URL, nil)
	if err != nil {
		return nil
	}

	resp, err := httpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if resp != nil && resp.Body != nil {
			_ = resp.Body.Close()
		}
		return nil
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil || len(data) == 0 {
		return nil
	}
	return data
}

func (w *WAClient) ReactMessage(chat, sender types.JID, msgID types.MessageID, emoji string) error {
	if !w.IsReady() {
		return ErrWANotConnected
	}

	reactionMsg := w.client.BuildReaction(chat, sender, msgID, emoji)
	sendCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := w.client.SendMessage(sendCtx, chat, reactionMsg)
	if err != nil {
		return fmt.Errorf("failed to react to message: %w", err)
	}

	return nil
}

func (w *WAClient) SendToGroups(ctx context.Context, message string) int {
	return w.SendToGroupsWithReply(ctx, message, "", "", "")
}

func (w *WAClient) SendReplyToGroup(ctx context.Context, groupJID types.JID, message string, replyToID string, replySender string, quotedText string) error {
	if !w.IsReady() {
		return ErrWANotConnected
	}

	targetJID := groupJID
	if targetJID.IsEmpty() {
		if len(w.config.GroupJIDs) > 0 {
			cleanJid := strings.TrimSpace(w.config.GroupJIDs[0])
			if strings.Contains(cleanJid, "@") {
				targetJID, _ = types.ParseJID(cleanJid)
			} else {
				targetJID = types.NewJID(cleanJid, types.GroupServer)
			}
		} else {
			return errors.New("no group jid available")
		}
	}

	// Jika pesan biasa (bukan quoted reply) dan tidak mengandung karakter '@', kirim sebagai plain conversation murni
	if replyToID == "" && !strings.Contains(message, "@") {
		return w.SendToJID(ctx, targetJID, message)
	}

	extMsg := &waProto.ExtendedTextMessage{
		Text: proto.String(message),
	}

	var ctxInfo *waProto.ContextInfo

	if replyToID != "" {
		ctxInfo = &waProto.ContextInfo{
			StanzaID: proto.String(replyToID),
		}
		if replySender != "" {
			var senderJID types.JID
			if strings.Contains(replySender, "@") {
				senderJID, _ = types.ParseJID(replySender)
			} else {
				senderJID = types.NewJID(replySender, types.DefaultUserServer)
			}
			if !senderJID.IsEmpty() {
				ctxInfo.Participant = proto.String(senderJID.String())
			}
		}
		if quotedText != "" {
			ctxInfo.QuotedMessage = &waProto.Message{
				Conversation: proto.String(quotedText),
			}
		}
	}

	// Resolve mentions (@Name atau @Phone) menjadi clickable blue tag di WhatsApp HANYA JIKA ADA '@'
	if strings.Contains(message, "@") {
		mentions := w.resolveMentionsInText(ctx, targetJID, message)
		if len(mentions) > 0 {
			if ctxInfo == nil {
				ctxInfo = &waProto.ContextInfo{}
			}
			ctxInfo.MentionedJID = append(ctxInfo.MentionedJID, mentions...)
		}
	}

	if ctxInfo != nil {
		extMsg.ContextInfo = ctxInfo
	}

	msg := &waProto.Message{
		ExtendedTextMessage: extMsg,
	}

	sendCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	_, err := w.client.SendMessage(sendCtx, targetJID, msg)
	return err
}

func (w *WAClient) SendToGroupsWithReply(ctx context.Context, message string, replyToID string, replySender string, quotedText string) int {
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

		err := w.SendReplyToGroup(ctx, jid, message, replyToID, replySender, quotedText)
		if err == nil {
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

// HandleImageMapOrderStatusFromMC menangani konfirmasi dari Minecraft plugin terkait status binding player order imagemap.
func (w *WAClient) HandleImageMapOrderStatusFromMC(orderID string, bound bool, playerName string) {
	order := w.imagemapManager.GetOrderByPaymentID(orderID)
	if order == nil {
		fmt.Printf("[WA-ImageMap] Order %s dari MC tidak ditemukan\n", orderID)
		return
	}

	chatJID, _ := types.ParseJID(order.ChatJID)

	if bound && playerName != "" {
		w.imagemapManager.AssignPlayerToOrder(order.PaymentID, playerName)
		msg := fmt.Sprintf("Pembayaran Berhasil\n\n"+
			"Gambar imagemap '%s' (Order ID: %s) telah diproses ke server Minecraft.\n"+
			"Akun Minecraft terhubung: *%s*\n\n"+
			"Silakan login ke server Minecraft dan ketik */getimagemap* untuk mengambil map ke inventory Anda.",
			order.MapName, order.PaymentID, playerName)
		_ = w.SendToJID(context.Background(), chatJID, msg)
	} else {
		w.imagemapManager.SetOrderWaitingUsername(order.PaymentID)
		msg := fmt.Sprintf("Pembayaran Berhasil\n\n"+
			"Gambar imagemap '%s' (Order ID: %s) telah diproses ke server Minecraft.\n"+
			"Nomor WhatsApp Anda belum terhubung dengan akun Minecraft manapun.\n\n"+
			"Silakan balas (reply) pesan ini dengan *Username Minecraft* Anda (atau ketik *.setuser <username>*) agar map dapat diklaim di dalam game.\n"+
			"(Catatan: Untuk pemain Bedrock, wajib diawali tanda titik, contoh: *.NamaPlayer*)",
			order.MapName, order.PaymentID)
		_ = w.SendToJID(context.Background(), chatJID, msg)
	}
}

// HandleAssignImageMapUsername memproses input username Minecraft dari pemesan yang belum ter-bind.
func (w *WAClient) HandleAssignImageMapUsername(ctx context.Context, evt *events.Message, order *ImageMapOrder, usernameInput string) {
	username := strings.TrimSpace(usernameInput)
	// Bersihkan prefix jika dikirim via command (.setuser, .setmc, .claimuser)
	for _, p := range []string{".setuser", "!setuser", "/setuser", "setuser", ".setmc", "!setmc", "/setmc", "setmc", ".claimuser", "!claimuser", "/claimuser", "claimuser"} {
		if strings.HasPrefix(strings.ToLower(username), p) {
			username = strings.TrimSpace(username[len(p):])
			break
		}
	}

	chatJID := evt.Info.Chat

	if username == "" {
		_ = w.SendReplyToGroup(ctx, chatJID, "Silakan masukkan username Minecraft Anda.\nContoh: .setuser Steve atau balas (reply) pesan bot dengan username Anda.", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	// Validasi username Minecraft (Java: 2-16 char alfanumerik+underscore; Bedrock: diawali titik lalu 1-16 char)
	matched, _ := regexp.MatchString(`^\.?[a-zA-Z0-9_]{2,16}$`, username)
	if !matched {
		_ = w.SendReplyToGroup(ctx, chatJID, "Username Minecraft tidak valid. Panjang harus 2-16 karakter (huruf, angka, garis bawah). Untuk pemain Bedrock diawali tanda titik (contoh: .PlayerBedrock).", string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
		return
	}

	w.imagemapManager.AssignPlayerToOrder(order.PaymentID, username)

	// Kirim penugasan player ke plugin Minecraft via WebSocket
	if w.broadcastCallback != nil {
		publicURL := w.config.PublicURL
		if publicURL == "" {
			publicURL = fmt.Sprintf("http://192.168.18.67:%d", w.config.HTTPPort)
		}
		fileName := order.SavedFileName
		if fileName == "" {
			ext := ".png"
			if order.MediaType == "gif" {
				ext = ".gif"
			}
			fileName = strings.ToLower(order.MapName) + ext
		}
		imageURL := fmt.Sprintf("%s/images/%s", strings.TrimSuffix(publicURL, "/"), fileName)

		w.broadcastCallback(map[string]interface{}{
			"type":         "imagemap_assign_player",
			"order_id":     order.PaymentID,
			"map_name":     order.MapName,
			"player_name":  username,
			"sender_phone": order.SenderPhone,
			"image_url":    imageURL,
			"width":        order.Width,
			"height":       order.Height,
		})
	}

	confirmMsg := fmt.Sprintf("Akun Minecraft *%s* berhasil dihubungkan ke imagemap '%s' (Order ID: %s)!\n\n"+
		"Silakan masuk ke server Minecraft dan ketik */getimagemap* untuk mengambil item map ke inventory Anda.",
		username, order.MapName, order.PaymentID)
	_ = w.SendReplyToGroup(ctx, chatJID, confirmMsg, string(evt.Info.ID), evt.Info.Sender.ToNonAD().String(), "")
}
