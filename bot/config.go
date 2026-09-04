package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
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
	CasakuLicenseKey     string         `json:"casaku_license_key,omitempty"`
	CasakuQRID           string         `json:"casaku_qr_id,omitempty"`
	CasakuBaseURL        string         `json:"casaku_base_url,omitempty"`
	TripayMerchantCode   string         `json:"tripay_merchant_code,omitempty"`
	TripayAPIKey         string         `json:"tripay_api_key,omitempty"`
	TripayPrivateKey     string         `json:"tripay_private_key,omitempty"`
	TripayMode           string         `json:"tripay_mode,omitempty"`
	MerchantCode         string         `json:"merchant_code,omitempty"`
	APIKey               string         `json:"api_key,omitempty"`
	PrivateKey           string         `json:"private_key,omitempty"`
	Mode                 string         `json:"mode,omitempty"`
	Tripay               TripayConfig   `json:"tripay,omitempty"`
	ImageMapPricing      map[string]int `json:"imagemap_pricing,omitempty"`
	ImageMapPricePerTile int            `json:"imagemap_price_per_tile,omitempty"`
	ImageMapPriceImage   int            `json:"imagemap_price_image"`
	ImageMapPriceGIF     int            `json:"imagemap_price_gif"`
	ImageMapUploadDir    string         `json:"imagemap_upload_dir"`
	PublicURL            string         `json:"public_url,omitempty"`
}

type TripayConfig struct {
	MerchantCode  string `json:"merchant_code"`
	APIKey        string `json:"api_key"`
	PrivateKey    string `json:"private_key"`
	Mode          string `json:"mode"` // "sandbox" or "production"
	PaymentMethod string `json:"payment_method,omitempty"` // default "QRIS"
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
		Tripay: TripayConfig{
			MerchantCode:  "TRIPAY_MERCHANT_CODE",
			APIKey:        "TRIPAY_API_KEY",
			PrivateKey:    "TRIPAY_PRIVATE_KEY",
			Mode:          "sandbox",
			PaymentMethod: "QRIS",
		},
		ImageMapPriceImage: 5000,
		ImageMapPriceGIF:   7000,
		ImageMapUploadDir:  "upload/images",
	}
}

func (cfg *Config) GetTripayConfig() TripayConfig {
	t := cfg.Tripay
	if cfg.TripayMerchantCode != "" {
		t.MerchantCode = cfg.TripayMerchantCode
	}
	if cfg.MerchantCode != "" {
		t.MerchantCode = cfg.MerchantCode
	}
	if cfg.TripayAPIKey != "" {
		t.APIKey = cfg.TripayAPIKey
	}
	if cfg.APIKey != "" {
		t.APIKey = cfg.APIKey
	}
	if cfg.TripayPrivateKey != "" {
		t.PrivateKey = cfg.TripayPrivateKey
	}
	if cfg.PrivateKey != "" {
		t.PrivateKey = cfg.PrivateKey
	}
	if cfg.TripayMode != "" {
		t.Mode = cfg.TripayMode
	}
	if cfg.Mode != "" {
		t.Mode = cfg.Mode
	}
	if t.Mode == "" {
		t.Mode = "sandbox"
	}
	if t.PaymentMethod == "" {
		t.PaymentMethod = "QRIS"
	}
	return t
}

func (t TripayConfig) GetBaseURL() string {
	mode := strings.ToLower(strings.TrimSpace(t.Mode))
	if mode == "production" || mode == "live" {
		return "https://tripay.co.id/api"
	}
	return "https://tripay.co.id/api-sandbox"
}

func (cfg *Config) CalculateImageMapPrice(width, height int, isGIF bool) int {
	if isGIF {
		if cfg.ImageMapPriceGIF > 0 {
			return cfg.ImageMapPriceGIF
		}
		return 7000
	}
	if cfg.ImageMapPriceImage > 0 {
		return cfg.ImageMapPriceImage
	}
	return 5000
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
	if envMerch := os.Getenv("TRIPAY_MERCHANT_CODE"); envMerch != "" {
		cfg.Tripay.MerchantCode = envMerch
	}
	if envAPI := os.Getenv("TRIPAY_API_KEY"); envAPI != "" {
		cfg.Tripay.APIKey = envAPI
	}
	if envPriv := os.Getenv("TRIPAY_PRIVATE_KEY"); envPriv != "" {
		cfg.Tripay.PrivateKey = envPriv
	}
	if envMode := os.Getenv("TRIPAY_MODE"); envMode != "" {
		cfg.Tripay.Mode = envMode
	}
	if cfg.ImageMapUploadDir == "" {
		cfg.ImageMapUploadDir = "upload/images"
	}
	if cfg.ImageMapPriceImage <= 0 {
		cfg.ImageMapPriceImage = 5000
	}
	if cfg.ImageMapPriceGIF <= 0 {
		cfg.ImageMapPriceGIF = 7000
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

