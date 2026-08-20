package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	DefaultWelcomeTemplate = `Halo %name! Selamat datang di grup *%groupname* 🎉
Tanggal: %date (%time)

📌 Ketik *.rules* untuk membaca peraturan grup.
🎮 Ketik *.cekserver* untuk cek status server Minecraft.
💬 Ketik *.chat <pesan>* untuk kirim chat ke in-game.`

	DefaultLeaveTemplate = `%name telah keluar dari grup *%groupname*. Sampai jumpa lagi! ✨`
)

type GroupWelcomeData struct {
	WelcomeText string `json:"welcome_text"`
	LeaveText   string `json:"leave_text"`
}

type WelcomeManager struct {
	filePath string
	dataMap  map[string]GroupWelcomeData // groupJID -> GroupWelcomeData
	mu       sync.RWMutex
}

func NewWelcomeManager(filePath string) *WelcomeManager {
	wm := &WelcomeManager{
		filePath: filePath,
		dataMap:  make(map[string]GroupWelcomeData),
	}
	wm.load()
	return wm
}

func (wm *WelcomeManager) load() {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	data, err := os.ReadFile(wm.filePath)
	if err != nil {
		return
	}
	_ = json.Unmarshal(data, &wm.dataMap)
	if wm.dataMap == nil {
		wm.dataMap = make(map[string]GroupWelcomeData)
	}
}

func (wm *WelcomeManager) save() error {
	data, err := json.MarshalIndent(wm.dataMap, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal welcome data: %w", err)
	}
	return os.WriteFile(wm.filePath, data, 0644)
}

func (wm *WelcomeManager) GetWelcomeTemplate(groupJID string) string {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	if d, exists := wm.dataMap[groupJID]; exists && strings.TrimSpace(d.WelcomeText) != "" {
		return d.WelcomeText
	}
	if def, exists := wm.dataMap["default"]; exists && strings.TrimSpace(def.WelcomeText) != "" {
		return def.WelcomeText
	}
	return DefaultWelcomeTemplate
}

func (wm *WelcomeManager) SetWelcomeTemplate(groupJID, template string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	d := wm.dataMap[groupJID]
	d.WelcomeText = strings.TrimSpace(template)
	wm.dataMap[groupJID] = d
	return wm.save()
}

func (wm *WelcomeManager) GetLeaveTemplate(groupJID string) string {
	wm.mu.RLock()
	defer wm.mu.RUnlock()

	if d, exists := wm.dataMap[groupJID]; exists && strings.TrimSpace(d.LeaveText) != "" {
		return d.LeaveText
	}
	if def, exists := wm.dataMap["default"]; exists && strings.TrimSpace(def.LeaveText) != "" {
		return def.LeaveText
	}
	return DefaultLeaveTemplate
}

func (wm *WelcomeManager) SetLeaveTemplate(groupJID, template string) error {
	wm.mu.Lock()
	defer wm.mu.Unlock()

	d := wm.dataMap[groupJID]
	d.LeaveText = strings.TrimSpace(template)
	wm.dataMap[groupJID] = d
	return wm.save()
}

func (wm *WelcomeManager) FormatMessage(template, userName, groupName string) string {
	now := time.Now()
	dateStr := now.Format("02/01/2006")
	timeStr := now.Format("15:04 WIB")

	msg := template
	// Replace %name & %user
	msg = strings.ReplaceAll(msg, "%name", "@"+userName)
	msg = strings.ReplaceAll(msg, "%user", "@"+userName)
	// Replace %groupname & %group
	msg = strings.ReplaceAll(msg, "%groupname", groupName)
	msg = strings.ReplaceAll(msg, "%group", groupName)
	// Replace %date & %time
	msg = strings.ReplaceAll(msg, "%date", dateStr)
	msg = strings.ReplaceAll(msg, "%time", timeStr)

	return msg
}
