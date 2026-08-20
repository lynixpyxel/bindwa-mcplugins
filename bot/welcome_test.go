package main

import (
	"os"
	"strings"
	"testing"
)

func TestWelcomeManager(t *testing.T) {
	tempFile := "test_group_welcome.json"
	defer os.Remove(tempFile)

	wm := NewWelcomeManager(tempFile)

	groupJID := "123456789-test@g.us"

	// 1. Default template
	defaultTpl := wm.GetWelcomeTemplate(groupJID)
	if !strings.Contains(defaultTpl, "%name") {
		t.Fatalf("expected default template to contain %%name, got %s", defaultTpl)
	}

	// 2. Set custom welcome template
	customWelcome := "Halo %name selamat datang di %groupname pada tanggal %date jam %time!"
	err := wm.SetWelcomeTemplate(groupJID, customWelcome)
	if err != nil {
		t.Fatalf("failed to set welcome template: %v", err)
	}

	retrieved := wm.GetWelcomeTemplate(groupJID)
	if retrieved != customWelcome {
		t.Fatalf("expected custom welcome, got %s", retrieved)
	}

	// 3. Format message
	formatted := wm.FormatMessage(retrieved, "628123456789", "Minecraft Indo")
	if !strings.Contains(formatted, "@628123456789") {
		t.Fatalf("expected @628123456789 in formatted message, got %s", formatted)
	}
	if !strings.Contains(formatted, "Minecraft Indo") {
		t.Fatalf("expected Minecraft Indo in formatted message, got %s", formatted)
	}
	if strings.Contains(formatted, "%date") || strings.Contains(formatted, "%time") {
		t.Fatalf("variables %%date and %%time should have been replaced, got %s", formatted)
	}

	// 4. Set custom leave template
	customLeave := "%name telah keluar dari %groupname."
	err = wm.SetLeaveTemplate(groupJID, customLeave)
	if err != nil {
		t.Fatalf("failed to set leave template: %v", err)
	}

	retrievedLeave := wm.GetLeaveTemplate(groupJID)
	formattedLeave := wm.FormatMessage(retrievedLeave, "628999999", "Minecraft Indo")
	if !strings.Contains(formattedLeave, "@628999999 telah keluar dari Minecraft Indo.") {
		t.Fatalf("unexpected leave formatted string: %s", formattedLeave)
	}
}
