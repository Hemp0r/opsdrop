package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

// Config holds runtime configuration for the server.
type Config struct {
	Address             string
	TLSEnabled          bool
	TLSCertPath         string
	TLSKeyPath          string
	DatabasePath        string
	StorageDir          string
	JWTSigningKey       []byte
	RegistrationEnabled bool
	MaxUploadSizeBytes  int64
}

// Load populates Config from environment variables with reasonable defaults.
func Load() (Config, error) {
	tlsEnabled := getEnv("SERVER_TLS_ENABLED", "true") == "true"

	defaultAddr := ":8443"
	if !tlsEnabled {
		defaultAddr = ":8080"
	}

	cfg := Config{
		Address:      getEnv("SERVER_ADDRESS", defaultAddr),
		TLSEnabled:   tlsEnabled,
		TLSCertPath:  getEnv("SERVER_TLS_CERT", "certs/server.crt"),
		TLSKeyPath:   getEnv("SERVER_TLS_KEY", "certs/server.key"),
		DatabasePath: getEnv("SERVER_DATABASE", "data/server.db"),
		StorageDir:   getEnv("SERVER_STORAGE_DIR", "data/storage"),
	}

	cfg.RegistrationEnabled = getEnv("REGISTRATION_ENABLED", "true") == "true"

	if v := os.Getenv("MAX_UPLOAD_SIZE_BYTES"); v != "" {
		parsed, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Config{}, fmt.Errorf("invalid MAX_UPLOAD_SIZE_BYTES: %w", err)
		}
		cfg.MaxUploadSizeBytes = parsed
	}

	jwtKey := os.Getenv("SERVER_JWT_SECRET")
	if jwtKey == "" {
		return Config{}, errors.New("SERVER_JWT_SECRET must be set")
	}
	if len(jwtKey) < 32 {
		return Config{}, errors.New("SERVER_JWT_SECRET must be at least 32 characters")
	}
	cfg.JWTSigningKey = []byte(jwtKey)

	if err := ensureDir(filepath.Dir(cfg.DatabasePath)); err != nil {
		return Config{}, fmt.Errorf("prepare database dir: %w", err)
	}
	if err := ensureDir(cfg.StorageDir); err != nil {
		return Config{}, fmt.Errorf("prepare storage dir: %w", err)
	}

	return cfg, nil
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func ensureDir(path string) error {
	if path == "" {
		return nil
	}
	return os.MkdirAll(path, 0o755)
}
