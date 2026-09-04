package extractor

import (
	"archive/zip"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"path"
	"sort"
	"strconv"
	"strings"

	"convert2text/internal/assets"
)

// PPTXExtractor extracts text or markdown from Microsoft PowerPoint .pptx files with embedded image detection.
type PPTXExtractor struct{}

type slideFile struct {
	Index int
	File  *zip.File
}

func (e *PPTXExtractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts Options) (*Result, error) {
	zr, err := zip.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: failed to read pptx zip archive: %v", ErrCorruptedFile, err)
	}

	maxBytes := opts.MaxDecompressedBytes
	if maxBytes <= 0 {
		maxBytes = 150 * 1024 * 1024
	}

	// Discover and sort all slide XML files
	var slides []slideFile
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasPrefix(name, "ppt/slides/slide") && strings.HasSuffix(name, ".xml") {
			numStr := strings.TrimPrefix(name, "ppt/slides/slide")
			numStr = strings.TrimSuffix(numStr, ".xml")
			idx, err := strconv.Atoi(numStr)
			if err != nil {
				idx = len(slides) + 1
			}
			slides = append(slides, slideFile{Index: idx, File: f})
		}
	}

	if len(slides) == 0 {
		return nil, fmt.Errorf("%w: no slides found in presentation", ErrCorruptedFile)
	}

	sort.Slice(slides, func(i, j int) bool {
		return slides[i].Index < slides[j].Index
	})

	mediaMap := buildZipMediaMap(zr, "ppt/")
	var totalDecompressedBytes int64
	var fullContent strings.Builder
	var allImages []ExtractedImage

	for _, slide := range slides {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		remainingQuota := maxBytes - totalDecompressedBytes
		if remainingQuota <= 0 {
			return nil, ErrDecompressionLimit
		}

		rc, err := slide.File.Open()
		if err != nil {
			return nil, fmt.Errorf("failed to open slide %d: %w", slide.Index, err)
		}

		countingReader := &CountingLimitReader{
			R:     rc,
			Limit: remainingQuota,
		}

		slideRels := parseSlideRels(zr, slide.Index)
		slideLocation := fmt.Sprintf("Slide %d", slide.Index)

		slideText, slideImages, err := parseSlideXML(ctx, countingReader, opts.Format, slideRels, mediaMap, slideLocation)
		rc.Close()
		if countingReader.HitLimit {
			return nil, ErrDecompressionLimit
		}
		if err != nil {
			return nil, fmt.Errorf("error parsing slide %d: %w", slide.Index, err)
		}

		totalDecompressedBytes += countingReader.BytesRead
		allImages = append(allImages, slideImages...)

		trimmedText := strings.TrimSpace(slideText)
		if trimmedText != "" {
			if opts.Format == FormatMarkdown {
				fullContent.WriteString(fmt.Sprintf("## Slide %d\n\n%s\n\n", slide.Index, trimmedText))
			} else {
				fullContent.WriteString(fmt.Sprintf("--- Slide %d ---\n%s\n\n", slide.Index, trimmedText))
			}
		}
	}

	return &Result{
		Content: fullContent.String(),
		Images:  allImages,
		Metadata: map[string]interface{}{
			"file_format":  "pptx",
			"slide_count":  len(slides),
			"images_count": len(allImages),
		},
	}, nil
}

func parseSlideRels(zr *zip.Reader, slideIdx int) map[string]string {
	rels := make(map[string]string)
	relPath := fmt.Sprintf("ppt/slides/_rels/slide%d.xml.rels", slideIdx)
	for _, f := range zr.File {
		if strings.EqualFold(f.Name, relPath) {
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

func parseSlideXML(ctx context.Context, r io.Reader, format OutputFormat, rels map[string]string, mediaMap map[string]*zip.File, location string) (string, []ExtractedImage, error) {
	decoder := xml.NewDecoder(r)

	var sb strings.Builder
	var inParagraph bool
	var inTableCell bool
	var currentPText strings.Builder
	var currentCellText strings.Builder
	var currentRowCells []string
	var tableRows [][]string

	var extractedImages []ExtractedImage
	var currentPicName string
	var currentPicDescr string
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
			return "", nil, err
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
			case "t":
				var text string
				if err := decoder.DecodeElement(&text, &elem); err == nil {
					if inTableCell {
						currentCellText.WriteString(text)
					} else if inParagraph {
						currentPText.WriteString(text)
					}
				}
			case "cNvPr":
				for _, attr := range elem.Attr {
					if attr.Name.Local == "name" {
						currentPicName = attr.Value
					} else if attr.Name.Local == "descr" {
						currentPicDescr = attr.Value
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
								imgName := currentPicName
								if imgName == "" {
									imgName = targetBase
								}
								imgID, imgURL := store.Save(targetBase, "", currentPicDescr, location, data)
								relPath := "./assets/" + targetBase
								extImg := ExtractedImage{
									ID:           imgID,
									Filename:     targetBase,
									ContentType:  assets.DetectImageMIME(targetBase, data),
									SizeBytes:    int64(len(data)),
									AltText:      currentPicDescr,
									Location:     location,
									RelativePath: relPath,
									URL:          imgURL,
									Data:         data,
								}
								extractedImages = append(extractedImages, extImg)

								if format == FormatMarkdown {
									currentPText.WriteString(FormatImageMarkdown(imgName, currentPicDescr, relPath, location))
								} else {
									currentPText.WriteString(FormatImageText(imgName, currentPicDescr, location))
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
							sb.WriteString("- " + pText + "\n")
						} else {
							sb.WriteString(pText + "\n")
						}
					}
				}
			}
		}
	}

	return sb.String(), extractedImages, nil
}
