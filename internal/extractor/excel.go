package extractor

import (
	"archive/zip"
	"context"
	"fmt"
	"io"
	"path"
	"strings"

	"convert2text/internal/assets"
	"github.com/xuri/excelize/v2"
)

// ExcelExtractor extracts structured sheets, tables, and embedded images from Excel (.xlsx) workbooks.
type ExcelExtractor struct{}

func (e *ExcelExtractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts Options) (*Result, error) {
	// Wrap ReaderAt into a SectionReader so excelize can read it as io.Reader
	sr := io.NewSectionReader(r, 0, size)

	// excelize.OpenReader with memory-efficient streaming options
	f, err := excelize.OpenReader(sr, excelize.Options{
		UnzipXMLSizeLimit: opts.MaxDecompressedBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read excel workbook: %v", ErrCorruptedFile, err)
	}
	defer f.Close()

	sheetList := f.GetSheetList()
	if len(sheetList) == 0 {
		return nil, fmt.Errorf("%w: workbook contains no sheets", ErrCorruptedFile)
	}

	var sb strings.Builder
	processedSheets := 0

	for _, sheetName := range sheetList {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		rows, err := f.Rows(sheetName)
		if err != nil {
			continue
		}

		var sheetRows [][]string
		for rows.Next() {
			select {
			case <-ctx.Done():
				rows.Close()
				return nil, ctx.Err()
			default:
			}

			row, err := rows.Columns()
			if err != nil {
				break
			}

			// Filter out completely empty rows
			var hasContent bool
			for _, cell := range row {
				if strings.TrimSpace(cell) != "" {
					hasContent = true
					break
				}
			}
			if hasContent {
				sheetRows = append(sheetRows, row)
			}
		}
		rows.Close()

		if len(sheetRows) == 0 {
			continue
		}

		processedSheets++
		if opts.Format == FormatMarkdown {
			sb.WriteString(fmt.Sprintf("## Sheet: %s\n\n", sheetName))
			headers := sheetRows[0]
			var dataRows [][]string
			if len(sheetRows) > 1 {
				dataRows = sheetRows[1:]
			}
			sb.WriteString(FormatMarkdownTable(headers, dataRows))
			sb.WriteString("\n")
		} else {
			sb.WriteString(fmt.Sprintf("--- Sheet: %s ---\n", sheetName))
			for _, row := range sheetRows {
				sb.WriteString(strings.Join(row, "\t") + "\n")
			}
			sb.WriteString("\n")
		}
	}

	// Extract any embedded images from xl/media/
	var extractedImages []ExtractedImage
	zr, zErr := zip.NewReader(r, size)
	if zErr == nil {
		store := assets.GetDefaultStore()
		for _, file := range zr.File {
			lower := strings.ToLower(file.Name)
			if (strings.HasPrefix(lower, "xl/media/") || strings.Contains(lower, "/media/")) && len(extractedImages) < 30 {
				if file.UncompressedSize64 <= 10*1024*1024 {
					rc, err := file.Open()
					if err == nil {
						data, _ := io.ReadAll(io.LimitReader(rc, 10*1024*1024))
						rc.Close()
						if len(data) > 0 {
							base := path.Base(file.Name)
							altText := fmt.Sprintf("Embedded Excel Chart / Image: %s", base)
							imgID, imgURL := store.Save(base, "", altText, "Workbook Media", data)
							relPath := "./assets/" + base
							extImg := ExtractedImage{
								ID:           imgID,
								Filename:     base,
								ContentType:  assets.DetectImageMIME(base, data),
								SizeBytes:    int64(len(data)),
								AltText:      altText,
								Location:     "Workbook Media",
								RelativePath: relPath,
								URL:          imgURL,
								Data:         data,
							}
							extractedImages = append(extractedImages, extImg)

							if opts.Format == FormatMarkdown {
								sb.WriteString(FormatImageMarkdown(base, altText, relPath, "Workbook Media"))
							} else {
								sb.WriteString(FormatImageText(base, altText, "Workbook Media"))
							}
						}
					}
				}
			}
		}
	}

	return &Result{
		Content: sb.String(),
		Images:  extractedImages,
		Metadata: map[string]interface{}{
			"file_format":   "xlsx",
			"sheet_count":   len(sheetList),
			"active_sheets": processedSheets,
			"images_count":  len(extractedImages),
		},
	}, nil
}
