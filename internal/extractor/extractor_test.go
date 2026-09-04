package extractor

import (
	"archive/zip"
	"bytes"
	"context"
	"os"
	"strings"
	"testing"

	"convert2text/internal/vision"
	"github.com/xuri/excelize/v2"
)

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"normal", "report.pdf", "report.pdf"},
		{"traversal unix", "../../etc/passwd", "passwd"},
		{"traversal windows", "..\\..\\windows\\system32\\cmd.exe", "cmd.exe"},
		{"special chars", "my file @#$! 2026.docx", "my_file______2026.docx"},
		{"empty", "", "uploaded_file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeFileName(tt.input)
			if got != tt.expected {
				t.Errorf("SanitizeFileName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestFormatMarkdownTable(t *testing.T) {
	headers := []string{"Name", "Role", "Department"}
	rows := [][]string{
		{"Alice", "Engineer", "R&D"},
		{"Bob", "Manager", "Product"},
	}

	res := FormatMarkdownTable(headers, rows)
	if !strings.Contains(res, "| Name | Role | Department |") {
		t.Errorf("Expected header in markdown table, got: %s", res)
	}
	if !strings.Contains(res, "| Alice | Engineer | R&D |") {
		t.Errorf("Expected row in markdown table, got: %s", res)
	}
}

func TestDOCXExtraction(t *testing.T) {
	// Create a minimal in-memory docx zip
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	docXML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p>
      <w:pPr><w:pStyle w:val="Heading1"/></w:pPr>
      <w:r><w:t>Project Title</w:t></w:r>
    </w:p>
    <w:p>
      <w:r><w:t>This is an automated test document for docx extraction.</w:t></w:r>
    </w:p>
    <w:tbl>
      <w:tr>
        <w:tc><w:p><w:r><w:t>Item</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>Price</w:t></w:r></w:p></w:tc>
      </w:tr>
      <w:tr>
        <w:tc><w:p><w:r><w:t>Book</w:t></w:r></w:p></w:tc>
        <w:tc><w:p><w:r><w:t>$10</w:t></w:r></w:p></w:tc>
      </w:tr>
    </w:tbl>
  </w:body>
</w:document>`

	w, err := zw.Create("word/document.xml")
	if err != nil {
		t.Fatalf("Failed to create doc entry: %v", err)
	}
	_, _ = w.Write([]byte(docXML))
	zw.Close()

	reader := bytes.NewReader(buf.Bytes())
	res, err := ExecuteExtraction(context.Background(), reader, int64(buf.Len()), "sample.docx", Options{
		Format: FormatMarkdown,
	})
	if err != nil {
		t.Fatalf("DOCX extraction failed: %v", err)
	}

	if res.DetectedType != TypeDOCX {
		t.Errorf("Expected DetectedType docx, got %s", res.DetectedType)
	}
	if !strings.Contains(res.Content, "# Project Title") {
		t.Errorf("Expected markdown heading # Project Title, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "| Item | Price |") {
		t.Errorf("Expected markdown table in docx output, got: %s", res.Content)
	}
}

func TestPPTXExtraction(t *testing.T) {
	// Create minimal in-memory pptx zip
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	slide1XML := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<p:sld xmlns:p="http://schemas.openxmlformats.org/presentationml/2006/main"
       xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <p:cSld>
    <p:spTree>
      <p:sp>
        <p:txBody>
          <a:p><a:r><a:t>Introduction Slide</a:t></a:r></a:p>
          <a:p><a:r><a:t>Key point 1</a:t></a:r></a:p>
        </p:txBody>
      </p:sp>
    </p:spTree>
  </p:cSld>
</p:sld>`

	w, err := zw.Create("ppt/slides/slide1.xml")
	if err != nil {
		t.Fatalf("Failed to create slide entry: %v", err)
	}
	_, _ = w.Write([]byte(slide1XML))
	zw.Close()

	reader := bytes.NewReader(buf.Bytes())
	res, err := ExecuteExtraction(context.Background(), reader, int64(buf.Len()), "presentation.pptx", Options{
		Format: FormatMarkdown,
	})
	if err != nil {
		t.Fatalf("PPTX extraction failed: %v", err)
	}

	if res.DetectedType != TypePPTX {
		t.Errorf("Expected DetectedType pptx, got %s", res.DetectedType)
	}
	if !strings.Contains(res.Content, "## Slide 1") {
		t.Errorf("Expected Slide 1 header, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "Introduction Slide") {
		t.Errorf("Expected content Introduction Slide, got: %s", res.Content)
	}
}

func TestXLSXExtraction(t *testing.T) {
	// Create in-memory xlsx file using excelize
	f := excelize.NewFile()
	sheetName := "Financials"
	index, _ := f.NewSheet(sheetName)
	f.SetActiveSheet(index)

	f.SetCellValue(sheetName, "A1", "Quarter")
	f.SetCellValue(sheetName, "B1", "Revenue")
	f.SetCellValue(sheetName, "A2", "Q1")
	f.SetCellValue(sheetName, "B2", "10000")
	f.SetCellValue(sheetName, "A3", "Q2")
	f.SetCellValue(sheetName, "B3", "15000")

	buf, err := f.WriteToBuffer()
	if err != nil {
		t.Fatalf("Failed to write xlsx to buffer: %v", err)
	}

	reader := bytes.NewReader(buf.Bytes())
	res, err := ExecuteExtraction(context.Background(), reader, int64(buf.Len()), "financials.xlsx", Options{
		Format: FormatMarkdown,
	})
	if err != nil {
		t.Fatalf("XLSX extraction failed: %v", err)
	}

	if res.DetectedType != TypeXLSX {
		t.Errorf("Expected DetectedType xlsx, got %s", res.DetectedType)
	}
	if !strings.Contains(res.Content, "## Sheet: Financials") {
		t.Errorf("Expected Sheet: Financials, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "| Quarter | Revenue |") {
		t.Errorf("Expected table header in xlsx, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "| Q1 | 10000 |") {
		t.Errorf("Expected data row Q1 in xlsx, got: %s", res.Content)
	}
}

func TestZipBombProtection(t *testing.T) {
	// Create docx with small compressed size but huge uncompressed content
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	hugeText := strings.Repeat("A", 1024*1024) // 1MB repeated
	docXML := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body><w:p><w:r><w:t>` + hugeText + `</w:t></w:r></w:p></w:body>
</w:document>`

	w, _ := zw.Create("word/document.xml")
	_, _ = w.Write([]byte(docXML))
	zw.Close()

	reader := bytes.NewReader(buf.Bytes())
	// Set an artificially small decompression limit of 100KB to trigger the security defense
	opts := Options{
		Format:               FormatMarkdown,
		MaxDecompressedBytes: 100 * 1024,
	}

	_, err := ExecuteExtraction(context.Background(), reader, int64(buf.Len()), "bomb.docx", opts)
	if err != ErrDecompressionLimit {
		t.Fatalf("Expected ErrDecompressionLimit, got %v", err)
	}
}

func TestDevopsToolsPDFExtraction(t *testing.T) {
	pdfPath := "../../devops_tools.pdf"
	f, err := os.Open(pdfPath)
	if err != nil {
		t.Skip("devops_tools.pdf not found, skipping integration test")
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("Failed to stat PDF: %v", err)
	}

	opts := Options{
		Format: FormatMarkdown,
	}

	res, err := ExecuteExtraction(context.Background(), f, fi.Size(), "devops_tools.pdf", opts)
	if err != nil {
		t.Fatalf("PDF extraction failed: %v", err)
	}

	if res.DetectedType != TypePDF {
		t.Errorf("Expected TypePDF, got %v", res.DetectedType)
	}

	// 1. Assert accurate character decoding (ToUnicode fixes '!' -> 'a')
	if !strings.Contains(res.Content, "Generate Helm Repository") {
		t.Errorf("Expected decoded 'Generate Helm Repository', got: %s", res.Content)
	}
	if strings.Contains(res.Content, "Gener!te") {
		t.Errorf("Content still contains corrupted 'Gener!te': %s", res.Content)
	}

	// 2. Assert bullet list extraction
	if !strings.Contains(res.Content, "- Provisioning Env") {
		t.Errorf("Expected bullet '- Provisioning Env', got: %s", res.Content)
	}

	// 3. Assert section headers
	if !strings.Contains(res.Content, "### Feature:") {
		t.Errorf("Expected section header '### Feature:', got: %s", res.Content)
	}

	// 4. Assert Table detection on Page 3
	if !strings.Contains(res.Content, "| Minutes | Capacity | Total VM | Total hour |") {
		t.Errorf("Expected markdown table headers, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "| Engineer | 20 | 1 | 49 | 16.33333333 |") {
		t.Errorf("Expected markdown table row for Engineer, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "| Tools | 20 | 7 | 49 | 2.333333333 |") {
		t.Errorf("Expected markdown table row for Tools, got: %s", res.Content)
	}

	t.Logf("DevopsTools PDF Extracted Images Count: %d", len(res.Images))
	for idx, img := range res.Images {
		t.Logf("  Image %d: ID=%s, Filename=%s, Size=%d, URL=%s, Location=%s", idx, img.ID, img.Filename, img.SizeBytes, img.URL, img.Location)
	}
}

func TestAWSDRSPDFExtraction(t *testing.T) {
	pdfPath := "../../2025 KAK Pengadaan Solusi AWS DRS_SF.pdf"
	if _, err := os.Stat(pdfPath); os.IsNotExist(err) {
		pdfPath = "2025 KAK Pengadaan Solusi AWS DRS_SF.pdf"
	}
	f, err := os.Open(pdfPath)
	if err != nil {
		t.Skipf("Skipping live PDF test, file not found: %v", err)
		return
	}
	defer f.Close()

	fi, err := f.Stat()
	if err != nil {
		t.Fatalf("Failed to stat PDF: %v", err)
	}

	opts := Options{
		Format:        FormatMarkdown,
		ExtractImages: true,
	}

	res, err := ExecuteExtraction(context.Background(), f, fi.Size(), fi.Name(), opts)
	if err != nil {
		t.Fatalf("Failed to extract AWS DRS PDF: %v", err)
	}

	if res.WordCount == 0 {
		t.Errorf("Expected word count > 0, got 0")
	}

	if len(res.Images) == 0 {
		t.Errorf("Expected extracted images from AWS DRS PDF, got 0")
	}

	// Verify that text is cleanly extracted in proper reading order, not scrambled into anagrams
	expectedPhrases := []string{
		"Kerangka Acuan Kerja",
		"PT ANTAM Tbk",
		"Daftar Isi",
		"Pendahuluan",
		"Latar Belakang",
		"Maksud dan Tujuan",
		"Lingkup Pekerjaan",
	}
	for _, phrase := range expectedPhrases {
		if !strings.Contains(res.Content, phrase) {
			t.Errorf("Expected clean text to contain %q, but was not found", phrase)
		}
	}

	// Ensure scrambled words from previous bug do not exist
	scrambledPhrases := []string{
		"Kreangka cAuan ejaKr",
		"nPuaendahul",
		"aDaftrIi",
	}
	for _, scrambled := range scrambledPhrases {
		if strings.Contains(res.Content, scrambled) {
			t.Errorf("Found corrupted scrambled text %q in output", scrambled)
		}
	}

	t.Logf("AWS DRS PDF Extracted successfully: %d words, %d images", res.WordCount, len(res.Images))
}

func TestVisionCandidateFilter(t *testing.T) {
	tests := []struct {
		name     string
		img      ExtractedImage
		expected bool
	}{
		{
			name: "page 1 logo",
			img: ExtractedImage{
				Location:  "Page 1",
				SizeBytes: 50000,
				Width:     553,
				Height:    107,
			},
			expected: false,
		},
		{
			name: "slide 1 title logo",
			img: ExtractedImage{
				Location:  "Slide 1",
				SizeBytes: 120000,
				Width:     400,
				Height:    300,
			},
			expected: false,
		},
		{
			name: "tiny bullet icon",
			img: ExtractedImage{
				Location:  "Page 3",
				SizeBytes: 800,
			},
			expected: false,
		},
		{
			name: "banner strip extreme aspect ratio",
			img: ExtractedImage{
				Location:  "Page 2",
				SizeBytes: 85000,
				Width:     2549,
				Height:    367,
			},
			expected: false,
		},
		{
			name: "small icon stamp",
			img: ExtractedImage{
				Location:  "Page 3",
				SizeBytes: 8000,
				Width:     100,
				Height:    80,
			},
			expected: false,
		},
		{
			name: "legitimate solution architecture diagram",
			img: ExtractedImage{
				Location:  "Page 2",
				SizeBytes: 141633,
				Width:     954,
				Height:    1268,
			},
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := IsVisionCandidate(tt.img)
			if got != tt.expected {
				t.Errorf("IsVisionCandidate(%s) = %v, want %v", tt.name, got, tt.expected)
			}
		})
	}
}


func TestDOCXWithEmbeddedImage(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	// Document XML with drawing blip
	docXML := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Architecture Overview</w:t></w:r></w:p>
    <w:p>
      <w:r>
        <w:drawing>
          <wp:inline>
            <wp:docPr id="1" name="System Architecture" descr="High-Availability Kubernetes Cluster Diagram"/>
            <a:graphic>
              <a:graphicData>
                <a:blip r:embed="rId1" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/>
              </a:graphicData>
            </a:graphic>
          </wp:inline>
        </w:drawing>
      </w:r>
    </w:p>
  </w:body>
</w:document>`

	// Relationships XML
	relsXML := `<?xml version="1.0" encoding="UTF-8"?>
<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">
  <Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/image" Target="media/image1.png"/>
</Relationships>`

	wDoc, _ := zw.Create("word/document.xml")
	_, _ = wDoc.Write([]byte(docXML))

	wRels, _ := zw.Create("word/_rels/document.xml.rels")
	_, _ = wRels.Write([]byte(relsXML))

	// Fake image binary payload
	wImg, _ := zw.Create("word/media/image1.png")
	_, _ = wImg.Write([]byte("\x89PNG\r\n\x1a\n\x00\x00\x00\rIHDRfakeimagebytes"))
	zw.Close()

	reader := bytes.NewReader(buf.Bytes())
	res, err := ExecuteExtraction(context.Background(), reader, int64(buf.Len()), "architecture.docx", Options{
		Format: FormatMarkdown,
	})
	if err != nil {
		t.Fatalf("Extraction failed: %v", err)
	}

	if len(res.Images) == 0 {
		t.Fatalf("Expected at least 1 extracted image, got 0")
	}

	img := res.Images[0]
	if img.Filename != "image1.png" {
		t.Errorf("Expected filename image1.png, got %s", img.Filename)
	}
	if img.AltText != "High-Availability Kubernetes Cluster Diagram" {
		t.Errorf("Expected alt text to be preserved, got %s", img.AltText)
	}
	if !strings.Contains(res.Content, "[IMAGE: System Architecture") {
		t.Errorf("Expected semantic image placeholder in content, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "High-Availability Kubernetes Cluster Diagram") {
		t.Errorf("Expected alt text in content placeholder, got: %s", res.Content)
	}
}

type mockVisionAnalyzer struct {
	analysis *vision.AnalysisResult
}

func (m *mockVisionAnalyzer) AnalyzeImage(ctx context.Context, data []byte) (*vision.AnalysisResult, error) {
	return m.analysis, nil
}

func (m *mockVisionAnalyzer) BatchAnalyzeImages(ctx context.Context, imagesData map[string][]byte) map[string]*vision.AnalysisResult {
	results := make(map[string]*vision.AnalysisResult)
	for id := range imagesData {
		results[id] = m.analysis
	}
	return results
}

func (m *mockVisionAnalyzer) IsEnabled() bool {
	return true
}

func TestDOCXWithVisionEnrichment(t *testing.T) {
	buf := new(bytes.Buffer)
	zw := zip.NewWriter(buf)

	docXML := `<?xml version="1.0" encoding="UTF-8"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"
            xmlns:wp="http://schemas.openxmlformats.org/drawingml/2006/wordprocessingDrawing"
            xmlns:a="http://schemas.openxmlformats.org/drawingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>Architecture Section</w:t></w:r></w:p>
    <w:p>
      <w:r>
        <w:drawing>
          <wp:inline>
            <wp:docPr id="1" name="System Architecture" descr="Solution Diagram"/>
            <a:graphic>
              <a:graphicData>
                <a:blip r:embed="rId1" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"/>
              </a:graphicData>
            </a:graphic>
          </wp:inline>
        </w:drawing>
      </w:r>
    </w:p>
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
	// Write dummy image data larger than 1500 bytes so it passes the size filter
	dummyData := make([]byte, 2048)
	copy(dummyData, []byte("\x89PNG\r\n\x1a\n"))
	_, _ = wImg.Write(dummyData)
	zw.Close()

	mockAnalyzer := &mockVisionAnalyzer{
		analysis: &vision.AnalysisResult{
			Tags:          []string{"cloud architecture", "kubernetes", "database"},
			Objects:       []string{"server", "database"},
			ExtractedText: []string{"Ingress Controller", "API Gateway -> DB"},
			Summary:       "Architecture diagram with 2 services",
		},
	}

	reader := bytes.NewReader(buf.Bytes())
	res, err := ExecuteExtraction(context.Background(), reader, int64(buf.Len()), "architecture.docx", Options{
		Format:         FormatMarkdown,
		EnableVision:   true,
		VisionAnalyzer: mockAnalyzer,
	})
	if err != nil {
		t.Fatalf("Extraction failed: %v", err)
	}

	if len(res.Images) == 0 {
		t.Fatalf("Expected 1 image, got 0")
	}

	img := res.Images[0]
	if img.VisionAnalysis == nil {
		t.Fatalf("Expected VisionAnalysis to be populated on image, got nil")
	}
	if len(img.VisionAnalysis.Tags) != 3 {
		t.Errorf("Expected 3 tags, got %d", len(img.VisionAnalysis.Tags))
	}

	// Verify enriched markdown content
	if !strings.Contains(res.Content, "AI Vision Analysis (Solutioning & Architecture Insights)") {
		t.Errorf("Expected solutioning header in content, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "API Gateway -> DB") {
		t.Errorf("Expected OCR diagram text in markdown content, got: %s", res.Content)
	}
	if !strings.Contains(res.Content, "cloud architecture, kubernetes, database") {
		t.Errorf("Expected tags in markdown content, got: %s", res.Content)
	}
}

