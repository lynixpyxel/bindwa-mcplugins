package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

const DefaultRulesText = `1. Dilarang lorem ipsum
2. boleh lorem ipsum tapi gak boleh lorem ipsum`

type RulesManager struct {
	filePath string
	rulesMap map[string]string
	mu       sync.RWMutex
}

func NewRulesManager(filePath string) *RulesManager {
	rm := &RulesManager{
		filePath: filePath,
		rulesMap: make(map[string]string),
	}
	rm.load()
	return rm
}

func (rm *RulesManager) load() {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	data, err := os.ReadFile(rm.filePath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &rm.rulesMap)
}

func (rm *RulesManager) save() error {
	data, err := json.MarshalIndent(rm.rulesMap, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal rules: %w", err)
	}
	return os.WriteFile(rm.filePath, data, 0644)
}

func (rm *RulesManager) GetRules(groupJID string) string {
	rm.mu.RLock()
	defer rm.mu.RUnlock()

	if rules, exists := rm.rulesMap[groupJID]; exists && rules != "" {
		return rules
	}
	if defaultRules, exists := rm.rulesMap["default"]; exists && defaultRules != "" {
		return defaultRules
	}
	return DefaultRulesText
}

func (rm *RulesManager) SetRules(groupJID, rulesText string) error {
	rm.mu.Lock()
	defer rm.mu.Unlock()

	rm.rulesMap[groupJID] = rulesText
	return rm.save()
}
