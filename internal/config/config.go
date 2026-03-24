package config

import (
	"os"
)

type Config struct {
	DatabaseURL   string
	Port          string
	SessionSecret string
	BackupPath    string
}

func Load() *Config {
	return &Config{
		DatabaseURL:   getEnv("DATABASE_URL", "postgres://finance:finance@localhost:5432/finance?sslmode=disable"),
		Port:          getEnv("PORT", "8443"),
		SessionSecret: getEnv("SESSION_SECRET", "change-me"),
		BackupPath:    getEnv("BACKUP_PATH", "/backups"),
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
