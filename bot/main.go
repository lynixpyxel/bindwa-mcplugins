package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"
)

func main() {
	configPath := flag.String("config", "config.json", "Path to config file")
	portFlag := flag.Int("port", 0, "Override HTTP port")
	dryRun := flag.Bool("dry-run", false, "Run in dry-run mode without connecting to WhatsApp (for API testing)")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// Dukungan override port dari Flag atau Environment Variable Pterodactyl (SERVER_PORT / PORT)
	if *portFlag > 0 {
		cfg.HTTPPort = *portFlag
	} else if envPort := os.Getenv("SERVER_PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			cfg.HTTPPort = p
		}
	} else if envPort := os.Getenv("PORT"); envPort != "" {
		if p, err := strconv.Atoi(envPort); err == nil && p > 0 {
			cfg.HTTPPort = p
		}
	}

	fmt.Println("==================================================")
	fmt.Println("         BindWA - WhatsApp Bot Service            ")
	fmt.Println("==================================================")
	fmt.Printf("HTTP Bind Address : %s:%d\n", cfg.HTTPBind, cfg.HTTPPort)
	fmt.Printf("OTP TTL           : %d seconds\n", cfg.OTPTTLSeconds)
	fmt.Printf("OTP Cooldown      : %d seconds\n", cfg.OTPCooldownSeconds)
	fmt.Printf("OTP Max Attempts  : %d\n", cfg.OTPMaxAttempts)
	fmt.Println("==================================================")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Inisialisasi OTP Store
	otpStore := NewOTPStore(cfg.OTPTTLSeconds, cfg.OTPCooldownSeconds, cfg.OTPMaxAttempts)
	otpStore.StartCleanup(ctx, 30*time.Second)

	var waClient *WAClient
	if !*dryRun {
		var waErr error
		waClient, waErr = NewWAClient(ctx, "whatsapp_session.db", cfg)
		if waErr != nil {
			log.Fatalf("Failed to initialize WhatsApp client: %v", waErr)
		}

		if err := waClient.Start(ctx); err != nil {
			log.Fatalf("Failed to start WhatsApp client: %v", err)
		}
	} else {
		fmt.Println("[WARNING] Running in DRY-RUN mode. WhatsApp network connection is disabled.")
	}

	// Inisialisasi HTTP Server
	server := NewServer(cfg, otpStore, waClient)

	go func() {
		if err := server.Start(); err != nil {
			log.Fatalf("Server error: %v", err)
		}
	}()

	// Graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down BindWA Bot...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		fmt.Printf("HTTP server shutdown error: %v\n", err)
	}

	if waClient != nil {
		waClient.Disconnect()
	}

	fmt.Println("BindWA Bot successfully stopped.")
}
