package config

import (
	"os"
	"runtime"
	"strconv"
	"time"
)

// Config holds runtime configuration for the application.
type Config struct {
	Port                     string
	MaxUploadSizeBytes       int64
	MaxDecompressedSizeBytes int64
	MaxConcurrentExtractions int
	ExtractionTimeout        time.Duration
}

// Load loads configuration from environment variables with safe defaults.
func Load() *Config {
	port := getEnv("PORT", "8080")

	maxUploadMB := getEnvInt("MAX_UPLOAD_SIZE_MB", 32)
	maxDecompressMB := getEnvInt("MAX_DECOMPRESSED_SIZE_MB", 150)

	defaultWorkers := runtime.NumCPU() * 2
	if defaultWorkers < 2 {
		defaultWorkers = 2
	}
	maxConcurrency := getEnvInt("MAX_CONCURRENT_EXTRACTIONS", defaultWorkers)
	timeoutSec := getEnvInt("EXTRACTION_TIMEOUT_SEC", 60)

	return &Config{
		Port:                     port,
		MaxUploadSizeBytes:       int64(maxUploadMB) * 1024 * 1024,
		MaxDecompressedSizeBytes: int64(maxDecompressMB) * 1024 * 1024,
		MaxConcurrentExtractions: maxConcurrency,
		ExtractionTimeout:        time.Duration(timeoutSec) * time.Second,
	}
}

func getEnv(key, fallback string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil && parsed > 0 {
			return parsed
		}
	}
	return fallback
}
