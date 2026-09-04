package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	HTTPPort             int            `json:"http_port"`
	HTTPBind             string         `json:"http_bind"`
	APIToken             string         `json:"api_token"`
	GroupJIDs            []string       `json:"group_jids"`
	LogGroupJIDs         []string       `json:"log_group_jids"`
	ServerName           string         `json:"server_name"`
	CommandPrefixes      []string       `json:"command_prefixes"`
	OwnerNumber          string         `json:"owner_number"`
	OTPTTLSeconds        int            `json:"otp_ttl_seconds"`
	OTPCooldownSeconds   int            `json:"otp_cooldown_seconds"`
	OTPMaxAttempts       int            `json:"otp_max_attempts"`
	CasakuLicenseKey     string         `json:"casaku_license_key"`
	CasakuQRID           string         `json:"casaku_qr_id"`
	CasakuBaseURL        string         `json:"casaku_base_url"`
	ImageMapPricing      map[string]int `json:"imagemap_pricing"`
	ImageMapPricePerTile int            `json:"imagemap_price_per_tile"`
	ImageMapUploadDir    string         `json:"imagemap_upload_dir"`
}

func DefaultConfig() Config {
	return Config{
		HTTPPort:           3636,
		HTTPBind:           "0.0.0.0",
		APIToken:           "DOZZY_GANTENG_ABIEZZZZRAWRRRRR123",
		GroupJIDs:          []string{"6288287243319-1620284343@g.us"},
		LogGroupJIDs:       []string{},
		ServerName:         "CSMP Minecraft Server",
		CommandPrefixes:    []string{".", "!", "#", "?"},
		OwnerNumber:        "6285294959195",
		OTPTTLSeconds:      300,
		OTPCooldownSeconds: 60,
		OTPMaxAttempts:     5,
		CasakuLicenseKey:   "cashify_a236950d9c2edcc2db4d18d7f593320e6d37fa2ebd6a85959b5cd488a0913ef0",
		CasakuQRID:         "8f416161-b590-407a-aefc-17b4d2fb3666",
		CasakuBaseURL:      "https://api.casaku.id",
		ImageMapPricing: map[string]int{
			"1x1": 1000,
			"3x3": 2500,
		},
		ImageMapPricePerTile: 1000,
		ImageMapUploadDir:    "upload/images",
	}
}

func (cfg *Config) CalculateImageMapPrice(width, height int) int {
	key := fmt.Sprintf("%dx%d", width, height)
	if p, ok := cfg.ImageMapPricing[key]; ok && p > 0 {
		return p
	}
	revKey := fmt.Sprintf("%dx%d", height, width)
	if p, ok := cfg.ImageMapPricing[revKey]; ok && p > 0 {
		return p
	}
	tiles := width * height
	rate := cfg.ImageMapPricePerTile
	if rate <= 0 {
		rate = 1000
	}
	return tiles * rate
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			if writeErr := SaveConfig(path, cfg); writeErr != nil {
				return cfg, fmt.Errorf("failed to write default config: %w", writeErr)
			}
			return cfg, nil
		}
		return cfg, fmt.Errorf("failed to open config file: %w", err)
	}
	defer file.Close()

	if err := json.NewDecoder(file).Decode(&cfg); err != nil {
		return cfg, fmt.Errorf("failed to parse config file: %w", err)
	}

	if cfg.HTTPPort <= 0 {
		cfg.HTTPPort = 3636
	}
	if cfg.HTTPBind == "" {
		cfg.HTTPBind = "0.0.0.0"
	}
	if cfg.ServerName == "" {
		cfg.ServerName = "CSMP Minecraft Server"
	}
	if len(cfg.CommandPrefixes) == 0 {
		cfg.CommandPrefixes = []string{".", "!", "#", "?"}
	}
	if cfg.OwnerNumber == "" {
		cfg.OwnerNumber = "6285294959195"
	}
	if cfg.OTPTTLSeconds <= 0 {
		cfg.OTPTTLSeconds = 300
	}
	if cfg.OTPCooldownSeconds <= 0 {
		cfg.OTPCooldownSeconds = 60
	}
	if cfg.OTPMaxAttempts <= 0 {
		cfg.OTPMaxAttempts = 5
	}
	if envKey := os.Getenv("CASAKU_LICENSE_KEY"); envKey != "" {
		cfg.CasakuLicenseKey = envKey
	}
	if envQR := os.Getenv("CASAKU_QR_ID"); envQR != "" {
		cfg.CasakuQRID = envQR
	}
	if envBase := os.Getenv("CASAKU_BASE_URL"); envBase != "" {
		cfg.CasakuBaseURL = envBase
	}
	if cfg.CasakuBaseURL == "" {
		cfg.CasakuBaseURL = "https://api.casaku.id"
	}
	if cfg.ImageMapUploadDir == "" {
		cfg.ImageMapUploadDir = "upload/images"
	}
	if cfg.ImageMapPricePerTile <= 0 {
		cfg.ImageMapPricePerTile = 1000
	}
	if cfg.ImageMapPricing == nil {
		cfg.ImageMapPricing = map[string]int{
			"1x1": 1000,
			"3x3": 2500,
		}
	}

	return cfg, nil
}

func SaveConfig(path string, cfg Config) error {
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}
	return os.WriteFile(path, data, 0644)
}

