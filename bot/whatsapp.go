package main

import (
	"context"
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

type cachedGroup struct {
	name      string
	updatedAt time.Time
}

type WAClient struct {
	client          *whatsmeow.Client
	container       *sqlstore.Container
	config          Config
	configPath      string
	mu              sync.RWMutex
	isLoggedIn      bool
	serverStatus    ServerStatus
	messageCallback WAMessageCallback
	groupNames      map[string]cachedGroup
	groupMu         sync.RWMutex
	rulesManager    *RulesManager
	warnManager     *WarnManager
	welcomeManager  *WelcomeManager
	participantMap  map[string]string
	participantMu   sync.RWMutex
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

	w := &WAClient{
		client:         client,
		container:      container,
		config:         cfg,
		configPath:     configPath,
		groupNames:     make(map[string]cachedGroup),
		rulesManager:   NewRulesManager("group_rules.json"),
		warnManager:    NewWarnManager("group_warns.json"),
		welcomeManager: NewWelcomeManager("group_welcome.json"),
		participantMap: make(map[string]string),
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

func (w *WAClient) GetGroupName(jid types.JID) string {
	jidStr := jid.String()

	w.groupMu.RLock()
	cached, exists := w.groupNames[jidStr]
	w.groupMu.RUnlock()

	// Jika cache masih segar (< 1 menit), gunakan langsung
	if exists && cached.name != "" && time.Since(cached.updatedAt) < 1*time.Minute {
		return cached.name
	}

	if !w.IsReady() {
		if exists && cached.name != "" {
			return cached.name
		}
		return "Grup WA"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	info, err := w.client.GetGroupInfo(ctx, jid)
	if err == nil && info != nil && info.Name != "" {
		w.groupMu.Lock()
		w.groupNames[jidStr] = cachedGroup{
			name:      info.Name,
			updatedAt: time.Now(),
		}
		w.groupMu.Unlock()
		return info.Name
	}

	// Fallback ke cache sebelumnya jika fetch info gagal/timeout
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
		ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
		defer cancel()
		info, err := w.client.GetGroupInfo(ctx, evt.Info.Chat)
		if err == nil && info != nil {
			senderJID := evt.Info.Sender
			for _, p := range info.Participants {
				if (p.JID == senderJID || p.LID == senderJID) && !p.PhoneNumber.IsEmpty() {
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
	if w.isSenderOwner(evt) {
		return true
	}

	info, err := w.client.GetGroupInfo(ctx, groupJID)
	if err != nil || info == nil {
		return false
	}

	senderJID := evt.Info.Sender
	senderAlt := evt.Info.SenderAlt

	for _, p := range info.Participants {
		match := (p.JID == senderJID || p.LID == senderJID || p.PhoneNumber == senderJID)
		if !match && !senderAlt.IsEmpty() {
			match = (p.JID == senderAlt || p.LID == senderAlt || p.PhoneNumber == senderAlt)
		}
		if match && (p.IsAdmin || p.IsSuperAdmin) {
			return true
		}
	}
	return false
}

func (w *WAClient) IsBotGroupAdmin(ctx context.Context, groupJID types.JID) bool {
	if w.client.Store.ID == nil {
		return false
	}
	botUser := w.client.Store.ID.User

	info, err := w.client.GetGroupInfo(ctx, groupJID)
	if err != nil || info == nil {
		return false
	}

	for _, p := range info.Participants {
		if (p.JID.User == botUser || p.PhoneNumber.User == botUser) && (p.IsAdmin || p.IsSuperAdmin) {
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

	prefix, cmd, args, isCmd := w.parseCommand(text)
	if !isCmd {
		// Jika pesan bukan command, cek apakah me-reply pesan dari bot di grup yang terhubung
		if evt.Info.IsGroup && w.isGroupLinked(chatJID) && w.isReplyingToBot(evt) {
			w.forwardChatMessageToMC(evt, text)
			return
		}
		return
	}

	ctx := context.Background()

	switch cmd {
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
		go w.replyServerStatus(evt.Info.Chat)

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

		alreadyLinked := false
		for _, j := range w.config.GroupJIDs {
			if strings.EqualFold(strings.TrimSpace(j), chatJID) {
				alreadyLinked = true
				break
			}
		}

		groupName := w.GetGroupName(evt.Info.Chat)

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
			"_Anggota grup ini sekarang dapat menggunakan perintah %schat <pesan>, %scekserver, %srules, dan %swarn._",
			groupName, chatJID, prefix, prefix, prefix, prefix)
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

		groupName := w.GetGroupName(evt.Info.Chat)
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

	cleanPhoneStr := strings.TrimPrefix(phone, "+")
	jid := types.NewJID(cleanPhoneStr, types.DefaultUserServer)
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
