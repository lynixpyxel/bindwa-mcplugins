package main

import (
	"context"
	"os"
	"testing"
	"time"

	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

func TestRulesManager(t *testing.T) {
	tempFile := "test_rules.json"
	defer os.Remove(tempFile)

	rm := NewRulesManager(tempFile)

	// Test default rules
	defaultRules := rm.GetRules("123@g.us")
	if defaultRules == "" {
		t.Errorf("Expected default rules to not be empty")
	}

	// Test set rules
	newRules := "1. Jangan rusuh\n2. Hormati sesama"
	err := rm.SetRules("123@g.us", newRules)
	if err != nil {
		t.Fatalf("Failed to set rules: %v", err)
	}

	retrieved := rm.GetRules("123@g.us")
	if retrieved != newRules {
		t.Errorf("Expected rules '%s', got '%s'", newRules, retrieved)
	}
}

func TestWarnManager(t *testing.T) {
	tempFile := "test_warns.json"
	defer os.Remove(tempFile)

	wm := NewWarnManager(tempFile)

	// Test initial count
	if count := wm.GetWarn("123@g.us", "62812345"); count != 0 {
		t.Errorf("Expected 0 warns, got %d", count)
	}

	// Test add warn 1
	w1 := wm.AddWarn("123@g.us", "62812345")
	if w1 != 1 {
		t.Errorf("Expected 1 warn, got %d", w1)
	}

	// Test add warn 2
	w2 := wm.AddWarn("123@g.us", "62812345")
	if w2 != 2 {
		t.Errorf("Expected 2 warns, got %d", w2)
	}

	// Test reset warn
	wm.ResetWarn("123@g.us", "62812345")
	if count := wm.GetWarn("123@g.us", "62812345"); count != 0 {
		t.Errorf("Expected 0 warns after reset, got %d", count)
	}
}

func TestPhoneMatchingAndMentions(t *testing.T) {
	// Strict phone match tests
	if isPhoneMatch("1", "6285294959195") {
		t.Errorf("Single digit should NOT match phone number")
	}
	if isPhoneMatch("8", "6285294959195") {
		t.Errorf("Single digit '8' should NOT match phone number")
	}
	if isPhoneMatch("852", "6285294959195") {
		t.Errorf("Partial prefix '852' should NOT match full phone number")
	}
	if !isPhoneMatch("085294959195", "6285294959195") {
		t.Errorf("085294959195 should match 6285294959195")
	}
	if !isPhoneMatch("+6285294959195", "6285294959195") {
		t.Errorf("+6285294959195 should match 6285294959195")
	}

	// WAClient mention tests
	client := &WAClient{
		participantMap: make(map[string]string),
	}
	client.recordParticipant("Dozzy", "6285294959195", "6285294959195@s.whatsapp.net")
	client.recordParticipant("Budi", "6281234567890", "6281234567890@s.whatsapp.net")

	// Test exact name mention
	m1 := client.resolveMentionsInText(nil, types.EmptyJID, "Halo @Dozzy apa kabar")
	if len(m1) != 1 || m1[0] != "6285294959195@s.whatsapp.net" {
		t.Errorf("Expected 1 mention for Dozzy, got %v", m1)
	}

	// Test Minecraft selectors should NOT trigger mentions
	m2 := client.resolveMentionsInText(nil, types.EmptyJID, "Coba /msg @a halo")
	if len(m2) != 0 {
		t.Errorf("Minecraft selector @a should NOT trigger mentions, got %v", m2)
	}

	m3 := client.resolveMentionsInText(nil, types.EmptyJID, "Coba /msg @p atau @s")
	if len(m3) != 0 {
		t.Errorf("Minecraft selector @p/@s should NOT trigger mentions, got %v", m3)
	}

	// Test short numbers or random words should NOT trigger mentions
	m4 := client.resolveMentionsInText(nil, types.EmptyJID, "Beli level @1 atau @2")
	if len(m4) != 0 {
		t.Errorf("Short numbers should NOT trigger mentions, got %v", m4)
	}
}

func TestIsUserGroupAdminMultiDeviceAndLID(t *testing.T) {
	groupJIDA := types.NewJID("120363040000000000", types.GroupServer)
	groupJIDB := types.NewJID("120363099999999999", types.GroupServer)
	adminPhone := "6285294959195"
	adminLID := "109876543210"

	groupInfoA := &types.GroupInfo{
		JID: groupJIDA,
		Participants: []types.GroupParticipant{
			{
				JID:         types.NewJID(adminPhone, types.DefaultUserServer),
				LID:         types.NewJID(adminLID, types.HiddenUserServer),
				PhoneNumber: types.NewJID(adminPhone, types.DefaultUserServer),
				IsAdmin:     true,
			},
			{
				JID:         types.NewJID("6281234567890", types.DefaultUserServer),
				PhoneNumber: types.NewJID("6281234567890", types.DefaultUserServer),
				IsAdmin:     false,
			},
		},
	}

	// Group B: adminPhone is NOT admin in Group B (only a regular member)
	groupInfoB := &types.GroupInfo{
		JID: groupJIDB,
		Participants: []types.GroupParticipant{
			{
				JID:         types.NewJID(adminPhone, types.DefaultUserServer),
				LID:         types.NewJID(adminLID, types.HiddenUserServer),
				PhoneNumber: types.NewJID(adminPhone, types.DefaultUserServer),
				IsAdmin:     false,
			},
			{
				JID:         types.NewJID("6287777777777", types.DefaultUserServer),
				PhoneNumber: types.NewJID("6287777777777", types.DefaultUserServer),
				IsAdmin:     true,
			},
		},
	}

	w := &WAClient{
		config: Config{
			OwnerNumber: "6289999999999",
			GroupJIDs:   []string{groupJIDA.String()},
		},
		groupNames: map[string]cachedGroup{
			groupJIDA.String(): {
				name:      "Group A",
				info:      groupInfoA,
				updatedAt: time.Now(),
			},
			groupJIDB.String(): {
				name:      "Group B",
				info:      groupInfoB,
				updatedAt: time.Now(),
			},
		},
	}

	// Case 1: Sender has device ID != 0 (e.g. multi-device WhatsApp: 6285294959195:12@s.whatsapp.net)
	evtMD := &events.Message{}
	evtMD.Info.Sender = types.JID{
		User:   adminPhone,
		Device: 12,
		Server: types.DefaultUserServer,
	}
	if !w.IsUserGroupAdmin(context.Background(), groupJIDA, evtMD) {
		t.Errorf("Expected multi-device admin to be recognized as admin in Group A")
	}

	// Case 2: Sender uses LID with Device ID (e.g. 109876543210:2@lid)
	evtLID := &events.Message{}
	evtLID.Info.Sender = types.JID{
		User:   adminLID,
		Device: 2,
		Server: types.HiddenUserServer,
	}
	if !w.IsUserGroupAdmin(context.Background(), groupJIDA, evtLID) {
		t.Errorf("Expected LID sender to be recognized as admin in Group A")
	}

	// Case 3: Admin of Group A trying to run admin commands (like linkgroup/unlinkgroup) in Group B -> MUST BE REJECTED!
	if w.IsUserGroupAdmin(context.Background(), groupJIDB, evtMD) {
		t.Errorf("Security flaw: User A is admin in Group A, but must NOT be recognized as admin in Group B!")
	}

	// Case 4: Admin of Group A in DM with bot cannot be group admin (since DM is not a group)
	dmJID := types.NewJID(adminPhone, types.DefaultUserServer)
	if w.IsUserGroupAdmin(context.Background(), dmJID, evtMD) {
		t.Errorf("Expected IsUserGroupAdmin to return false for DM chat")
	}

	// Case 5: Admin of Group A CAN moderate orders in DM with bot
	if !w.IsOrderModerator(context.Background(), dmJID, evtMD) {
		t.Errorf("Expected configured group admin to be allowed to moderate orders in DM")
	}

	// Case 6: Admin of Group A CANNOT moderate orders in Group B (where they are not admin)
	if w.IsOrderModerator(context.Background(), groupJIDB, evtMD) {
		t.Errorf("Expected user to NOT be order moderator in Group B where they are regular member")
	}

	// Case 7: Non-admin member should not be admin in Group A
	evtNonAdmin := &events.Message{}
	evtNonAdmin.Info.Sender = types.NewJID("6281234567890", types.DefaultUserServer)
	if w.IsUserGroupAdmin(context.Background(), groupJIDA, evtNonAdmin) {
		t.Errorf("Expected regular member NOT to be recognized as admin in Group A")
	}

	// Case 8: Owner should always be admin even in Group B where owner is not in participant list
	evtOwner := &events.Message{}
	evtOwner.Info.Sender = types.NewJID("6289999999999", types.DefaultUserServer)
	if !w.IsUserGroupAdmin(context.Background(), groupJIDB, evtOwner) {
		t.Errorf("Expected bot owner to be recognized as admin in Group B")
	}
}

