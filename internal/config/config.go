package config

import (
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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
	DefaultPrivateTTL   time.Duration
	MaxPrivateTTL       time.Duration
	DefaultPublicTTL    time.Duration
	MaxPublicTTL        time.Duration
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

	// Parse expiration settings
	var err error
	cfg.DefaultPrivateTTL, err = parseDuration(getEnv("DEFAULT_PRIVATE_TTL", "14d"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid DEFAULT_PRIVATE_TTL: %w", err)
	}

	cfg.MaxPrivateTTL, err = parseDuration(getEnv("MAX_PRIVATE_TTL", "14d"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid MAX_PRIVATE_TTL: %w", err)
	}

	cfg.DefaultPublicTTL, err = parseDuration(getEnv("DEFAULT_PUBLIC_TTL", "48h"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid DEFAULT_PUBLIC_TTL: %w", err)
	}

	cfg.MaxPublicTTL, err = parseDuration(getEnv("MAX_PUBLIC_TTL", "48h"))
	if err != nil {
		return Config{}, fmt.Errorf("invalid MAX_PUBLIC_TTL: %w", err)
	}

	// Validate TTL constraints
	if cfg.DefaultPrivateTTL > cfg.MaxPrivateTTL {
		return Config{}, errors.New("DEFAULT_PRIVATE_TTL cannot exceed MAX_PRIVATE_TTL")
	}
	if cfg.DefaultPublicTTL > cfg.MaxPublicTTL {
		return Config{}, errors.New("DEFAULT_PUBLIC_TTL cannot exceed MAX_PUBLIC_TTL")
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

// parseDuration parses a duration string with support for hours (h) and days (d).
// Examples: "24h", "7d", "48h", "14d"
func parseDuration(s string) (time.Duration, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, errors.New("empty duration string")
	}

	// Try standard time.ParseDuration first
	if d, err := time.ParseDuration(s); err == nil {
		return d, nil
	}

	// Handle day suffix (e.g., "7d", "14d")
	if strings.HasSuffix(s, "d") {
		daysStr := strings.TrimSuffix(s, "d")
		days, err := strconv.ParseInt(daysStr, 10, 64)
		if err != nil {
			return 0, fmt.Errorf("invalid days value: %w", err)
		}
		if days < 0 {
			return 0, errors.New("days cannot be negative")
		}
		return time.Duration(days) * 24 * time.Hour, nil
	}

	return 0, fmt.Errorf("invalid duration format: %s (use formats like '24h', '7d')", s)
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	days := d / (24 * time.Hour)
	if days > 0 && d%(24*time.Hour) == 0 {
		if days == 1 {
			return "1 day"
		}
		return fmt.Sprintf("%d days", days)
	}
	hours := d / time.Hour
	if hours > 0 && d%time.Hour == 0 {
		if hours == 1 {
			return "1 hour"
		}
		return fmt.Sprintf("%d hours", hours)
	}
	return d.String()
}

// LogSummary prints the active configuration (excluding secrets) to the given logger.
func (c Config) LogSummary(l *log.Logger) {
	l.Println("Configuration:")
	l.Printf("  Address:              %s", c.Address)
	l.Printf("  TLS enabled:          %v", c.TLSEnabled)
	if c.TLSEnabled {
		l.Printf("  TLS cert:             %s", c.TLSCertPath)
		l.Printf("  TLS key:              %s", c.TLSKeyPath)
	}
	l.Printf("  Database:             %s", c.DatabasePath)
	l.Printf("  Storage dir:          %s", c.StorageDir)
	l.Printf("  Registration:         %v", c.RegistrationEnabled)
	if c.MaxUploadSizeBytes > 0 {
		l.Printf("  Max upload size:      %d bytes", c.MaxUploadSizeBytes)
	} else {
		l.Printf("  Max upload size:      server default cap (set MAX_UPLOAD_SIZE_BYTES to override)")
	}
	l.Printf("  Default private TTL:  %s", formatDuration(c.DefaultPrivateTTL))
	l.Printf("  Max private TTL:      %s", formatDuration(c.MaxPrivateTTL))
	l.Printf("  Default public TTL:   %s", formatDuration(c.DefaultPublicTTL))
	l.Printf("  Max public TTL:       %s", formatDuration(c.MaxPublicTTL))
}
