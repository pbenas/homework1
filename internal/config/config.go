// Package config parses and validates server configuration.
package config

import (
	"flag"
	"fmt"
	"net"
	"strconv"
	"strings"
)

const (
	EnvPort          = "OBJECT_STORE_PORT"
	EnvBindAddress   = "OBJECT_STORE_BIND_ADDRESS"
	EnvBackend       = "OBJECT_STORE_BACKEND"
	EnvDataDir       = "OBJECT_STORE_DATA_DIR"
	EnvMaxObjectSize = "OBJECT_STORE_MAX_OBJECT_SIZE"

	DefaultMaxObjectSize = int64(1 << 30) // 1 GiB
)

// Backend identifies a storage implementation.
type Backend string

const (
	BackendMemory Backend = "memory"
	BackendDisk   Backend = "disk"
)

// Config contains validated command-line configuration.
type Config struct {
	Port          int
	BindAddress   string
	Backend       Backend
	DataDir       string
	MaxObjectSize int64
}

// Load resolves environment variables and command-line arguments. Flags take
// precedence over environment variables.
func Load(args []string, getenv func(string) string) (Config, error) {
	port, err := envInt(getenv, EnvPort, 8080)
	if err != nil {
		return Config{}, err
	}
	maxObjectSize, err := envInt64(getenv, EnvMaxObjectSize, DefaultMaxObjectSize)
	if err != nil {
		return Config{}, err
	}
	backend := envString(getenv, EnvBackend, string(BackendMemory))
	cfg := Config{
		Port:          port,
		BindAddress:   envString(getenv, EnvBindAddress, "127.0.0.1"),
		Backend:       Backend(backend),
		DataDir:       envString(getenv, EnvDataDir, "./data"),
		MaxObjectSize: maxObjectSize,
	}

	flags := flag.NewFlagSet("object-server", flag.ContinueOnError)
	backend = string(cfg.Backend)
	flags.IntVar(&cfg.Port, "port", cfg.Port, "HTTP listening port (env: "+EnvPort+")")
	flags.StringVar(&cfg.BindAddress, "bind-address", cfg.BindAddress, "HTTP bind IP address (env: "+EnvBindAddress+")")
	flags.StringVar(&backend, "backend", backend, "storage backend: memory or disk (env: "+EnvBackend+")")
	flags.StringVar(&cfg.DataDir, "data-dir", cfg.DataDir, "disk backend directory (env: "+EnvDataDir+")")
	flags.Int64Var(&cfg.MaxObjectSize, "max-object-size", cfg.MaxObjectSize, "maximum object size in bytes (env: "+EnvMaxObjectSize+")")
	if err := flags.Parse(args); err != nil {
		return Config{}, err
	}
	if flags.NArg() != 0 {
		return Config{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(flags.Args(), " "))
	}
	cfg.Backend = Backend(backend)
	return cfg, cfg.validate()
}

func (cfg *Config) validate() error {
	cfg.BindAddress = strings.TrimSpace(cfg.BindAddress)
	cfg.Backend = Backend(strings.ToLower(strings.TrimSpace(string(cfg.Backend))))
	if cfg.Port < 1 || cfg.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}
	if net.ParseIP(cfg.BindAddress) == nil {
		return fmt.Errorf("bind-address must be a valid IP address")
	}
	if cfg.MaxObjectSize < 1 {
		return fmt.Errorf("max-object-size must be positive")
	}
	if cfg.Backend != BackendMemory && cfg.Backend != BackendDisk {
		return fmt.Errorf("backend must be memory or disk")
	}
	if cfg.Backend == BackendDisk && strings.TrimSpace(cfg.DataDir) == "" {
		return fmt.Errorf("data-dir cannot be empty for the disk backend")
	}
	return nil
}

func envInt(getenv func(string) string, key string, fallback int) (int, error) {
	value := strings.TrimSpace(getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}

func envString(getenv func(string) string, key, fallback string) string {
	if value := getenv(key); value != "" {
		return value
	}
	return fallback
}

func envInt64(getenv func(string) string, key string, fallback int64) (int64, error) {
	value := strings.TrimSpace(getenv(key))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("%s must be an integer: %w", key, err)
	}
	return parsed, nil
}
