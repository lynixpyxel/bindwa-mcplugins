package main

import (
	"os"
	"testing"
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
