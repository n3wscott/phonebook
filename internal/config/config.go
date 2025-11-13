package config

import (
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path"
	"strings"
	"time"
)

// Config captures all runtime configuration.
type Config struct {
	Dir      string
	Addr     string
	BasePath string
	TLSCert  string
	TLSKey   string
	LogLevel string
	Debounce time.Duration
}

const (
	defaultAddr     = ":8080"
	defaultBasePath = "/"
	defaultLogLevel = "info"
	defaultDebounce = 250 * time.Millisecond
)

// Load parses CLI flags and environment variables to produce a Config.
func Load(args []string) (Config, error) {
	cfg := Config{Debounce: defaultDebounce}

	fs := flag.NewFlagSet("phonebookd", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)

	dirEnv := getenv("PHONEBOOK_DIR", "")
	addrEnv := getenv("PHONEBOOK_ADDR", defaultAddr)
	baseEnv := getenv("PHONEBOOK_BASE_PATH", defaultBasePath)
	tlsCertEnv := getenv("PHONEBOOK_TLS_CERT", "")
	tlsKeyEnv := getenv("PHONEBOOK_TLS_KEY", "")
	logLevelEnv := getenv("PHONEBOOK_LOG_LEVEL", defaultLogLevel)

	fs.StringVar(&cfg.Dir, "dir", dirEnv, "Root directory containing YAML files (required).")
	fs.StringVar(&cfg.Dir, "d", dirEnv, "Root directory containing YAML files (required).")
	fs.StringVar(&cfg.Addr, "addr", addrEnv, "HTTP listen address (default :8080).")
	fs.StringVar(&cfg.BasePath, "base-path", baseEnv, "Base HTTP path prefix (default /).")
	fs.StringVar(&cfg.TLSCert, "tls-cert", tlsCertEnv, "Path to TLS certificate (optional).")
	fs.StringVar(&cfg.TLSKey, "tls-key", tlsKeyEnv, "Path to TLS private key (optional).")
	fs.StringVar(&cfg.LogLevel, "log-level", logLevelEnv, "Log level: debug, info, error (default info).")

	if err := fs.Parse(args); err != nil {
		return Config{}, err
	}

	if cfg.Dir == "" {
		return Config{}, errors.New("--dir (or PHONEBOOK_DIR) is required")
	}

	cfg.BasePath = sanitizeBasePath(cfg.BasePath)
	cfg.LogLevel = strings.ToLower(cfg.LogLevel)
	if cfg.LogLevel == "" {
		cfg.LogLevel = defaultLogLevel
	}
	if _, err := toSlogLevel(cfg.LogLevel); err != nil {
		return Config{}, err
	}

	if (cfg.TLSCert == "") != (cfg.TLSKey == "") {
		return Config{}, errors.New("both --tls-cert and --tls-key must be provided together")
	}

	return cfg, nil
}

func sanitizeBasePath(p string) string {
	if p == "" {
		p = defaultBasePath
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	// path.Clean removes trailing slash; preserve when not root
	cleaned := path.Clean(p)
	if cleaned != "/" && strings.HasSuffix(p, "/") {
		cleaned += "/"
	}
	if cleaned == "" {
		cleaned = "/"
	}
	// ensure trailing slash for non-root to make joining easier
	if cleaned != "/" && !strings.HasSuffix(cleaned, "/") {
		cleaned += "/"
	}
	return cleaned
}

func getenv(key, fallback string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return fallback
}

// ToSlogLevel converts string log levels into slog levels.
func ToSlogLevel(level string) (slog.Level, error) {
	return toSlogLevel(strings.ToLower(level))
}

func toSlogLevel(level string) (slog.Level, error) {
	switch strings.ToLower(level) {
	case "debug":
		return slog.LevelDebug, nil
	case "info":
		return slog.LevelInfo, nil
	case "error":
		return slog.LevelError, nil
	default:
		return 0, fmt.Errorf("invalid log level %q", level)
	}
}
