package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type Config struct {
	HTTPHost          string
	HTTPPort          int
	WebOrigin         string
	DatabasePath      string
	SourceStoragePath string
	TempStoragePath   string
	ExportStoragePath string
	MaxUploadSize     int64
	MaxEntrySize      int
	ImportBatchSize   int
	LogLevel          string
}

func LoadConfig() (Config, error) {
	port, err := envInt("HTTP_PORT", 8080)
	if err != nil {
		return Config{}, err
	}
	maxUpload, err := envBytes("MAX_UPLOAD_SIZE", 20<<30)
	if err != nil {
		return Config{}, err
	}
	maxEntry, err := envBytes("MAX_ENTRY_SIZE", 4<<20)
	if err != nil {
		return Config{}, err
	}
	batch, err := envInt("IMPORT_BATCH_SIZE", 2000)
	if err != nil {
		return Config{}, err
	}

	return Config{
		HTTPHost:          env("HTTP_HOST", "0.0.0.0"),
		HTTPPort:          port,
		WebOrigin:         env("WEB_ORIGIN", "http://localhost:5173"),
		DatabasePath:      filepath.Clean(env("DATABASE_PATH", "./storage/loglens.db")),
		SourceStoragePath: filepath.Clean(env("SOURCE_STORAGE_PATH", "./storage/sources")),
		TempStoragePath:   filepath.Clean(env("TEMP_STORAGE_PATH", "./storage/temp")),
		ExportStoragePath: filepath.Clean(env("EXPORT_STORAGE_PATH", "./storage/exports")),
		MaxUploadSize:     maxUpload,
		MaxEntrySize:      int(maxEntry),
		ImportBatchSize:   batch,
		LogLevel:          strings.ToLower(env("LOG_LEVEL", "info")),
	}, nil
}

func (c Config) Address() string {
	return fmt.Sprintf("%s:%d", c.HTTPHost, c.HTTPPort)
}

func env(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func envInt(key string, fallback int) (int, error) {
	raw := env(key, strconv.Itoa(fallback))
	value, err := strconv.Atoi(raw)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return value, nil
}

func envBytes(key string, fallback int64) (int64, error) {
	raw := strings.ToUpper(strings.TrimSpace(env(key, strconv.FormatInt(fallback, 10))))
	multipliers := map[string]int64{"KB": 1 << 10, "MB": 1 << 20, "GB": 1 << 30}
	for suffix, multiplier := range multipliers {
		if strings.HasSuffix(raw, suffix) {
			number := strings.TrimSpace(strings.TrimSuffix(raw, suffix))
			value, err := strconv.ParseFloat(number, 64)
			if err != nil || value <= 0 {
				return 0, fmt.Errorf("%s has an invalid size", key)
			}
			return int64(value * float64(multiplier)), nil
		}
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value <= 0 {
		return 0, fmt.Errorf("%s has an invalid size", key)
	}
	return value, nil
}
