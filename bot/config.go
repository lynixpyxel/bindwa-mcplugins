package main

import (
	"encoding/json"
	"fmt"
	"os"
)

type Config struct {
	HTTPPort           int      `json:"http_port"`
	HTTPBind           string   `json:"http_bind"`
	APIToken           string   `json:"api_token"`
	GroupJIDs          []string `json:"group_jids"`
	ServerName         string   `json:"server_name"`
	CommandPrefixes    []string `json:"command_prefixes"`
	OTPTTLSeconds      int      `json:"otp_ttl_seconds"`
	OTPCooldownSeconds int      `json:"otp_cooldown_seconds"`
	OTPMaxAttempts     int      `json:"otp_max_attempts"`
}

func DefaultConfig() Config {
	return Config{
		HTTPPort:           3636,
		HTTPBind:           "0.0.0.0",
		APIToken:           "DOZZY_GANTENG_ABIEZZZZRAWRRRRR123",
		GroupJIDs:          []string{"6288287243319-1620284343@g.us"},
		ServerName:         "CSMP Minecraft Server",
		CommandPrefixes:    []string{".", "!", "#", "?"},
		OTPTTLSeconds:      300,
		OTPCooldownSeconds: 60,
		OTPMaxAttempts:     5,
	}
}

func LoadConfig(path string) (Config, error) {
	cfg := DefaultConfig()

	file, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			data, marshalErr := json.MarshalIndent(cfg, "", "  ")
			if marshalErr != nil {
				return cfg, fmt.Errorf("failed to marshal default config: %w", marshalErr)
			}
			if writeErr := os.WriteFile(path, data, 0600); writeErr != nil {
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
	if cfg.OTPTTLSeconds <= 0 {
		cfg.OTPTTLSeconds = 300
	}
	if cfg.OTPCooldownSeconds <= 0 {
		cfg.OTPCooldownSeconds = 60
	}
	if cfg.OTPMaxAttempts <= 0 {
		cfg.OTPMaxAttempts = 5
	}

	return cfg, nil
}
