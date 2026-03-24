package config

import (
	"os"
	"testing"
)

func TestGetEnv(t *testing.T) {
	tests := []struct {
		name     string
		key      string
		envValue string
		setEnv   bool
		fallback string
		want     string
	}{
		{
			name:     "returns env value when set",
			key:      "TEST_GET_ENV_SET",
			envValue: "custom_value",
			setEnv:   true,
			fallback: "default",
			want:     "custom_value",
		},
		{
			name:     "returns fallback when not set",
			key:      "TEST_GET_ENV_UNSET",
			setEnv:   false,
			fallback: "default_value",
			want:     "default_value",
		},
		{
			name:     "returns fallback when empty",
			key:      "TEST_GET_ENV_EMPTY",
			envValue: "",
			setEnv:   true,
			fallback: "fallback",
			want:     "fallback",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv(tt.key)
			if tt.setEnv {
				os.Setenv(tt.key, tt.envValue)
				t.Cleanup(func() { os.Unsetenv(tt.key) })
			}

			got := getEnv(tt.key, tt.fallback)
			if got != tt.want {
				t.Errorf("getEnv(%q, %q) = %q, want %q", tt.key, tt.fallback, got, tt.want)
			}
		})
	}
}

func TestLoad(t *testing.T) {
	t.Run("defaults when no env vars set", func(t *testing.T) {
		// Clear any env vars that might be set
		for _, key := range []string{"DATABASE_URL", "PORT", "SESSION_SECRET", "BACKUP_PATH"} {
			prev := os.Getenv(key)
			os.Unsetenv(key)
			t.Cleanup(func() {
				if prev != "" {
					os.Setenv(key, prev)
				}
			})
		}

		cfg := Load()

		if cfg.DatabaseURL != "postgres://finance:finance@localhost:5432/finance?sslmode=disable" {
			t.Errorf("DatabaseURL = %q, want default", cfg.DatabaseURL)
		}
		if cfg.Port != "8443" {
			t.Errorf("Port = %q, want 8443", cfg.Port)
		}
		if cfg.SessionSecret != "change-me" {
			t.Errorf("SessionSecret = %q, want change-me", cfg.SessionSecret)
		}
		if cfg.BackupPath != "/backups" {
			t.Errorf("BackupPath = %q, want /backups", cfg.BackupPath)
		}
	})

	t.Run("reads from environment", func(t *testing.T) {
		envs := map[string]string{
			"DATABASE_URL":   "postgres://test:test@db:5432/test",
			"PORT":           "9090",
			"SESSION_SECRET": "super-secret",
			"BACKUP_PATH":    "/tmp/backups",
		}
		for k, v := range envs {
			prev := os.Getenv(k)
			os.Setenv(k, v)
			t.Cleanup(func() {
				if prev != "" {
					os.Setenv(k, prev)
				} else {
					os.Unsetenv(k)
				}
			})
		}

		cfg := Load()

		if cfg.DatabaseURL != "postgres://test:test@db:5432/test" {
			t.Errorf("DatabaseURL = %q, want custom value", cfg.DatabaseURL)
		}
		if cfg.Port != "9090" {
			t.Errorf("Port = %q, want 9090", cfg.Port)
		}
		if cfg.SessionSecret != "super-secret" {
			t.Errorf("SessionSecret = %q, want super-secret", cfg.SessionSecret)
		}
		if cfg.BackupPath != "/tmp/backups" {
			t.Errorf("BackupPath = %q, want /tmp/backups", cfg.BackupPath)
		}
	})
}
