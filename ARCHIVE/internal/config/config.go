package config

import (
	"bufio"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Config holds runtime configuration for the application.
type Config struct {
	Port                     string
	MaxUploadSizeBytes       int64
	MaxDecompressedSizeBytes int64
	MaxConcurrentExtractions int
	ExtractionTimeout        time.Duration
	AzureVisionEndpoint      string
	AzureVisionKey           string
	EnableAIVision           bool
	VisionConcurrency        int
	VisionTimeout            time.Duration
}

// Load loads configuration from .env and environment variables with safe defaults.
func Load() *Config {
	loadDotEnv()

	port := getEnv("PORT", "8080")

	maxUploadMB := getEnvInt("MAX_UPLOAD_SIZE_MB", 32)
	maxDecompressMB := getEnvInt("MAX_DECOMPRESSED_SIZE_MB", 150)

	defaultWorkers := runtime.NumCPU() * 2
	if defaultWorkers < 2 {
		defaultWorkers = 2
	}
	maxConcurrency := getEnvInt("MAX_CONCURRENT_EXTRACTIONS", defaultWorkers)
	timeoutSec := getEnvInt("EXTRACTION_TIMEOUT_SEC", 60)

	visionEndpoint := getEnv("AZURE_VISION_ENDPOINT", "")
	visionKey := getEnv("AZURE_VISION_KEY", "")
	enableVision := getEnvBool("ENABLE_AI_VISION", false) && visionEndpoint != "" && visionKey != ""
	visionConcurrency := getEnvInt("VISION_CONCURRENCY", 4)
	visionTimeoutSec := getEnvInt("VISION_TIMEOUT_SEC", 15)

	return &Config{
		Port:                     port,
		MaxUploadSizeBytes:       int64(maxUploadMB) * 1024 * 1024,
		MaxDecompressedSizeBytes: int64(maxDecompressMB) * 1024 * 1024,
		MaxConcurrentExtractions: maxConcurrency,
		ExtractionTimeout:        time.Duration(timeoutSec) * time.Second,
		AzureVisionEndpoint:      visionEndpoint,
		AzureVisionKey:           visionKey,
		EnableAIVision:           enableVision,
		VisionConcurrency:        visionConcurrency,
		VisionTimeout:            time.Duration(visionTimeoutSec) * time.Second,
	}
}

// loadDotEnv discovers and loads key-value pairs from .env into the process environment.
func loadDotEnv() {
	candidates := []string{".env", "../.env", "../../.env"}
	for _, path := range candidates {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		scanner := bufio.NewScanner(strings.NewReader(string(data)))
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				val := strings.TrimSpace(parts[1])
				if (strings.HasPrefix(val, `"`) && strings.HasSuffix(val, `"`)) ||
					(strings.HasPrefix(val, `'`) && strings.HasSuffix(val, `'`)) {
					val = val[1 : len(val)-1]
				}
				if os.Getenv(key) == "" {
					_ = os.Setenv(key, val)
				}
			}
		}
		break
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

func getEnvBool(key string, fallback bool) bool {
	if val := os.Getenv(key); val != "" {
		lower := strings.ToLower(strings.TrimSpace(val))
		return lower == "true" || lower == "1" || lower == "yes" || lower == "on"
	}
	return fallback
}

