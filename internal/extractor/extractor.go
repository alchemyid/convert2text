package extractor

import (
	"archive/zip"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"
)

// OutputFormat defines the requested output format.
type OutputFormat string

const (
	FormatMarkdown OutputFormat = "markdown"
	FormatText     OutputFormat = "text"
)

// SupportedFileType represents detected file format.
type SupportedFileType string

const (
	TypePDF     SupportedFileType = "pdf"
	TypeDOCX    SupportedFileType = "docx"
	TypePPTX    SupportedFileType = "pptx"
	TypeXLSX    SupportedFileType = "xlsx"
	TypeXLS     SupportedFileType = "xls"
	TypeUnknown SupportedFileType = "unknown"
)

var (
	ErrUnsupportedFileType = errors.New("unsupported file type: only PDF, DOCX, PPTX, XLSX/XLS are supported")
	ErrCorruptedFile       = errors.New("corrupted or invalid file format")
	ErrDecompressionLimit  = errors.New("decompression limit exceeded (potential zip bomb detected)")
	ErrEmptyFile           = errors.New("file is empty")
)

// ExtractedImage represents an image found inside a document.
type ExtractedImage struct {
	ID           string `json:"id"`
	Filename     string `json:"filename"`
	ContentType  string `json:"content_type"`
	SizeBytes    int64  `json:"size_bytes"`
	AltText      string `json:"alt_text,omitempty"`
	Location     string `json:"location,omitempty"`
	RelativePath string `json:"relative_path,omitempty"`
	URL          string `json:"url,omitempty"`
	Data         []byte `json:"-"`
}

// Options specifies extraction configuration.
type Options struct {
	Format               OutputFormat
	MaxDecompressedBytes int64
	ExtractImages        bool
}

// Result holds the extraction output and metadata.
type Result struct {
	Filename     string                 `json:"filename"`
	DetectedType SupportedFileType      `json:"detected_type"`
	OutputFormat OutputFormat           `json:"output_format"`
	Content      string                 `json:"content"`
	WordCount    int                    `json:"word_count"`
	DurationMs   int64                  `json:"duration_ms"`
	Images       []ExtractedImage       `json:"images,omitempty"`
	Metadata     map[string]interface{} `json:"metadata,omitempty"`
}

// FormatImageMarkdown creates a structured, AI-agent friendly image reference in Markdown.
func FormatImageMarkdown(name, altText, url, location string) string {
	var sb strings.Builder
	title := name
	if title == "" {
		title = "Embedded Image"
	}
	sb.WriteString("\n> 🖼️ **[IMAGE: ")
	sb.WriteString(title)
	if location != "" {
		sb.WriteString(" (" + location + ")")
	}
	sb.WriteString("]**")
	if altText != "" && altText != title {
		sb.WriteString("\n> - **Description / Alt-Text**: " + altText)
	}
	if url != "" {
		sb.WriteString(fmt.Sprintf("\n![%s](%s)\n\n", title, url))
	} else {
		sb.WriteString("\n\n")
	}
	return sb.String()
}

// FormatImageText creates a concise text-only image reference.
func FormatImageText(name, altText, location string) string {
	title := name
	if title == "" {
		title = "Image"
	}
	if location != "" {
		title += " (" + location + ")"
	}
	if altText != "" && altText != name {
		return fmt.Sprintf("\n[IMAGE: %s - %s]\n\n", title, altText)
	}
	return fmt.Sprintf("\n[IMAGE: %s]\n\n", title)
}

// Extractor is the common interface for extracting text/markdown from documents.
type Extractor interface {
	Extract(ctx context.Context, r io.ReaderAt, size int64, opts Options) (*Result, error)
}

// Magic bytes signatures.
var (
	magicPDF = []byte("%PDF-")
	magicZIP = []byte{0x50, 0x4B, 0x03, 0x04} // PK..
	magicOLE = []byte{0xD0, 0xCF, 0x11, 0xE0, 0xA1, 0xB1, 0x1A, 0xE1}
)

// DetectFileType inspects both magic bytes and internal archive structure to determine file type.
func DetectFileType(r io.ReaderAt, size int64, filename string) (SupportedFileType, error) {
	if size <= 0 {
		return TypeUnknown, ErrEmptyFile
	}

	header := make([]byte, 512)
	n, _ := r.ReadAt(header, 0)
	if n < 4 {
		return TypeUnknown, ErrCorruptedFile
	}

	// 1. Check for PDF magic bytes
	if bytes.HasPrefix(header, magicPDF) {
		return TypePDF, nil
	}

	// 2. Check for legacy OLE2 binary (e.g. .xls)
	if n >= 8 && bytes.Equal(header[:8], magicOLE) {
		return TypeXLS, nil
	}

	// 3. Check for ZIP magic bytes (DOCX, PPTX, XLSX)
	if bytes.Equal(header[:4], magicZIP) {
		zr, err := zip.NewReader(r, size)
		if err == nil {
			var hasWord, hasPPT, hasXL bool
			for _, f := range zr.File {
				lower := strings.ToLower(f.Name)
				if strings.HasPrefix(lower, "word/") {
					hasWord = true
				} else if strings.HasPrefix(lower, "ppt/") {
					hasPPT = true
				} else if strings.HasPrefix(lower, "xl/") {
					hasXL = true
				}
			}
			if hasWord {
				return TypeDOCX, nil
			}
			if hasPPT {
				return TypePPTX, nil
			}
			if hasXL {
				return TypeXLSX, nil
			}
		}
	}

	// 4. Fallback to extension check if magic bytes inspection was inconclusive
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".pdf":
		return TypePDF, nil
	case ".docx":
		return TypeDOCX, nil
	case ".pptx":
		return TypePPTX, nil
	case ".xlsx":
		return TypeXLSX, nil
	case ".xls":
		return TypeXLS, nil
	}

	return TypeUnknown, ErrUnsupportedFileType
}

// GetExtractor returns the appropriate Extractor implementation based on file type.
func GetExtractor(fileType SupportedFileType) (Extractor, error) {
	switch fileType {
	case TypePDF:
		return &PDFExtractor{}, nil
	case TypeDOCX:
		return &DOCXExtractor{}, nil
	case TypePPTX:
		return &PPTXExtractor{}, nil
	case TypeXLSX, TypeXLS:
		return &ExcelExtractor{}, nil
	default:
		return nil, ErrUnsupportedFileType
	}
}

// ExecuteExtraction is the DRY coordinator that detects file type, runs extractor, and builds Result.
func ExecuteExtraction(ctx context.Context, r io.ReaderAt, size int64, filename string, opts Options) (*Result, error) {
	startTime := time.Now()

	sanitizedName := SanitizeFileName(filename)
	fileType, err := DetectFileType(r, size, sanitizedName)
	if err != nil {
		return nil, err
	}

	extractor, err := GetExtractor(fileType)
	if err != nil {
		return nil, err
	}

	result, err := extractor.Extract(ctx, r, size, opts)
	if err != nil {
		return nil, err
	}

	result.Filename = sanitizedName
	result.DetectedType = fileType
	result.OutputFormat = opts.Format
	result.DurationMs = time.Since(startTime).Milliseconds()
	result.Content = CleanExtractedText(result.Content)
	result.WordCount = CountWords(result.Content)

	return result, nil
}

// DRY Utilities

var unsafeChars = regexp.MustCompile(`[^\w\.-]`)
var consecutiveNewlines = regexp.MustCompile(`\n{3,}`)

// SanitizeFileName cleans file names to prevent path traversal and injection across all OSes.
func SanitizeFileName(name string) string {
	name = strings.ReplaceAll(name, `\`, `/`)
	base := filepath.Base(name)
	base = filepath.Clean(base)
	clean := unsafeChars.ReplaceAllString(base, "_")
	if clean == "" || clean == "." {
		clean = "uploaded_file"
	}
	return clean
}

// CountingLimitReader tracks uncompressed stream bytes and detects decompression limits.
type CountingLimitReader struct {
	R         io.Reader
	Limit     int64
	BytesRead int64
	HitLimit  bool
}

func (c *CountingLimitReader) Read(p []byte) (int, error) {
	if c.BytesRead >= c.Limit {
		c.HitLimit = true
		return 0, io.EOF
	}

	maxToRead := c.Limit - c.BytesRead
	if int64(len(p)) > maxToRead {
		p = p[:maxToRead]
	}

	n, err := c.R.Read(p)
	c.BytesRead += int64(n)
	if c.BytesRead >= c.Limit {
		c.HitLimit = true
	}
	return n, err
}

// CleanExtractedText trims trailing whitespace and limits redundant newlines.
func CleanExtractedText(s string) string {
	s = strings.TrimSpace(s)
	s = consecutiveNewlines.ReplaceAllString(s, "\n\n")
	return s
}

// CountWords counts words in text.
func CountWords(s string) int {
	count := 0
	inWord := false
	for _, r := range s {
		if unicode.IsSpace(r) {
			inWord = false
		} else if !inWord {
			inWord = true
			count++
		}
	}
	return count
}

// FormatMarkdownTable formats 2D table slices into clean Markdown tables.
func FormatMarkdownTable(headers []string, rows [][]string) string {
	if len(headers) == 0 && len(rows) == 0 {
		return ""
	}

	numCols := len(headers)
	for _, row := range rows {
		if len(row) > numCols {
			numCols = len(row)
		}
	}
	if numCols == 0 {
		return ""
	}

	var sb strings.Builder
	cleanCell := func(cell string) string {
		cell = strings.ReplaceAll(cell, "|", `\|`)
		cell = strings.ReplaceAll(cell, "\n", " ")
		return strings.TrimSpace(cell)
	}

	// Write Headers
	sb.WriteString("|")
	for i := 0; i < numCols; i++ {
		val := ""
		if i < len(headers) {
			val = cleanCell(headers[i])
		}
		if val == "" {
			val = fmt.Sprintf("Col %d", i+1)
		}
		sb.WriteString(" " + val + " |")
	}
	sb.WriteString("\n|")

	// Separator row
	for i := 0; i < numCols; i++ {
		sb.WriteString(" --- |")
	}
	sb.WriteString("\n")

	// Data rows
	for _, row := range rows {
		sb.WriteString("|")
		for i := 0; i < numCols; i++ {
			val := ""
			if i < len(row) {
				val = cleanCell(row[i])
			}
			sb.WriteString(" " + val + " |")
		}
		sb.WriteString("\n")
	}
	sb.WriteString("\n")

	return sb.String()
}
