package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"convert2text/internal/config"
	"convert2text/internal/extractor"
	"convert2text/internal/handler"
	"convert2text/internal/middleware"
	"convert2text/internal/vision"
	"convert2text/internal/web"
)

func main() {
	cfg := config.Load()

	// CLI Extraction Mode: ./convert2text <filename> [optional_output.md]
	if len(os.Args) > 1 && !strings.HasPrefix(os.Args[1], "-") {
		filePath := os.Args[1]
		f, err := os.Open(filePath)
		if err != nil {
			log.Fatalf("Failed to open file: %v", err)
		}
		defer f.Close()

		fi, err := f.Stat()
		if err != nil {
			log.Fatalf("Failed to stat file: %v", err)
		}

		var visionAnalyzer vision.Analyzer
		if cfg.EnableAIVision && cfg.AzureVisionEndpoint != "" && cfg.AzureVisionKey != "" {
			visionAnalyzer = vision.NewClient(cfg.AzureVisionEndpoint, cfg.AzureVisionKey, cfg.VisionTimeout, cfg.VisionConcurrency)
		}

		opts := extractor.Options{
			Format:               extractor.FormatMarkdown,
			MaxDecompressedBytes: cfg.MaxDecompressedSizeBytes,
			ExtractImages:        true,
			VisionAnalyzer:       visionAnalyzer,
			EnableVision:         cfg.EnableAIVision,
		}

		res, err := extractor.ExecuteExtraction(context.Background(), f, fi.Size(), fi.Name(), opts)
		if err != nil {
			log.Fatalf("Extraction error: %v", err)
		}

		if len(os.Args) > 2 {
			outPath := os.Args[2]
			if err := os.WriteFile(outPath, []byte(res.Content), 0644); err != nil {
				log.Fatalf("Failed to write output file: %v", err)
			}
			log.Printf("Extraction complete -> %s (%d words, %d images)", outPath, res.WordCount, len(res.Images))
		} else {
			fmt.Print(res.Content)
		}
		return
	}

	// Initialize Concurrency Limiter for Compute Optimization
	limiter := middleware.NewConcurrencyLimiter(cfg.MaxConcurrentExtractions)
	extractHandler := handler.NewExtractHandler(cfg)

	mux := http.NewServeMux()

	// REST API Endpoints with concurrency throttle & extraction queue
	limitedExtractJSON := limiter.Limit(5 * time.Second)(http.HandlerFunc(extractHandler.HandleExtractJSON))
	limitedExtractRaw := limiter.Limit(5 * time.Second)(http.HandlerFunc(extractHandler.HandleExtractRaw))
	limitedExtractBundle := limiter.Limit(5 * time.Second)(http.HandlerFunc(extractHandler.HandleExtractBundle))

	mux.Handle("POST /api/v1/extract", limitedExtractJSON)
	mux.Handle("POST /api/v1/extract/raw", limitedExtractRaw)
	mux.Handle("POST /api/v1/extract/bundle", limitedExtractBundle)
	mux.Handle("GET /api/v1/health", handler.HealthHandler(limiter))
	mux.HandleFunc("GET /api/v1/assets/", extractHandler.HandleGetAsset)
	mux.HandleFunc("GET /assets/", extractHandler.HandleGetAsset)

	// Web UI Frontend (embedded static files)
	mux.Handle("/", web.Handler())

	// Apply Security & Max Upload Size Middleware globally
	rootHandler := middleware.SecurityMiddleware(cfg.MaxUploadSizeBytes)(mux)

	server := &http.Server{
		Addr:              ":" + cfg.Port,
		Handler:           rootHandler,
		ReadHeaderTimeout: 10 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Server runner goroutine
	go func() {
		log.Printf("==========================================================")
		log.Printf(" Convert2Text Server is running at http://localhost:%s", cfg.Port)
		log.Printf(" Max Upload Size      : %d MB", cfg.MaxUploadSizeBytes/(1024*1024))
		log.Printf(" Concurrency Workers  : %d slots", cfg.MaxConcurrentExtractions)
		log.Printf(" Decompression Limit  : %d MB", cfg.MaxDecompressedSizeBytes/(1024*1024))
		if cfg.EnableAIVision {
			log.Printf(" Azure AI Vision      : Enabled (endpoint: %s)", cfg.AzureVisionEndpoint)
		} else {
			log.Printf(" Azure AI Vision      : Disabled")
		}
		log.Printf("==========================================================")

		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Server failed to start: %v", err)
		}
	}()

	// Graceful shutdown listener
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Println("Shutting down server gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server forced to shutdown: %v", err)
	}

	fmt.Println("Server exiting")
}
