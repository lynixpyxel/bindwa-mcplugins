package main

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"time"
)

var (
	ErrCooldown            = errors.New("otp cooldown active")
	ErrOTPNotFoundOrExpired = errors.New("otp expired or not found")
	ErrMaxAttemptsExceeded = errors.New("max attempts exceeded")
	ErrWrongOTP            = errors.New("wrong otp")
)

type OTPEntry struct {
	Code      string
	ExpiresAt time.Time
	Attempts  int
	CreatedAt time.Time
}

type OTPStore struct {
	mu          sync.RWMutex
	store       map[string]OTPEntry
	ttl         time.Duration
	cooldown    time.Duration
	maxAttempts int
}

func NewOTPStore(ttlSeconds, cooldownSeconds, maxAttempts int) *OTPStore {
	return &OTPStore{
		store:       make(map[string]OTPEntry),
		ttl:         time.Duration(ttlSeconds) * time.Second,
		cooldown:    time.Duration(cooldownSeconds) * time.Second,
		maxAttempts: maxAttempts,
	}
}

func (s *OTPStore) key(uuid, phone string) string {
	return uuid + "|" + phone
}

func generateNumericCode(length int) (string, error) {
	code := ""
	for i := 0; i < length; i++ {
		num, err := rand.Int(rand.Reader, big.NewInt(10))
		if err != nil {
			return "", err
		}
		code += fmt.Sprintf("%d", num.Int64())
	}
	return code, nil
}

func (s *OTPStore) Generate(uuid, phone string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := s.key(uuid, phone)
	now := time.Now()

	if entry, exists := s.store[k]; exists {
		// Cek cooldown
		if now.Sub(entry.CreatedAt) < s.cooldown {
			return "", ErrCooldown
		}
	}

	code, err := generateNumericCode(6)
	if err != nil {
		return "", fmt.Errorf("failed to generate otp: %w", err)
	}

	s.store[k] = OTPEntry{
		Code:      code,
		ExpiresAt: now.Add(s.ttl),
		Attempts:  0,
		CreatedAt: now,
	}

	return code, nil
}

type VerifyResult struct {
	Success      bool
	AttemptsLeft int
}

func (s *OTPStore) Verify(uuid, phone, inputCode string) (VerifyResult, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	k := s.key(uuid, phone)
	entry, exists := s.store[k]
	now := time.Now()

	if !exists || now.After(entry.ExpiresAt) {
		if exists {
			delete(s.store, k)
		}
		return VerifyResult{}, ErrOTPNotFoundOrExpired
	}

	if entry.Attempts >= s.maxAttempts {
		delete(s.store, k)
		return VerifyResult{}, ErrMaxAttemptsExceeded
	}

	if entry.Code != inputCode {
		entry.Attempts++
		if entry.Attempts >= s.maxAttempts {
			delete(s.store, k)
			return VerifyResult{Success: false, AttemptsLeft: 0}, ErrMaxAttemptsExceeded
		}
		s.store[k] = entry
		return VerifyResult{Success: false, AttemptsLeft: s.maxAttempts - entry.Attempts}, ErrWrongOTP
	}

	// Sukses verifikasi -> hapus dari store (one-time use)
	delete(s.store, k)
	return VerifyResult{Success: true, AttemptsLeft: s.maxAttempts - entry.Attempts}, nil
}

func (s *OTPStore) StartCleanup(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case now := <-ticker.C:
				s.mu.Lock()
				for k, v := range s.store {
					if now.After(v.ExpiresAt) {
						delete(s.store, k)
					}
				}
				s.mu.Unlock()
			}
		}
	}()
}
