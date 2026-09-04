package extractor

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"strings"

	"convert2text/internal/assets"
)

// DOCXExtractor extracts text or markdown from Microsoft Word .docx files with embedded image detection.
type DOCXExtractor struct{}

func (e *DOCXExtractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts Options) (*Result, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read docx zip archive: %v", ErrCorruptedFile, err)
	}

	var docFile *zip.File
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "word/document.xml") {
			docFile = f
			break
		}
	}

	if docFile == nil {
		return nil, fmt.Errorf("%w: missing word/document.xml", ErrCorruptedFile)
	}

	rc, err := docFile.Open()
	if err != nil {
		return nil, fmt.Errorf("failed to open word/document.xml: %w", err)
	}
	defer rc.Close()

	// Zip bomb protection: enforce decompression limit
	maxBytes := opts.MaxDecompressedBytes
	if maxBytes <= 0 {
		maxBytes = 150 * 1024 * 1024
	}
	countingReader := &CountingLimitReader{
		R:     rc,
		Limit: maxBytes,
	}

	rels := parseDocxRels(zr)
	mediaMap := buildZipMediaMap(zr, "word/")

	content, extractedImages, err := parseDocxXML(ctx, countingReader, opts.Format, rels, mediaMap)
	if countingReader.HitLimit {
		return nil, ErrDecompressionLimit
	}
	if err != nil {
		return nil, err
	}

	return &Result{
		Content: content,
		Images:  extractedImages,
		Metadata: map[string]interface{}{
			"file_format":  "docx",
			"images_count": len(extractedImages),
		},
	}, nil
}

func parseDocxRels(zr *zip.Reader) map[string]string {
	rels := make(map[string]string)
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, "word/_rels/document.xml.rels") {
			rc, err := f.Open()
			if err != nil {
				continue
			}
			defer rc.Close()

			decoder := xml.NewDecoder(rc)
			for {
				tok, err := decoder.Token()
				if err != nil {
					break
				}
				if start, ok := tok.(xml.StartElement); ok && start.Name.Local == "Relationship" {
					var id, target string
					for _, attr := range start.Attr {
						if attr.Name.Local == "Id" {
							id = attr.Value
						} else if attr.Name.Local == "Target" {
							target = attr.Value
						}
					}
					if id != "" && target != "" {
						rels[id] = target
					}
				}
			}
			break
		}
	}
	return rels
}

func buildZipMediaMap(zr *zip.Reader, prefix string) map[string]*zip.File {
	mediaMap := make(map[string]*zip.File)
	for _, f := range zr.File {
		lower := strings.ToLower(f.Name)
		if strings.HasPrefix(lower, prefix+"media/") || strings.Contains(lower, "/media/") {
			base := path.Base(f.Name)
			mediaMap[base] = f
			mediaMap[f.Name] = f
		}
	}
	return mediaMap
}

// parseDocxXML streams through word/document.xml to extract structured paragraphs, tables, and images.
func parseDocxXML(ctx context.Context, r io.Reader, format OutputFormat, rels map[string]string, mediaMap map[string]*zip.File) (string, []ExtractedImage, error) {
	decoder := xml.NewDecoder(r)

	var sb strings.Builder
	var inParagraph bool
	var inTableCell bool
	var isHeadingLevel int
	var isBullet bool
	var currentPText strings.Builder
	var currentCellText strings.Builder
	var currentRowCells []string
	var tableRows [][]string

	var extractedImages []ExtractedImage
	var currentDocPrName string
	var currentDocPrDescr string

	store := assets.GetDefaultStore()

	for {
		select {
		case <-ctx.Done():
			return "", nil, ctx.Err()
		default:
		}

		tok, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", nil, fmt.Errorf("xml parsing error: %w", err)
		}

		switch elem := tok.(type) {
		case xml.StartElement:
			name := elem.Name.Local
			switch name {
			case "tbl":
				tableRows = nil
			case "tr":
				currentRowCells = nil
			case "tc":
				inTableCell = true
				currentCellText.Reset()
			case "p":
				inParagraph = true
				currentPText.Reset()
				isHeadingLevel = 0
				isBullet = false
			case "pStyle":
				for _, attr := range elem.Attr {
					if attr.Name.Local == "val" {
						val := strings.ToLower(attr.Value)
						if strings.Contains(val, "heading1") || strings.Contains(val, "heading 1") {
							isHeadingLevel = 1
						} else if strings.Contains(val, "heading2") || strings.Contains(val, "heading 2") {
							isHeadingLevel = 2
						} else if strings.Contains(val, "heading3") || strings.Contains(val, "heading 3") {
							isHeadingLevel = 3
						} else if strings.Contains(val, "heading") {
							isHeadingLevel = 4
						}
					}
				}
			case "numPr":
				isBullet = true
			case "t":
				var text string
				if err := decoder.DecodeElement(&text, &elem); err == nil {
					if inTableCell {
						currentCellText.WriteString(text)
					} else if inParagraph {
						currentPText.WriteString(text)
					}
				}
			case "docPr":
				for _, attr := range elem.Attr {
					if attr.Name.Local == "name" {
						currentDocPrName = attr.Value
					} else if attr.Name.Local == "descr" {
						currentDocPrDescr = attr.Value
					}
				}
			case "blip":
				var embedID string
				for _, attr := range elem.Attr {
					if attr.Name.Local == "embed" {
						embedID = attr.Value
						break
					}
				}
				if embedID != "" && len(extractedImages) < 30 {
					target := rels[embedID]
					targetBase := path.Base(target)
					zipFile := mediaMap[targetBase]
					if zipFile == nil {
						zipFile = mediaMap[target]
					}
					if zipFile != nil && zipFile.UncompressedSize64 <= 10*1024*1024 {
						mrc, err := zipFile.Open()
						if err == nil {
							data, _ := io.ReadAll(io.LimitReader(mrc, 10*1024*1024))
							mrc.Close()
							if len(data) > 0 {
								imgName := currentDocPrName
								if imgName == "" {
									imgName = targetBase
								}
								imgID, imgURL := store.Save(targetBase, "", currentDocPrDescr, "Document Body", data)
								relPath := "./assets/" + targetBase
								extImg := ExtractedImage{
									ID:           imgID,
									Filename:     targetBase,
									ContentType:  assets.DetectImageMIME(targetBase, data),
									SizeBytes:    int64(len(data)),
									AltText:      currentDocPrDescr,
									Location:     "Document Body",
									RelativePath: relPath,
									URL:          imgURL,
									Data:         data,
								}
								extractedImages = append(extractedImages, extImg)

								// Inject placeholder in content flow
								if format == FormatMarkdown {
									currentPText.WriteString(FormatImageMarkdown(imgName, currentDocPrDescr, relPath, "Document Body"))
								} else {
									currentPText.WriteString(FormatImageText(imgName, currentDocPrDescr, "Document Body"))
								}
							}
						}
					}
				}
			}

		case xml.EndElement:
			name := elem.Name.Local
			switch name {
			case "tc":
				inTableCell = false
				currentRowCells = append(currentRowCells, strings.TrimSpace(currentCellText.String()))
			case "tr":
				if len(currentRowCells) > 0 {
					tableRows = append(tableRows, currentRowCells)
				}
			case "tbl":
				if len(tableRows) > 0 {
					if format == FormatMarkdown {
						headers := tableRows[0]
						rows := tableRows[1:]
						sb.WriteString(FormatMarkdownTable(headers, rows))
						sb.WriteString("\n")
					} else {
						for _, row := range tableRows {
							sb.WriteString(strings.Join(row, "\t") + "\n")
						}
						sb.WriteString("\n")
					}
				}
			case "p":
				inParagraph = false
				if !inTableCell {
					pText := strings.TrimSpace(currentPText.String())
					if pText != "" {
						if format == FormatMarkdown {
							if isHeadingLevel > 0 {
								sb.WriteString(strings.Repeat("#", isHeadingLevel) + " " + pText + "\n\n")
							} else if isBullet {
								sb.WriteString("- " + pText + "\n")
							} else {
								sb.WriteString(pText + "\n\n")
							}
						} else {
							sb.WriteString(pText + "\n\n")
						}
					}
				}
			}
		}
	}

	return sb.String(), extractedImages, nil
}
