package handler

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"

	"convert2text/internal/assets"
	"convert2text/internal/config"
	"convert2text/internal/extractor"
)

// ExtractHandler handles JSON and raw extraction requests.
type ExtractHandler struct {
	cfg *config.Config
}

// NewExtractHandler creates a new ExtractHandler instance.
func NewExtractHandler(cfg *config.Config) *ExtractHandler {
	return &ExtractHandler{cfg: cfg}
}

// HandleExtractJSON processes upload and returns structured JSON with content & statistics.
func (h *ExtractHandler) HandleExtractJSON(w http.ResponseWriter, r *http.Request) {
	result, err := h.processExtraction(r)
	if err != nil {
		h.handleError(w, err)
		return
	}
	JSONSuccess(w, http.StatusOK, result)
}

// HandleExtractRaw processes upload and returns plain raw text/markdown body.
func (h *ExtractHandler) HandleExtractRaw(w http.ResponseWriter, r *http.Request) {
	result, err := h.processExtraction(r)
	if err != nil {
		h.handleError(w, err)
		return
	}

	contentType := "text/markdown; charset=utf-8"
	ext := ".md"
	if result.OutputFormat == extractor.FormatText {
		contentType = "text/plain; charset=utf-8"
		ext = ".txt"
	}

	baseName := strings.TrimSuffix(result.Filename, filepath.Ext(result.Filename))
	downloadName := fmt.Sprintf("%s%s", baseName, ext)

	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s"`, downloadName))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(result.Content))
}

// HandleGetAsset serves stored image assets on-demand by ID or filename.
func (h *ExtractHandler) HandleGetAsset(w http.ResponseWriter, r *http.Request) {
	assetPath := strings.TrimPrefix(r.URL.Path, "/api/v1/assets/")
	if assetPath == r.URL.Path {
		assetPath = strings.TrimPrefix(r.URL.Path, "/assets/")
	}
	if assetPath == "" {
		http.NotFound(w, r)
		return
	}

	store := assets.GetDefaultStore()
	item, exists := store.Get(assetPath)
	if !exists {
		http.NotFound(w, r)
		return
	}

	w.Header().Set("Content-Type", item.ContentType)
	w.Header().Set("Cache-Control", "public, max-age=86400")
	w.Header().Set("Content-Length", fmt.Sprintf("%d", len(item.Data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(item.Data)
}

// HandleExtractBundle processes upload and returns a downloadable .zip bundle containing the Markdown/Text file and an assets/ folder.
func (h *ExtractHandler) HandleExtractBundle(w http.ResponseWriter, r *http.Request) {
	result, err := h.processExtraction(r)
	if err != nil {
		h.handleError(w, err)
		return
	}

	baseName := strings.TrimSuffix(result.Filename, filepath.Ext(result.Filename))
	if baseName == "" {
		baseName = "extracted"
	}
	docExt := ".md"
	if result.OutputFormat == extractor.FormatText {
		docExt = ".txt"
	}
	docFileName := baseName + docExt
	zipFileName := baseName + ".zip"

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, zipFileName))
	w.WriteHeader(http.StatusOK)

	zw := zip.NewWriter(w)
	defer zw.Close()

	// 1. Write the markdown/text document
	docWriter, err := zw.Create(docFileName)
	if err == nil {
		_, _ = docWriter.Write([]byte(result.Content))
	}

	// 2. Write all extracted image assets into assets/ folder
	store := assets.GetDefaultStore()
	for _, img := range result.Images {
		data := img.Data
		if len(data) == 0 {
			if item, exists := store.Get(img.ID); exists {
				data = item.Data
			}
		}
		if len(data) > 0 {
			assetPath := path.Join("assets", img.Filename)
			imgWriter, err := zw.Create(assetPath)
			if err == nil {
				_, _ = imgWriter.Write(data)
			}
		}
	}
}

// processExtraction is the DRY coordinator for reading multipart or raw stream into temporary storage and extracting.
func (h *ExtractHandler) processExtraction(r *http.Request) (*extractor.Result, error) {
	if r.Method != http.MethodPost {
		return nil, errors.New("method not allowed: only POST is supported")
	}

	ctx, cancel := context.WithTimeout(r.Context(), h.cfg.ExtractionTimeout)
	defer cancel()

	var reader io.Reader
	var filename string
	var formatParam string

	contentType := r.Header.Get("Content-Type")
	mediaType, _, _ := mime.ParseMediaType(contentType)

	if strings.HasPrefix(mediaType, "multipart/") {
		// Multipart form data
		mr, err := r.MultipartReader()
		if err != nil {
			return nil, fmt.Errorf("failed to parse multipart body: %w", err)
		}

		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				return nil, fmt.Errorf("failed reading multipart part: %w", err)
			}

			formName := part.FormName()
			if formName == "format" {
				buf := new(strings.Builder)
				_, _ = io.Copy(buf, part)
				formatParam = strings.TrimSpace(buf.String())
				continue
			}

			if formName == "file" || part.FileName() != "" {
				filename = part.FileName()
				reader = part
				break
			}
		}

		if reader == nil {
			return nil, errors.New("missing 'file' field in multipart request")
		}
	} else {
		// Direct binary payload
		filename = r.URL.Query().Get("filename")
		if filename == "" {
			filename = r.Header.Get("X-File-Name")
		}
		if filename == "" {
			_, params, _ := mime.ParseMediaType(r.Header.Get("Content-Disposition"))
			filename = params["filename"]
		}
		if filename == "" {
			filename = "upload.bin"
		}
		formatParam = r.URL.Query().Get("format")
		reader = r.Body
	}

	if formatParam == "" {
		formatParam = r.URL.Query().Get("format")
	}
	outputFormat := extractor.FormatMarkdown
	if strings.ToLower(formatParam) == "text" || strings.ToLower(formatParam) == "txt" {
		outputFormat = extractor.FormatText
	}

	// Stream to a temp file on disk to preserve RAM and keep compute utilization low
	tmpFile, err := os.CreateTemp("", "c2t-upload-*")
	if err != nil {
		return nil, fmt.Errorf("failed to allocate temp buffer: %w", err)
	}
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpFile.Name())
	}()

	written, err := io.Copy(tmpFile, reader)
	if err != nil {
		return nil, fmt.Errorf("failed writing upload payload: %w", err)
	}
	if written <= 0 {
		return nil, extractor.ErrEmptyFile
	}

	opts := extractor.Options{
		Format:               outputFormat,
		MaxDecompressedBytes: h.cfg.MaxDecompressedSizeBytes,
	}

	return extractor.ExecuteExtraction(ctx, tmpFile, written, filename, opts)
}

func (h *ExtractHandler) handleError(w http.ResponseWriter, err error) {
	statusCode := http.StatusBadRequest
	msg := err.Error()

	if errors.Is(err, extractor.ErrUnsupportedFileType) {
		statusCode = http.StatusUnsupportedMediaType
	} else if errors.Is(err, extractor.ErrDecompressionLimit) {
		statusCode = http.StatusRequestEntityTooLarge
	} else if strings.Contains(msg, "request body too large") {
		statusCode = http.StatusRequestEntityTooLarge
		msg = "uploaded file exceeds maximum allowed file size limit"
	} else if errors.Is(err, context.DeadlineExceeded) {
		statusCode = http.StatusGatewayTimeout
		msg = "extraction request timed out"
	}

	JSONError(w, statusCode, msg)
}
