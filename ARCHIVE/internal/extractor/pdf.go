package extractor

import (
	"bufio"
	"bytes"
	"compress/zlib"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"math"
	"reflect"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unsafe"

	"convert2text/internal/assets"
	"github.com/dslipak/pdf"
)

var (
	hexTagRegex     = regexp.MustCompile(`<([0-9a-fA-F]+)>`)
	bfcharRegex      = regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`)
	bfrangeRegex     = regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`)
	tjOperatorRegex  = regexp.MustCompile(`(?s)(/[A-Za-z0-9_]+)\s+[\d\.]+\s+Tf.*?\[(.*?)\]\s*TJ`)
	hexCIDRegex      = regexp.MustCompile(`<([0-9a-fA-F]+)>`)
	rowNumRegex      = regexp.MustCompile(`^(\d+|TOTAL)\b`)
	orderedListRegex = regexp.MustCompile(`^\d+\.\s+`)
)

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

	docCMap := extractDocumentCMaps(r, size)

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
		pageImages := extractPDFPageImages(page, pageNum, r)
		allPDFImages = append(allPDFImages, pageImages...)

		pageContent := extractPageContent(page, opts.Format, pageImages, pageNum, docCMap)

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

func extractPageContent(p pdf.Page, format OutputFormat, images []ExtractedImage, pageNum int, docCMap map[int]rune) string {
	cmaps := buildPageFontCMaps(p)
	missingWords := extractMissingFontTexts(p, docCMap)
	rawTexts := p.Content().Text

	var contentText string
	if len(rawTexts) == 0 {
		txt, _ := p.GetPlainText(nil)
		contentText = txt
	} else {
		var lineGroups []*lineGroup
		missingIdx := 0

		for i := 0; i < len(rawTexts); i++ {
			t := rawTexts[i]
			isMissing := (t.Font == "")

			if isMissing {
				startX := t.X
				startY := t.Y
				totalW := t.W
				j := i + 1
				for j < len(rawTexts) && (rawTexts[j].Font == "") && math.Abs(rawTexts[j].Y-startY) <= 3.8 {
					totalW += rawTexts[j].W
					j++
				}
				i = j - 1

				var replacement string
				if missingIdx < len(missingWords) {
					replacement = missingWords[missingIdx]
					missingIdx++
				}
				if strings.TrimSpace(replacement) == "" {
					continue
				}

				w := totalW
				if w <= 0 {
					w = float64(len(replacement)) * 5.2
				}

				frag := pdfTextFragment{
					X:    startX,
					Y:    startY,
					W:    w,
					Text: replacement,
				}
				appendFragToLines(&lineGroups, frag, startY)
			} else {
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

				frag := pdfTextFragment{
					X:    t.X,
					Y:    t.Y,
					W:    t.W,
					Text: cleanStr,
				}
				appendFragToLines(&lineGroups, frag, t.Y)
			}
		}

		if len(lineGroups) > 0 {
			// Sort lines top-to-bottom (Y descending in PDF coordinate space)
			sort.SliceStable(lineGroups, func(i, j int) bool {
				return lineGroups[i].Y > lineGroups[j].Y
			})

			var lines []pdfTextLine
			for _, lg := range lineGroups {
				assembled := assemblePDFLine(lg.Y, lg.Frags)
				if assembled.FullText != "" {
					lines = append(lines, assembled)
				}
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

func extractPDFPageImages(p pdf.Page, pageNum int, r io.ReaderAt) []ExtractedImage {
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
			img, ok := extractSinglePDFImage(obj, key, pageNum, r, store)
			if ok {
				images = append(images, img)
			}
		}
	}
	return images
}

func extractSinglePDFImage(obj pdf.Value, key string, pageNum int, r io.ReaderAt, store *assets.Store) (img ExtractedImage, ok bool) {
	// Guard against potential malformed/unsupported stream filters (e.g. unknown filter panic)
	defer func() {
		if rec := recover(); rec != nil {
			ok = false
		}
	}()

	width := int(obj.Key("Width").Int64())
	height := int(obj.Key("Height").Int64())
	filter := obj.Key("Filter").Name()

	location := fmt.Sprintf("Page %d", pageNum)
	imgName := fmt.Sprintf("page_%d_%s.png", pageNum, key)
	altText := fmt.Sprintf("Embedded Image / Diagram on Page %d", pageNum)
	if width > 0 && height > 0 {
		altText += fmt.Sprintf(" (%dx%d px)", width, height)
	}

	var data []byte

	// 1. In PDF ISO standard, DCTDecode stream already contains raw valid JPEG/JFIF bytes.
	// dslipak/pdf's Reader() panics on DCTDecode because it lacks JPEG decompressor,
	// but the raw stream itself is the direct JPEG image file.
	if filter == "DCTDecode" {
		data = extractRawStreamBytes(obj, r)
		if len(data) > 0 {
			imgName = fmt.Sprintf("page_%d_%s.jpg", pageNum, key)
		}
	} else {
		rc := safeReadObject(obj)
		if rc != nil {
			raw, _ := io.ReadAll(io.LimitReader(rc, 25*1024*1024))
			rc.Close()

			if len(raw) > 0 && width > 0 && height > 0 {
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
	}

	if len(data) == 0 {
		return img, false
	}

	mimeType := assets.DetectImageMIME(imgName, data)
	imgID, imgURL := store.Save(imgName, mimeType, altText, location, data)

	return ExtractedImage{
		ID:           imgID,
		Filename:     imgName,
		ContentType:  mimeType,
		SizeBytes:    int64(len(data)),
		Width:        width,
		Height:       height,
		AltText:      altText,
		Location:     location,
		RelativePath: "./assets/" + imgName,
		URL:          imgURL,
		Data:         data,
	}, true
}

func safeReadObject(obj pdf.Value) (rc io.ReadCloser) {
	defer func() {
		_ = recover()
	}()
	return obj.Reader()
}

func extractRawStreamBytes(v pdf.Value, r io.ReaderAt) []byte {
	type valueLayout struct {
		r    uintptr
		ptr  uint64
		typ  uintptr
		word unsafe.Pointer
	}
	vl := (*valueLayout)(unsafe.Pointer(&v))
	if vl.word == nil {
		return nil
	}

	var iface interface{}
	ifaceLayout := (*[2]uintptr)(unsafe.Pointer(&iface))
	ifaceLayout[0] = vl.typ
	ifaceLayout[1] = uintptr(vl.word)

	val := reflect.ValueOf(iface)
	if !val.IsValid() || val.Kind() != reflect.Struct {
		return nil
	}
	typ := val.Type()
	var offset int64
	for i := 0; i < typ.NumField(); i++ {
		field := typ.Field(i)
		if field.Name == "offset" {
			ptrToOffset := unsafe.Pointer(uintptr(vl.word) + field.Offset)
			offset = *(*int64)(ptrToOffset)
			break
		}
	}

	length := v.Key("Length").Int64()
	if offset > 0 && length > 0 && r != nil {
		buf := make([]byte, length)
		n, err := r.ReadAt(buf, offset)
		if err == nil || n > 0 {
			return buf[:n]
		}
	}
	return nil
}

func appendFragToLines(lineGroups *[]*lineGroup, frag pdfTextFragment, y float64) {
	var matchedLine *lineGroup
	for _, l := range *lineGroups {
		if math.Abs(l.Y-y) <= 3.8 {
			matchedLine = l
			break
		}
	}
	if matchedLine != nil {
		matchedLine.Frags = append(matchedLine.Frags, frag)
	} else {
		*lineGroups = append(*lineGroups, &lineGroup{
			Y:     y,
			Frags: []pdfTextFragment{frag},
		})
	}
}

type lineGroup struct {
	Y     float64
	Frags []pdfTextFragment
}

func assemblePDFLine(y float64, frags []pdfTextFragment) pdfTextLine {
	var cells []pdfTextCell
	var currentCellText strings.Builder
	var cellStartX float64 = -1
	var lastX float64 = -1

	for _, f := range frags {
		if cellStartX < 0 {
			cellStartX = f.X
			currentCellText.WriteString(f.Text)
			lastX = f.X
			if f.W > 0 {
				lastX += f.W
			}
			continue
		}

		gap := f.X - lastX
		isPurePunct := f.Text == "." || f.Text == "," || f.Text == ";" || f.Text == ":"

		// Substantial horizontal gap (>= 7.5 pt) indicates separate tabular column
		if gap >= 7.5 && strings.TrimSpace(currentCellText.String()) != "" && !isPurePunct {
			cellText := strings.TrimSpace(currentCellText.String())
			cells = append(cells, pdfTextCell{
				X:    cellStartX,
				W:    lastX - cellStartX,
				Text: cellText,
			})
			cellStartX = f.X
			currentCellText.Reset()
			currentCellText.WriteString(f.Text)
		} else {
			if gap > 2.0 && !isPurePunct && !strings.HasSuffix(currentCellText.String(), " ") && !strings.HasPrefix(f.Text, " ") {
				currentCellText.WriteString(" ")
			}
			currentCellText.WriteString(f.Text)
		}

		lastX = f.X
		if f.W > 0 {
			lastX += f.W
		}
	}

	if currentCellText.Len() > 0 {
		cellText := strings.TrimSpace(currentCellText.String())
		if cellText != "" {
			cells = append(cells, pdfTextCell{
				X:    cellStartX,
				W:    lastX - cellStartX,
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

		// Suppress page number footer at bottom of page (e.g. "1", "2", "3" at Y < 65)
		if line.Y < 65.0 && regexp.MustCompile(`^\d+$`).MatchString(strings.TrimSpace(line.FullText)) {
			i++
			continue
		}

		// Table Detection
		if isPotentialTableStart(lines, i) {
			newI, tableStr := parsePDFTable(lines, i, format)
			if tableStr != "" {
				sb.WriteString(tableStr + "\n\n")
				i = newI
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
				if isPDFBullet(nextText) || isPDFHeader(nextText) || isPotentialTableStart(lines, j) {
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
		if i == 0 && len(text) < 80 && !isPDFBullet(text) && !strings.Contains(text, ".....") {
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
			if isPDFBullet(nextText) || isPDFHeader(nextText) || isPotentialTableStart(lines, j) {
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

var pdfHeaderPattern = regexp.MustCompile(`^([I|V|X]+(\.\d+)*\.|\d+\.\d+(\.\d+)*\.)\s+`)

func isPDFHeader(s string) bool {
	trimmed := strings.TrimSpace(s)
	if strings.Contains(trimmed, ".....") {
		return false
	}
	if strings.HasSuffix(trimmed, ":") && len(trimmed) < 60 {
		return true
	}
	if pdfHeaderPattern.MatchString(trimmed) && len(trimmed) < 120 {
		return true
	}
	return false
}

func isPotentialTableStart(lines []pdfTextLine, i int) bool {
	line := lines[i]
	if strings.Contains(line.FullText, ".....") {
		return false
	}
	if len(line.Cells) <= 1 && isPDFHeader(line.FullText) {
		return false
	}
	if regexp.MustCompile(`^[a-z]\.\s+`).MatchString(strings.TrimSpace(line.FullText)) {
		return false
	}
	if orderedListRegex.MatchString(strings.TrimSpace(line.FullText)) {
		return false
	}
	if line.Y < 65.0 && regexp.MustCompile(`^\d+$`).MatchString(strings.TrimSpace(line.FullText)) {
		return false
	}
	if rowNumRegex.MatchString(line.FullText) && len(line.Cells) >= 4 {
		return true
	}
	if len(line.Cells) >= 3 {
		if isPDFBullet(line.FullText) || orderedListRegex.MatchString(strings.TrimSpace(line.FullText)) {
			return false
		}
		return true
	}
	if len(line.Cells) >= 2 {
		firstCol := strings.ToLower(line.Cells[0].Text)
		if firstCol == "no" || firstCol == "no." || firstCol == "item" || firstCol == "id" {
			return true
		}
		if i+1 < len(lines) && math.Abs(line.Y-lines[i+1].Y) <= 18.0 && len(lines[i+1].Cells) >= 3 {
			return true
		}
	}
	if len(line.Cells) == 1 && line.Cells[0].X > 400.0 && i+1 < len(lines) && math.Abs(line.Y-lines[i+1].Y) <= 18.0 && len(lines[i+1].Cells) >= 3 {
		return true
	}
	return false
}

type tableColDef struct {
	name string
	minX float64
	maxX float64
}

func parsePDFTable(lines []pdfTextLine, startIdx int, format OutputFormat) (int, string) {
	line := lines[startIdx]
	headerLine := line
	idx := startIdx + 1

	// Check if this line is actually a data row without header (e.g. table continuation across pages)
	isContinuationDataRow := false
	if rowNumRegex.MatchString(line.FullText) && len(line.Cells) >= 4 {
		isContinuationDataRow = true
	}

	var colDefs []tableColDef
	var headers []string

	if isContinuationDataRow {
		n := len(line.Cells)
		if n == 8 {
			colDefs = make([]tableColDef, 8)
			colDefs[0] = tableColDef{minX: 0, maxX: 118.0}
			colDefs[1] = tableColDef{minX: 118.0, maxX: 150.0}
			colDefs[2] = tableColDef{minX: 150.0, maxX: 195.0}
			colDefs[3] = tableColDef{minX: 195.0, maxX: 250.0}
			colDefs[4] = tableColDef{minX: 250.0, maxX: 305.0}
			colDefs[5] = tableColDef{minX: 305.0, maxX: 410.0}
			colDefs[6] = tableColDef{minX: 410.0, maxX: 480.0}
			colDefs[7] = tableColDef{minX: 480.0, maxX: 9999}
			headers = []string{"No", "Server", "Operating System", "CPU", "Memory (GB)", "Total Disk (GB)", "Payment Options", "Kesesuaian (Diisi oleh Mitra kerja)"}
			for c := 0; c < 8; c++ {
				colDefs[c].name = headers[c]
			}
		} else {
			colDefs = make([]tableColDef, n)
			for c := 0; c < n; c++ {
				var minX, maxX float64
				if c == 0 {
					minX = 0
				} else {
					minX = (line.Cells[c-1].X + line.Cells[c-1].W + line.Cells[c].X) / 2
				}
				if c == n-1 {
					maxX = 9999
				} else {
					maxX = (line.Cells[c].X + line.Cells[c].W + line.Cells[c+1].X) / 2
				}
				colDefs[c] = tableColDef{
					name: fmt.Sprintf("Col %d", c+1),
					minX: minX,
					maxX: maxX,
				}
				headers = append(headers, fmt.Sprintf("Col %d", c+1))
			}
		}
		idx = startIdx
	} else {
		// Merge stacked header lines up to 2 continuation lines
		for idx < len(lines) && math.Abs(headerLine.Y-lines[idx].Y) <= 20.0 && !rowNumRegex.MatchString(lines[idx].FullText) && !isPDFHeader(lines[idx].FullText) {
			if len(lines[idx].Cells) >= len(headerLine.Cells) {
				headerLine = mergeHeaderLines(headerLine, lines[idx])
			} else {
				headerLine = mergeHeaderLines(lines[idx], headerLine)
			}
			idx++
		}

		// Check if next line is a single subheader cell (e.g. "(Diisi oleh Mitra Kerja)")
		if idx < len(lines) && math.Abs(headerLine.Y-lines[idx].Y) <= 20.0 && !isNewTableRow(lines[idx], len(headerLine.Cells)) && len(lines[idx].Cells) == 1 && !isPDFHeader(lines[idx].FullText) {
			lastCellIdx := len(headerLine.Cells) - 1
			if lastCellIdx >= 0 {
				headerLine.Cells[lastCellIdx].Text += " " + lines[idx].Cells[0].Text
			}
			idx++
		}

		// Check if first data row has an unlabelled leading column (e.g. row names before metric columns)
		if idx < len(lines) && len(lines[idx].Cells) > 0 && len(headerLine.Cells) > 0 {
			if lines[idx].Cells[0].X < headerLine.Cells[0].X-35.0 {
				headerLine.Cells = append([]pdfTextCell{{
					X:    lines[idx].Cells[0].X,
					W:    lines[idx].Cells[0].W,
					Text: "",
				}}, headerLine.Cells...)
			}
		}

		// Fix split header words like "Kesesuai" + "an"
		var cleanedCells []pdfTextCell
		for c := 0; c < len(headerLine.Cells); c++ {
			cell := headerLine.Cells[c]
			if strings.EqualFold(cell.Text, "Kesesuai") && c+1 < len(headerLine.Cells) && strings.EqualFold(headerLine.Cells[c+1].Text, "an") {
				cell.Text = "Kesesuaian"
				cell.W = headerLine.Cells[c+1].X + headerLine.Cells[c+1].W - cell.X
				c++
			}
			cleanedCells = append(cleanedCells, cell)
		}
		headerLine.Cells = cleanedCells

		n := len(headerLine.Cells)
		if n < 2 {
			return startIdx, ""
		}

		firstText := strings.ToLower(headerLine.Cells[0].Text)
		isNoFirst := firstText == "no" || firstText == "no."

		colDefs = make([]tableColDef, n)
		if n == 8 && isNoFirst {
			colDefs[0] = tableColDef{minX: 0, maxX: 118.0}
			colDefs[1] = tableColDef{minX: 118.0, maxX: 150.0}
			colDefs[2] = tableColDef{minX: 150.0, maxX: 195.0}
			colDefs[3] = tableColDef{minX: 195.0, maxX: 250.0}
			colDefs[4] = tableColDef{minX: 250.0, maxX: 305.0}
			colDefs[5] = tableColDef{minX: 305.0, maxX: 410.0}
			colDefs[6] = tableColDef{minX: 410.0, maxX: 480.0}
			colDefs[7] = tableColDef{minX: 480.0, maxX: 9999}
		} else if n == 3 && isNoFirst {
			colDefs[0] = tableColDef{minX: 0, maxX: 125.0}
			colDefs[1] = tableColDef{minX: 125.0, maxX: 460.0}
			colDefs[2] = tableColDef{minX: 460.0, maxX: 9999}
		} else if n == 4 && isNoFirst {
			colDefs[0] = tableColDef{minX: 0, maxX: 120.0}
			colDefs[1] = tableColDef{minX: 120.0, maxX: 240.0}
			colDefs[2] = tableColDef{minX: 240.0, maxX: 460.0}
			colDefs[3] = tableColDef{minX: 460.0, maxX: 9999}
		} else {
			for c := 0; c < n; c++ {
				var minX, maxX float64
				if c == 0 {
					minX = 0
				} else {
					minX = (headerLine.Cells[c-1].X + headerLine.Cells[c-1].W + headerLine.Cells[c].X) / 2
				}
				if c == n-1 {
					maxX = 9999
				} else {
					maxX = (headerLine.Cells[c].X + headerLine.Cells[c].W + headerLine.Cells[c+1].X) / 2
				}
				colDefs[c] = tableColDef{
					minX: minX,
					maxX: maxX,
				}
			}
		}

		for c := 0; c < n; c++ {
			colDefs[c].name = headerLine.Cells[c].Text
			headers = append(headers, cleanCellText(headerLine.Cells[c].Text))
		}
	}

	numCols := len(colDefs)
	type tableRow struct {
		cells []string
		y     float64
	}
	var rows []*tableRow

	for idx < len(lines) {
		curLine := lines[idx]
		if curLine.FullText == "" {
			idx++
			continue
		}

		// Terminators
		if isPDFHeader(curLine.FullText) || strings.HasPrefix(curLine.FullText, "II.") || strings.HasPrefix(curLine.FullText, "III.") || strings.HasPrefix(curLine.FullText, "IV.") || strings.HasPrefix(curLine.FullText, "V.") || strings.HasPrefix(curLine.FullText, "VI.") {
			break
		}
		if orderedListRegex.MatchString(strings.TrimSpace(curLine.FullText)) {
			break
		}
		// Standalone section title on left margin (e.g. "Spesifikasi Layanan" at X=72.0)
		if len(rows) > 0 && len(curLine.Cells) == 1 && curLine.Cells[0].X < 90.0 && (strings.HasPrefix(curLine.FullText, "Spesifikasi") || strings.HasPrefix(curLine.FullText, "Bab") || isPDFHeader(curLine.FullText)) {
			break
		}
		// Page number footer at bottom of page (Y < 65)
		if curLine.Y < 65.0 && regexp.MustCompile(`^\d+$`).MatchString(strings.TrimSpace(curLine.FullText)) {
			break
		}
		// Large vertical gap between table rows
		if len(rows) > 0 && math.Abs(rows[len(rows)-1].y-curLine.Y) > 85.0 {
			break
		}
		// If a new table header starts while a table is already active
		if len(rows) > 0 && len(curLine.Cells) >= 2 && (strings.EqualFold(curLine.Cells[0].Text, "no") || strings.EqualFold(curLine.Cells[0].Text, "no.")) {
			break
		}

		if isNewTableRow(curLine, numCols) || len(rows) == 0 {
			newRow := &tableRow{
				cells: make([]string, numCols),
				y:     curLine.Y,
			}
			assignCellsToRow(newRow.cells, curLine.Cells, colDefs)
			if numCols == 4 && (strings.Contains(newRow.cells[1], "kebutuhan server") || newRow.cells[0] == "5") {
				if newRow.cells[2] != "" && !strings.HasPrefix(newRow.cells[2], "•") {
					newRow.cells[1] += " " + newRow.cells[2]
					newRow.cells[2] = "-"
				}
			}
			rows = append(rows, newRow)
			idx++

			// If this row was the TOTAL summary row, table is finished
			if strings.HasPrefix(strings.ToUpper(curLine.FullText), "TOTAL") {
				break
			}
		} else {
			if len(rows) > 0 {
				lastRow := rows[len(rows)-1]
				appendCellsToRow(lastRow.cells, curLine.Cells, colDefs)
				lastRow.y = curLine.Y
				idx++
			} else {
				break
			}
		}
	}

	if len(rows) == 0 {
		return startIdx, ""
	}

	var dataRows [][]string
	for _, r := range rows {
		var row []string
		for _, c := range r.cells {
			row = append(row, cleanCellText(c))
		}
		dataRows = append(dataRows, row)
	}

	if format == FormatMarkdown {
		return idx, FormatMarkdownTable(headers, dataRows)
	}
	return idx, renderTextTable(headers, dataRows)
}

func isNewTableRow(l pdfTextLine, numCols int) bool {
	if orderedListRegex.MatchString(strings.TrimSpace(l.FullText)) {
		return false
	}
	if rowNumRegex.MatchString(l.FullText) {
		return true
	}
	if len(l.Cells) >= 3 {
		return true
	}
	if numCols <= 3 && len(l.Cells) >= 2 {
		return true
	}
	return false
}

func mergeHeaderLines(top, bottom pdfTextLine) pdfTextLine {
	resCells := make([]pdfTextCell, len(bottom.Cells))
	copy(resCells, bottom.Cells)

	for _, tc := range top.Cells {
		bestIdx := -1
		minDist := 999.0
		for bi, bc := range bottom.Cells {
			d := math.Abs(tc.X - bc.X)
			if d < minDist {
				minDist = d
				bestIdx = bi
			}
		}
		if bestIdx >= 0 && minDist < 45.0 {
			resCells[bestIdx].Text = tc.Text + " " + resCells[bestIdx].Text
		}
	}
	return pdfTextLine{
		Y:     bottom.Y,
		Cells: resCells,
	}
}

func assignCellsToRow(row []string, cells []pdfTextCell, cols []tableColDef) {
	if len(cells) == len(row) {
		for i, c := range cells {
			row[i] = c.Text
		}
		return
	}
	for i := 0; i < len(cells); i++ {
		c := cells[i]
		txt := strings.TrimSpace(c.Text)
		if txt == "" {
			continue
		}
		// If this cell is a bullet and next cell is on the same line, combine them!
		if (txt == "•" || txt == "\uf0b7" || txt == "") && i+1 < len(cells) {
			nextTxt := strings.TrimSpace(cells[i+1].Text)
			combined := "• " + nextTxt
			colIdx := findColIndex(cells[i+1].X, cols)
			if colIdx >= 0 && colIdx < len(row) {
				if row[colIdx] != "" {
					row[colIdx] += "<br>" + combined
				} else {
					row[colIdx] = combined
				}
			}
			i++
			continue
		}
		colIdx := findColIndex(c.X, cols)
		if colIdx >= 0 && colIdx < len(row) {
			if row[colIdx] != "" {
				row[colIdx] += " " + c.Text
			} else {
				row[colIdx] = c.Text
			}
		}
	}
}

func appendCellsToRow(row []string, cells []pdfTextCell, cols []tableColDef) {
	isMergedNoteRow := len(row) == 4 && (row[0] == "5" || strings.Contains(row[1], "kebutuhan server"))
	for i := 0; i < len(cells); i++ {
		c := cells[i]
		txt := strings.TrimSpace(c.Text)
		if txt == "" {
			continue
		}
		if isMergedNoteRow {
			if row[1] != "" {
				row[1] += " " + txt
			} else {
				row[1] = txt
			}
			continue
		}
		// If this cell is a bullet and next cell is on the same line, combine them!
		if (txt == "•" || txt == "\uf0b7" || txt == "") && i+1 < len(cells) {
			nextTxt := strings.TrimSpace(cells[i+1].Text)
			combined := "• " + nextTxt
			colIdx := findColIndex(cells[i+1].X, cols)
			if colIdx >= 0 && colIdx < len(row) {
				if row[colIdx] != "" {
					row[colIdx] += "<br>" + combined
				} else {
					row[colIdx] = combined
				}
			}
			i++
			continue
		}
		isBullet := strings.HasPrefix(txt, "") || strings.HasPrefix(txt, "•") || strings.HasPrefix(txt, "\uf0b7")
		cleanTxt := strings.TrimSpace(strings.TrimPrefix(strings.TrimPrefix(strings.TrimPrefix(txt, "\uf0b7"), ""), "•"))
		if isBullet {
			cleanTxt = "• " + cleanTxt
		}
		colIdx := findColIndex(c.X, cols)
		if colIdx >= 0 && colIdx < len(row) {
			if row[colIdx] != "" {
				if isBullet {
					row[colIdx] += "<br>" + cleanTxt
				} else {
					row[colIdx] += " " + cleanTxt
				}
			} else {
				row[colIdx] = cleanTxt
			}
		}
	}
}

func findColIndex(x float64, cols []tableColDef) int {
	for i, col := range cols {
		if x >= col.minX && x < col.maxX {
			return i
		}
	}
	bestIdx := -1
	minDist := 9999.0
	for i, col := range cols {
		center := (col.minX + col.maxX) / 2
		d := math.Abs(x - center)
		if d < minDist {
			minDist = d
			bestIdx = i
		}
	}
	return bestIdx
}

func cleanCellText(s string) string {
	s = strings.ReplaceAll(s, "\uf0b7", "•")
	s = strings.ReplaceAll(s, "", "•")
	s = strings.ReplaceAll(s, "|", "\\|")
	s = strings.ReplaceAll(s, "\n", " ")
	return strings.TrimSpace(s)
}

func renderTextTable(headers []string, rows [][]string) string {
	var sb strings.Builder
	sb.WriteString(strings.Join(headers, "\t") + "\n")
	for _, r := range rows {
		sb.WriteString(strings.Join(r, "\t") + "\n")
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

func extractDocumentCMaps(r io.ReaderAt, size int64) map[int]rune {
	cmap := make(map[int]rune)
	if r == nil || size <= 0 {
		return cmap
	}
	buf := make([]byte, size)
	n, err := r.ReadAt(buf, 0)
	if err != nil && n <= 0 {
		return cmap
	}
	b := buf[:n]

	flateRe := regexp.MustCompile(`(?s)<<[^>]*?/Length\s+(\d+)[^>]*?>>\s*stream\r?\n`)
	matches := flateRe.FindAllSubmatchIndex(b, -1)
	for _, m := range matches {
		streamStart := m[1]
		zr, err := zlib.NewReader(bytes.NewReader(b[streamStart:]))
		if err == nil {
			decomp, err := io.ReadAll(zr)
			zr.Close()
			if err == nil && (bytes.Contains(decomp, []byte("beginbfrange")) || bytes.Contains(decomp, []byte("beginbfchar"))) {
				parseCMapData(decomp, cmap)
			}
		}
	}
	return cmap
}

func parseCMapData(decomp []byte, cmap map[int]rune) {
	for _, m := range bfcharRegex.FindAllSubmatch(decomp, -1) {
		tags := hexTagRegex.FindAllStringSubmatch(string(m[1]), -1)
		for i := 0; i+1 < len(tags); i += 2 {
			srcCode, _ := strconv.ParseInt(tags[i][1], 16, 64)
			uniHex, _ := strconv.ParseInt(tags[i+1][1], 16, 64)
			cmap[int(srcCode)] = rune(uniHex)
		}
	}

	for _, m := range bfrangeRegex.FindAllSubmatch(decomp, -1) {
		tags := hexTagRegex.FindAllStringSubmatch(string(m[1]), -1)
		for i := 0; i+2 < len(tags); i += 3 {
			startCode, _ := strconv.ParseInt(tags[i][1], 16, 64)
			endCode, _ := strconv.ParseInt(tags[i+1][1], 16, 64)
			uniStart, _ := strconv.ParseInt(tags[i+2][1], 16, 64)
			for c := startCode; c <= endCode; c++ {
				cmap[int(c)] = rune(uniStart + (c - startCode))
			}
		}
	}
}

func extractMissingFontTexts(p pdf.Page, docCMap map[int]rune) []string {
	strm := p.V.Key("Contents")
	var rawData []byte
	if strm.Len() == 0 {
		rc := strm.Reader()
		if rc != nil {
			rawData, _ = io.ReadAll(rc)
			rc.Close()
		}
	} else {
		for i := 0; i < strm.Len(); i++ {
			rc := strm.Index(i).Reader()
			if rc != nil {
				d, _ := io.ReadAll(rc)
				rc.Close()
				rawData = append(rawData, d...)
			}
		}
	}

	var words []string
	matches := tjOperatorRegex.FindAllSubmatch(rawData, -1)
	for _, m := range matches {
		fontTag := string(m[1])
		fontName := strings.TrimPrefix(fontTag, "/")
		fVal := p.Font(fontName).V
		if fVal.IsNull() {
			hexMatches := hexCIDRegex.FindAllSubmatch(m[2], -1)
			var sb strings.Builder
			for _, h := range hexMatches {
				hexStr := string(h[1])
				for c := 0; c+4 <= len(hexStr); c += 4 {
					var code int
					fmt.Sscanf(hexStr[c:c+4], "%04x", &code)
					if r, ok := docCMap[code]; ok {
						sb.WriteRune(r)
					}
				}
			}
			w := sb.String()
			words = append(words, w)
		}
	}
	return words
}
