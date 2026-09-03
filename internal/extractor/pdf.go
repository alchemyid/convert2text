package extractor

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"convert2text/internal/assets"
	"github.com/dslipak/pdf"
)

var hexTagRegex = regexp.MustCompile(`<([0-9a-fA-F]+)>`)

// PDFExtractor extracts text or markdown from PDF files with spatial layout & table detection.
type PDFExtractor struct{}

func (e *PDFExtractor) Extract(ctx context.Context, r io.ReaderAt, size int64, opts Options) (res *Result, err error) {
	// Guard against potential panics from malformed PDF structures
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%w: failed to parse PDF: %v", ErrCorruptedFile, r)
		}
	}()

	reader, err := pdf.NewReader(r, size)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrCorruptedFile, err)
	}

	numPages := reader.NumPage()
	if numPages <= 0 {
		return nil, fmt.Errorf("%w: PDF has 0 pages", ErrCorruptedFile)
	}

	var sb strings.Builder
	validPages := 0
	var allPDFImages []ExtractedImage

	for pageNum := 1; pageNum <= numPages; pageNum++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		page := reader.Page(pageNum)
		pageImages := extractPDFPageImages(page, pageNum)
		allPDFImages = append(allPDFImages, pageImages...)

		pageContent := extractPageContent(page, opts.Format, pageImages, pageNum)

		trimmed := strings.TrimSpace(pageContent)
		if trimmed != "" {
			validPages++
			if opts.Format == FormatMarkdown {
				if numPages > 1 {
					sb.WriteString(fmt.Sprintf("## Page %d\n\n%s\n\n", pageNum, trimmed))
				} else {
					sb.WriteString(trimmed + "\n\n")
				}
			} else {
				if numPages > 1 {
					sb.WriteString(fmt.Sprintf("--- Page %d ---\n%s\n\n", pageNum, trimmed))
				} else {
					sb.WriteString(trimmed + "\n\n")
				}
			}
		}
	}

	return &Result{
		Content: sb.String(),
		Images:  allPDFImages,
		Metadata: map[string]interface{}{
			"file_format":  "pdf",
			"total_pages":  numPages,
			"valid_pages":  validPages,
			"images_count": len(allPDFImages),
		},
	}, nil
}

type pdfTextFragment struct {
	X, Y, W float64
	Text    string
}

type pdfTextLine struct {
	Y        float64
	Cells    []pdfTextCell
	FullText string
}

type pdfTextCell struct {
	X, W float64
	Text string
}

func extractPageContent(p pdf.Page, format OutputFormat, images []ExtractedImage, pageNum int) string {
	cmaps := buildPageFontCMaps(p)
	rawTexts := p.Content().Text

	var contentText string
	if len(rawTexts) == 0 {
		txt, _ := p.GetPlainText(nil)
		contentText = txt
	} else {
		var fragments []pdfTextFragment
		for _, t := range rawTexts {
			cm := cmaps[t.Font]
			var decoded strings.Builder
			for _, ch := range t.S {
				if cm != nil {
					if mapped, ok := cm[ch]; ok {
						decoded.WriteString(mapped)
						continue
					}
				}
				decoded.WriteRune(ch)
			}

			cleanStr := decoded.String()
			if cleanStr == "" {
				continue
			}

			fragments = append(fragments, pdfTextFragment{
				X:    t.X,
				Y:    t.Y,
				W:    t.W,
				Text: cleanStr,
			})
		}

		if len(fragments) > 0 {
			// Sort fragments top-to-bottom (Y descending in PDF coordinate space)
			sort.Slice(fragments, func(i, j int) bool {
				return fragments[i].Y > fragments[j].Y
			})

			var lines []pdfTextLine
			var currentFrags []pdfTextFragment
			var currentY float64 = -9999

			for _, frag := range fragments {
				if currentY < -9000 {
					currentY = frag.Y
					currentFrags = append(currentFrags, frag)
				} else if math.Abs(frag.Y-currentY) <= 3.5 {
					currentFrags = append(currentFrags, frag)
				} else {
					lines = append(lines, assemblePDFLine(currentY, currentFrags))
					currentFrags = []pdfTextFragment{frag}
					currentY = frag.Y
				}
			}
			if len(currentFrags) > 0 {
				lines = append(lines, assemblePDFLine(currentY, currentFrags))
			}

			contentText = formatPDFLines(lines, format)
		}
	}

	// If images exist on this page, inject semantic placeholders
	if len(images) > 0 {
		var imgSb strings.Builder
		for _, img := range images {
			relPath := img.RelativePath
			if relPath == "" {
				relPath = "./assets/" + img.Filename
			}
			if format == FormatMarkdown {
				imgSb.WriteString(FormatImageMarkdown(img.Filename, img.AltText, relPath, img.Location))
			} else {
				imgSb.WriteString(FormatImageText(img.Filename, img.AltText, img.Location))
			}
		}
		contentText += "\n" + imgSb.String()
	}

	return contentText
}

func extractPDFPageImages(p pdf.Page, pageNum int) []ExtractedImage {
	var images []ExtractedImage
	resVal := p.Resources()
	if resVal.IsNull() {
		return nil
	}
	xobjVal := resVal.Key("XObject")
	if xobjVal.IsNull() {
		return nil
	}

	store := assets.GetDefaultStore()

	for _, key := range xobjVal.Keys() {
		obj := xobjVal.Key(key)
		if obj.Key("Subtype").Name() == "Image" {
			width := int(obj.Key("Width").Int64())
			height := int(obj.Key("Height").Int64())
			filter := obj.Key("Filter").Name()

			location := fmt.Sprintf("Page %d", pageNum)
			imgName := fmt.Sprintf("page_%d_%s.png", pageNum, key)
			altText := fmt.Sprintf("Embedded Image / Diagram on Page %d", pageNum)
			if width > 0 && height > 0 {
				altText += fmt.Sprintf(" (%dx%d px)", width, height)
			}

			var imgURL, imgID string
			var data []byte

			rc := obj.Reader()
			if rc != nil {
				raw, _ := io.ReadAll(io.LimitReader(rc, 25*1024*1024))
				rc.Close()

				if filter == "DCTDecode" && len(raw) > 0 {
					data = raw
					imgName = fmt.Sprintf("page_%d_%s.jpg", pageNum, key)
				} else if len(raw) > 0 && width > 0 && height > 0 {
					// Raw pixel stream (e.g. from FlateDecode)
					if len(raw) >= width*height*3 && len(raw) < width*height*4 {
						// 24-bit RGB
						rgba := image.NewRGBA(image.Rect(0, 0, width, height))
						for y := 0; y < height; y++ {
							rowOffset := y * width * 3
							for x := 0; x < width; x++ {
								p := rowOffset + x*3
								rgba.SetRGBA(x, y, color.RGBA{R: raw[p], G: raw[p+1], B: raw[p+2], A: 255})
							}
						}
						var buf bytes.Buffer
						if err := png.Encode(&buf, rgba); err == nil {
							data = buf.Bytes()
						}
					} else if len(raw) >= width*height*4 {
						// 32-bit RGBA
						rgba := image.NewRGBA(image.Rect(0, 0, width, height))
						copy(rgba.Pix, raw)
						var buf bytes.Buffer
						if err := png.Encode(&buf, rgba); err == nil {
							data = buf.Bytes()
						}
					} else if len(raw) >= width*height {
						// 8-bit Grayscale
						gray := image.NewGray(image.Rect(0, 0, width, height))
						copy(gray.Pix, raw)
						var buf bytes.Buffer
						if err := png.Encode(&buf, gray); err == nil {
							data = buf.Bytes()
						}
					}
				}
			}

			mimeType := "image/png"
			if len(data) > 0 {
				mimeType = assets.DetectImageMIME(imgName, data)
				imgID, imgURL = store.Save(imgName, mimeType, altText, location, data)
			} else {
				imgID = fmt.Sprintf("pdf_p%d_%s", pageNum, key)
			}

			images = append(images, ExtractedImage{
				ID:           imgID,
				Filename:     imgName,
				ContentType:  mimeType,
				SizeBytes:    int64(len(data)),
				AltText:      altText,
				Location:     location,
				RelativePath: "./assets/" + imgName,
				URL:          imgURL,
				Data:         data,
			})
		}
	}
	return images
}

func assemblePDFLine(y float64, frags []pdfTextFragment) pdfTextLine {
	sort.Slice(frags, func(i, j int) bool {
		return frags[i].X < frags[j].X
	})

	var cells []pdfTextCell
	var currentCellText strings.Builder
	var cellStartX float64 = -1
	var lastRight float64 = -1

	for _, f := range frags {
		if cellStartX < 0 {
			cellStartX = f.X
			currentCellText.WriteString(f.Text)
			lastRight = f.X + f.W
			continue
		}

		gap := f.X - lastRight

		// Horizontal gap (>= 10 pt) indicates separate tabular column / cell
		if gap >= 10.0 {
			cellText := strings.TrimSpace(currentCellText.String())
			if cellText != "" {
				cells = append(cells, pdfTextCell{
					X:    cellStartX,
					W:    lastRight - cellStartX,
					Text: cellText,
				})
			}
			cellStartX = f.X
			currentCellText.Reset()
			currentCellText.WriteString(f.Text)
		} else {
			if gap > 1.5 && !strings.HasSuffix(currentCellText.String(), " ") && !strings.HasPrefix(f.Text, " ") {
				currentCellText.WriteString(" ")
			}
			currentCellText.WriteString(f.Text)
		}
		lastRight = f.X + f.W
	}

	if currentCellText.Len() > 0 {
		cellText := strings.TrimSpace(currentCellText.String())
		if cellText != "" {
			cells = append(cells, pdfTextCell{
				X:    cellStartX,
				W:    lastRight - cellStartX,
				Text: cellText,
			})
		}
	}

	var fullLine strings.Builder
	for i, c := range cells {
		if i > 0 {
			fullLine.WriteString("   ")
		}
		fullLine.WriteString(c.Text)
	}

	return pdfTextLine{
		Y:        y,
		Cells:    cells,
		FullText: strings.TrimSpace(fullLine.String()),
	}
}

func formatPDFLines(lines []pdfTextLine, format OutputFormat) string {
	var sb strings.Builder

	i := 0
	for i < len(lines) {
		line := lines[i]
		if line.FullText == "" {
			i++
			continue
		}

		// Table Detection: check if consecutive lines have multiple column cells (>= 3 columns)
		if len(line.Cells) >= 3 {
			tableLines := []pdfTextLine{line}
			j := i + 1
			for j < len(lines) {
				nextLine := lines[j]
				if len(nextLine.Cells) >= 3 && math.Abs(lines[j-1].Y-nextLine.Y) <= 85.0 {
					tableLines = append(tableLines, nextLine)
					j++
				} else {
					break
				}
			}

			if len(tableLines) >= 2 {
				if format == FormatMarkdown {
					sb.WriteString(renderPDFTableMarkdown(tableLines))
				} else {
					sb.WriteString(renderPDFTableText(tableLines))
				}
				sb.WriteString("\n\n")
				i = j
				continue
			}
		}

		text := line.FullText

		if isPDFBullet(text) {
			bulletContent := cleanPDFBullet(text)
			j := i + 1
			for j < len(lines) {
				nextLine := lines[j]
				nextText := nextLine.FullText
				if nextText == "" {
					j++
					continue
				}
				if isPDFBullet(nextText) || isPDFHeader(nextText) || len(nextLine.Cells) >= 3 {
					break
				}
				// Small line gap indicates continuation of paragraph or bullet item
				if math.Abs(lines[j-1].Y-nextLine.Y) <= 45.0 {
					bulletContent += " " + nextText
					j++
				} else {
					break
				}
			}
			if format == FormatMarkdown {
				sb.WriteString("- " + bulletContent + "\n")
			} else {
				sb.WriteString("  • " + bulletContent + "\n")
			}
			i = j
			continue
		}

		if isPDFHeader(text) {
			if format == FormatMarkdown {
				sb.WriteString("\n### " + text + "\n\n")
			} else {
				sb.WriteString("\n[" + text + "]\n\n")
			}
			i++
			continue
		}

		// Top of page title
		if i == 0 || (i <= 2 && line.Y > 850) {
			if format == FormatMarkdown {
				sb.WriteString("## " + text + "\n\n")
			} else {
				sb.WriteString(text + "\n" + strings.Repeat("=", len(text)) + "\n\n")
			}
			i++
			continue
		}

		// Regular paragraph (accumulate wrapped lines)
		paraContent := text
		j := i + 1
		for j < len(lines) {
			nextLine := lines[j]
			nextText := nextLine.FullText
			if nextText == "" {
				j++
				continue
			}
			if isPDFBullet(nextText) || isPDFHeader(nextText) || len(nextLine.Cells) >= 3 {
				break
			}
			if math.Abs(lines[j-1].Y-nextLine.Y) <= 45.0 {
				paraContent += " " + nextText
				j++
			} else {
				break
			}
		}
		sb.WriteString(paraContent + "\n\n")
		i = j
	}

	return sb.String()
}

func isPDFBullet(s string) bool {
	return strings.HasPrefix(s, "•") || strings.HasPrefix(s, "-") || strings.HasPrefix(s, "*")
}

func cleanPDFBullet(s string) string {
	s = strings.TrimPrefix(s, "•")
	s = strings.TrimPrefix(s, "-")
	s = strings.TrimPrefix(s, "*")
	return strings.TrimSpace(s)
}

func isPDFHeader(s string) bool {
	return strings.HasSuffix(s, ":") || strings.HasSuffix(s, "?")
}

func renderPDFTableMarkdown(tableLines []pdfTextLine) string {
	maxCols := 0
	for _, tl := range tableLines {
		if len(tl.Cells) > maxCols {
			maxCols = len(tl.Cells)
		}
	}
	if maxCols == 0 {
		return ""
	}

	var headers []string
	var rows [][]string

	firstLine := tableLines[0]
	if len(firstLine.Cells) < maxCols {
		diff := maxCols - len(firstLine.Cells)
		for d := 0; d < diff; d++ {
			headers = append(headers, "")
		}
		for _, c := range firstLine.Cells {
			headers = append(headers, c.Text)
		}
	} else {
		for _, c := range firstLine.Cells {
			headers = append(headers, c.Text)
		}
	}

	for _, tl := range tableLines[1:] {
		var row []string
		for _, c := range tl.Cells {
			row = append(row, c.Text)
		}
		for len(row) < maxCols {
			row = append(row, "")
		}
		rows = append(rows, row)
	}

	return FormatMarkdownTable(headers, rows)
}

func renderPDFTableText(tableLines []pdfTextLine) string {
	var sb strings.Builder
	for _, tl := range tableLines {
		var cells []string
		for _, c := range tl.Cells {
			cells = append(cells, c.Text)
		}
		sb.WriteString(strings.Join(cells, "\t") + "\n")
	}
	return sb.String()
}

// buildPageFontCMaps extracts and parses ToUnicode CMap streams for all fonts on the page.
func buildPageFontCMaps(p pdf.Page) map[string]map[rune]string {
	mergedCMap := make(map[string]map[rune]string)
	for _, fontName := range p.Fonts() {
		font := p.Font(fontName)
		v := font.V.Key("ToUnicode")
		if !v.IsNull() {
			rc := v.Reader()
			if rc != nil {
				cm := parseCMapStream(rc)
				rc.Close()

				base := font.BaseFont()
				cleanBase := base
				if idx := strings.Index(cleanBase, "+"); idx >= 0 {
					cleanBase = cleanBase[idx+1:]
				}
				cleanBase = strings.TrimPrefix(cleanBase, "/")

				if mergedCMap[cleanBase] == nil {
					mergedCMap[cleanBase] = make(map[rune]string)
				}
				for k, val := range cm {
					mergedCMap[cleanBase][k] = val
				}
				mergedCMap[fontName] = cm
			}
		}
	}
	return mergedCMap
}

// parseCMapStream decodes a ToUnicode CMap into rune-to-string mappings.
func parseCMapStream(r io.Reader) map[rune]string {
	cmap := make(map[rune]string)
	if r == nil {
		return cmap
	}
	scanner := bufio.NewScanner(r)
	inBfrange := false
	inBfchar := false

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasSuffix(line, "beginbfrange") {
			inBfrange = true
			continue
		} else if strings.HasSuffix(line, "endbfrange") {
			inBfrange = false
			continue
		} else if strings.HasSuffix(line, "beginbfchar") {
			inBfchar = true
			continue
		} else if strings.HasSuffix(line, "endbfchar") {
			inBfchar = false
			continue
		}

		if inBfrange {
			matches := hexTagRegex.FindAllStringSubmatch(line, -1)
			if len(matches) >= 3 {
				startCode, _ := strconv.ParseInt(matches[0][1], 16, 64)
				endCode, _ := strconv.ParseInt(matches[1][1], 16, 64)
				uniStart, _ := strconv.ParseInt(matches[2][1], 16, 64)
				for c := startCode; c <= endCode; c++ {
					u := rune(uniStart + (c - startCode))
					cmap[rune(c)] = string(u)
				}
			}
		} else if inBfchar {
			matches := hexTagRegex.FindAllStringSubmatch(line, -1)
			if len(matches) >= 2 {
				srcCode, _ := strconv.ParseInt(matches[0][1], 16, 64)
				uniHex, _ := strconv.ParseInt(matches[1][1], 16, 64)
				cmap[rune(srcCode)] = string(rune(uniHex))
			}
		}
	}
	return cmap
}
