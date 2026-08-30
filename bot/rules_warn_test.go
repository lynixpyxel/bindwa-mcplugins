package main

import (
	"os"
	"testing"

	"go.mau.fi/whatsmeow/types"
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
