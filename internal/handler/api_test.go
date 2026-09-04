package handler

import (
	"archive/zip"
	"bytes"
	"context"
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
	"convert2text/internal/vision"
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

type mockVisionHandlerAnalyzer struct{}

func (m *mockVisionHandlerAnalyzer) AnalyzeImage(ctx context.Context, data []byte) (*vision.AnalysisResult, error) {
	return &vision.AnalysisResult{
		Tags:          []string{"system architecture", "cloud"},
		Objects:       []string{"server"},
		ExtractedText: []string{"Load Balancer -> Microservices"},
		Summary:       "Architecture diagram",
	}, nil
}

func (m *mockVisionHandlerAnalyzer) BatchAnalyzeImages(ctx context.Context, imagesData map[string][]byte) map[string]*vision.AnalysisResult {
	results := make(map[string]*vision.AnalysisResult)
	for id := range imagesData {
		results[id] = &vision.AnalysisResult{
			Tags:          []string{"system architecture", "cloud"},
			Objects:       []string{"server"},
			ExtractedText: []string{"Load Balancer -> Microservices"},
			Summary:       "Architecture diagram",
		}
	}
	return results
}

func (m *mockVisionHandlerAnalyzer) IsEnabled() bool {
	return true
}

func TestExtractJSONWithAIVision(t *testing.T) {
	cfg := &config.Config{
		Port:                     "8080",
		MaxUploadSizeBytes:       10 * 1024 * 1024,
		MaxDecompressedSizeBytes: 50 * 1024 * 1024,
		MaxConcurrentExtractions: 4,
		ExtractionTimeout:        10 * time.Second,
		EnableAIVision:           true,
	}

	extractHandler := NewExtractHandler(cfg)
	extractHandler.SetVisionAnalyzer(&mockVisionHandlerAnalyzer{})

	// Create docx with image
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)
	docXML := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Architecture Doc</w:t></w:r></w:p>
    <w:p><w:r><w:drawing><wp:inline><wp:docPr id="1" name="Diag" descr="Cloud Diagram"/><a:graphic><a:graphicData><a:blip r:embed="rId1" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/></a:graphicData></a:graphic></wp:inline></w:drawing></w:r></w:p>
  </w:body>
</w:document>`
	relsXML := `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`

	wDoc, _ := zw.Create("word/document.xml")
	_, _ = wDoc.Write([]byte(docXML))
	wRels, _ := zw.Create("word/_rels/document.xml.rels")
	_, _ = wRels.Write([]byte(relsXML))
	wImg, _ := zw.Create("word/media/image1.png")
	dummyData := make([]byte, 2048)
	copy(dummyData, []byte("\x89PNG\r\n\x1a\n"))
	_, _ = wImg.Write(dummyData)
	zw.Close()

	body := new(bytes.Buffer)
	writer := multipart.NewWriter(body)
	part, _ := writer.CreateFormFile("file", "arch.docx")
	_, _ = part.Write(buf.Bytes())
	_ = writer.WriteField("format", "markdown")
	_ = writer.WriteField("ai_vision", "true")
	writer.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/extract", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	w := httptest.NewRecorder()

	extractHandler.HandleExtractJSON(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("Expected 200 OK, got %d. Body: %s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool `json:"success"`
		Data    struct {
			Content  string `json:"content"`
			Images   []struct {
				Filename       string                 `json:"filename"`
				VisionAnalysis *vision.AnalysisResult `json:"vision_analysis"`
			} `json:"images"`
		} `json:"data"`
	}

	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatalf("Failed to parse JSON response: %v", err)
	}

	if len(resp.Data.Images) == 0 {
		t.Fatalf("Expected at least 1 image, got 0")
	}

	if resp.Data.Images[0].VisionAnalysis == nil {
		t.Errorf("Expected VisionAnalysis in image JSON, got nil")
	}

	if !strings.Contains(resp.Data.Content, "Load Balancer -> Microservices") {
		t.Errorf("Expected OCR text in Markdown content, got: %s", resp.Data.Content)
	}
}

