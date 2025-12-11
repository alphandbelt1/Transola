package config

import (
	"errors"
	"fmt"
	"os"
)

// Config holds runtime configuration sourced from environment variables.
type Config struct {
	HTTPAddr      string
	DataRoot      string
	DBPath        string
	JWTSecret     string
	AdminEmail    string
	AdminPassword string
}

// Load reads configuration with sensible defaults for local development.
func Load() Config {
	return Config{
		HTTPAddr:      getEnv("TRANSOLA_HTTP_ADDR", ":8080"),
		DataRoot:      getEnv("TRANSOLA_DATA_ROOT", "./data"),
		DBPath:        getEnv("TRANSOLA_DB_PATH", "./transola.db"),
		JWTSecret:     getEnv("TRANSOLA_JWT_SECRET", "dev-secret-change-me"),
		AdminEmail:    getEnv("TRANSOLA_ADMIN_EMAIL", "admin@example.com"),
		AdminPassword: getEnv("TRANSOLA_ADMIN_PASSWORD", "admin123"),
	}
}

// Validate returns an error if required configuration is missing.
func (c Config) Validate() error {
	if c.HTTPAddr == "" {
		return errors.New("TRANSOLA_HTTP_ADDR is required")
	}
	if c.DataRoot == "" {
		return errors.New("TRANSOLA_DATA_ROOT is required")
	}
	if c.JWTSecret == "" {
		return fmt.Errorf("TRANSOLA_JWT_SECRET is required; set a strong value for production")
	}
	if c.AdminEmail == "" || c.AdminPassword == "" {
		return fmt.Errorf("TRANSOLA_ADMIN_EMAIL and TRANSOLA_ADMIN_PASSWORD are required for initial seeding")
	}
	return nil
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
