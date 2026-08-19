package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
)

type WarnManager struct {
	filePath string
	warnsMap map[string]map[string]int // groupJID -> userJID -> warnCount
	mu       sync.RWMutex
}

func NewWarnManager(filePath string) *WarnManager {
	wm := &WarnManager{
		filePath: filePath,
		warnsMap: make(map[string]map[string]int),
	}
	wm.load()
	return wm
}

func (wm *WarnManager) load() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	data, err := os.ReadFile(wm.filePath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &wm.warnsMap)
	if wm.warnsMap == nil {
		wm.warnsMap = make(map[string]map[string]int)
	}
}

func (wm *WarnManager) save() error {
	data, err := json.MarshalIndent(wm.warnsMap, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal warns: %w", err)
	}
	return os.WriteFile(wm.filePath, data, 0644)
}

func (wm *WarnManager) GetWarn(groupJID, userJID string) int {
	wm.mu.RLock()
	defer rmUnlock(wm)

	if users, exists := wm.warnsMap[groupJID]; exists {
		return users[userJID]
	}
	return 0
}

func rmUnlock(wm *WarnManager) {
	wm.mu.RUnlock()
}

func (wm *WarnManager) AddWarn(groupJID, userJID string) int {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if _, exists := wm.warnsMap[groupJID]; !exists {
		wm.warnsMap[groupJID] = make(map[string]int)
	}

	wm.warnsMap[groupJID][userJID]++
	count := wm.warnsMap[groupJID][userJID]
	_ = wm.save()
	return count
}

func (wm *WarnManager) ResetWarn(groupJID, userJID string) {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	if users, exists := wm.warnsMap[groupJID]; exists {
		delete(users, userJID)
		_ = wm.save()
	}
}
