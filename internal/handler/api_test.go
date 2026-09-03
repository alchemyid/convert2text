package handler

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"convert2text/internal/assets"
	"convert2text/internal/config"
	"convert2text/internal/middleware"
)

func setupTestServer() (http.Handler, *config.Config) {
	cfg := &config.Config{
		Port:                     "8080",
		MaxUploadSizeBytes:       10 * 1024 * 1024,
		MaxDecompressedSizeBytes: 50 * 1024 * 1024,
		MaxConcurrentExtractions: 4,
		ExtractionTimeout:        10 * time.Second,
	}

	limiter := middleware.NewConcurrencyLimiter(cfg.MaxConcurrentExtractions)
	extractHandler := NewExtractHandler(cfg)

	mux := http.NewServeMux()
	mux.Handle("POST /api/v1/extract", limiter.Limit(2*time.Second)(http.HandlerFunc(extractHandler.HandleExtractJSON)))
	mux.Handle("POST /api/v1/extract/raw", limiter.Limit(2*time.Second)(http.HandlerFunc(extractHandler.HandleExtractRaw)))
	mux.Handle("POST /api/v1/extract/bundle", limiter.Limit(2*time.Second)(http.HandlerFunc(extractHandler.HandleExtractBundle)))
	mux.Handle("GET /api/v1/health", HealthHandler(limiter))
	mux.HandleFunc("GET /api/v1/assets/", extractHandler.HandleGetAsset)

	return middleware.SecurityMiddleware(cfg.MaxUploadSizeBytes)(mux), cfg
}

func createTestDocxBytes() []byte {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	docXML := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>API Test Document</w:t></w:r></w:p>
    <w:p><w:r><w:t>Successfully extracted via REST API.</w:t></w:r></w:p>
  </w:body>
</w:document>`
	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte(docXML))
	zw.Close()
	return buf.Bytes()
}

func TestHealthEndpoint(t *testing.T) {
	handler, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/api/v1/health", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected status 200, got %d", w.Code)
	}

	var resp StandardResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to decode JSON: %v", err)
	}

	if !resp.Success {
		t.Errorf("Expected success true, got %v", resp.Success)
	}
}

func TestExtractJSONMultipart(t *testing.T) {
	handler, _ := setupTestServer()

	docxData := createTestDocxBytes()

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	_ = writer.WriteField("format", "markdown")
	part, err := writer.CreateFormFile("file", "test.docx")
	if err != nil {
		t.Fatalf("Failed to create form file: %v", err)
	}
	_, _ = part.Write(docxData)
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/extract", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp StandardResponse
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	if !resp.Success {
		t.Errorf("Expected success true, got %v", resp.Success)
	}

	dataMap, ok := resp.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("Expected data map, got %T", resp.Data)
	}

	content, _ := dataMap["content"].(string)
	if !strings.Contains(content, "# API Test Document") {
		t.Errorf("Expected extracted content to contain '# API Test Document', got %s", content)
	}
}

func TestExtractRawBinaryStream(t *testing.T) {
	handler, _ := setupTestServer()

	docxData := createTestDocxBytes()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/extract/raw?format=markdown&filename=direct.docx", bytes.NewReader(docxData))
	req.Header.Set("Content-Type", "application/octet-stream")
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	contentType := w.Header().Get("Content-Type")
	if !strings.HasPrefix(contentType, "text/markdown") {
		t.Errorf("Expected Content-Type text/markdown, got %s", contentType)
	}

	rawBody := w.Body.String()
	if !strings.Contains(rawBody, "# API Test Document") {
		t.Errorf("Expected raw body to contain '# API Test Document', got %s", rawBody)
	}
}

func TestExtractUnsupportedFile(t *testing.T) {
	handler, _ := setupTestServer()

	fakeData := []byte("plain text fake file with random bytes")
	req := httptest.NewRequest(http.MethodPost, "/api/v1/extract?filename=fake.unknown", bytes.NewReader(fakeData))
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("Expected 415 Unsupported Media Type, got %d", w.Code)
	}
}

func TestAssetEndpoint(t *testing.T) {
	handler, _ := setupTestServer()

	// Store a test image in the asset store
	store := assets.GetDefaultStore()
	testPayload := []byte("\x89PNG\r\n\x1a\nfakeimagecontent")
	id, _ := store.Save("diagram.png", "image/png", "Test Diagram", "Slide 1", testPayload)

	// Fetch via HTTP GET /api/v1/assets/{id}.png
	req := httptest.NewRequest(http.MethodGet, "/api/v1/assets/"+id+".png", nil)
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d", w.Code)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "image/png" {
		t.Errorf("Expected Content-Type image/png, got %s", contentType)
	}

	if !bytes.Equal(w.Body.Bytes(), testPayload) {
		t.Errorf("Asset body does not match expected payload")
	}
}

func TestExtractBundle(t *testing.T) {
	handler, _ := setupTestServer()

	docxData := createTestDocxBytes()

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile("file", "document.docx")
	if err != nil {
		t.Fatalf("Failed to create form file: %v", err)
	}
	_, _ = part.Write(docxData)
	_ = writer.WriteField("format", "markdown")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/extract/bundle", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	handler.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "application/zip" {
		t.Errorf("Expected Content-Type application/zip, got %s", contentType)
	}

	// Verify the zip can be opened and contains document.md
	zipReader, err := zip.NewReader(bytes.NewReader(w.Body.Bytes()), int64(w.Body.Len()))
	if err != nil {
		t.Fatalf("Failed to parse returned zip archive: %v", err)
	}

	var foundMD bool
	for _, f := range zipReader.File {
		if f.Name == "document.md" {
			foundMD = true
			rc, err := f.Open()
			if err == nil {
				data, _ := io.ReadAll(rc)
				rc.Close()
				if !strings.Contains(string(data), "# API Test Document") {
					t.Errorf("Document.md in zip missing expected content")
				}
			}
		}
	}

	if !foundMD {
		t.Errorf("Expected document.md inside returned zip bundle")
	}
}
