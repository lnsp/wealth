package config

import (
	"os"
	"strconv"
)

type Config struct {
	DatabaseURL   string
	Port          string
	SessionSecret string
	BackupPath    string
	AdminPassword string // If empty, auth is disabled

	// Configurable thresholds (with sensible defaults)
	BackupAgeRecipient     string // age public key for encrypted backups (optional)
	BaseURL                string // e.g. "https://wealth.example.com" — used for WebAuthn RP origin

	BenchmarkISIN          string
	BenchmarkTicker        string
	PriceFetchConcurrency  int
	RequestTimeoutSeconds  int
	OverlapWarningPct      float64
	ConcentrationWarningPct float64
	TrustedProxies         string // comma-separated CIDRs for X-Forwarded-* headers

	RateLimitAPI           int // requests per minute for API endpoints
	RateLimitUpload        int // requests per hour for uploads
	RateLimitReport        int // requests per hour for report generation
}

func Load() *Config {
	return &Config{
		DatabaseURL:   getEnv("DATABASE_URL", "postgres://finance:finance@localhost:5432/finance?sslmode=disable"),
		Port:          getEnv("PORT", "8443"),
		SessionSecret: getEnv("SESSION_SECRET", "change-me"),
		BackupPath:    getEnv("BACKUP_PATH", "/backups"),
		AdminPassword: getEnv("ADMIN_PASSWORD", ""),

		BackupAgeRecipient:     getEnv("BACKUP_AGE_RECIPIENT", ""),
		BaseURL:                getEnv("BASE_URL", "http://localhost:8443"),

		BenchmarkISIN:          getEnv("BENCHMARK_ISIN", "IE00B4L5Y983"),
		BenchmarkTicker:        getEnv("BENCHMARK_TICKER", "IWDA.AS"),
		PriceFetchConcurrency:  getEnvInt("PRICE_FETCH_CONCURRENCY", 10),
		RequestTimeoutSeconds:  getEnvInt("REQUEST_TIMEOUT_SECONDS", 60),
		OverlapWarningPct:      getEnvFloat("OVERLAP_WARNING_PCT", 70.0),
		ConcentrationWarningPct: getEnvFloat("CONCENTRATION_WARNING_PCT", 5.0),
		TrustedProxies:         getEnv("TRUSTED_PROXIES", ""), // e.g. "127.0.0.1,10.0.0.0/8"

		RateLimitAPI:           getEnvInt("RATE_LIMIT_API", 600),
		RateLimitUpload:        getEnvInt("RATE_LIMIT_UPLOAD", 30),
		RateLimitReport:        getEnvInt("RATE_LIMIT_REPORT", 20),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			return n
		}
	}
	return fallback
}

func getEnvFloat(key string, fallback float64) float64 {
	if v := os.Getenv(key); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			return f
		}
	}
	return fallback
}
