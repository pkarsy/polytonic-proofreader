package main

import (
	"bytes"
	"fmt"
	"html"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Page represents one page of OCR or EL text.
type Page struct {
	Number  int
	Header  string
	Content string
}

// Chapter is a group of pages under the same header.
type Chapter struct {
	Title string
	Pages []Page
}

// Book holds all parsed chapters.
type Book struct {
	Chapters []Chapter
	Title    string
	// SourceDir is the directory that relative [IMAGE: ...] paths resolve
	// against (the project root, i.e. the parent of ocr/ or el/).
	SourceDir string
}

// imageAsset is an image referenced by an [IMAGE: ...] tag, resolved on disk
// and destined for OEBPS/images/.
type imageAsset struct {
	Src   string // path exactly as written in the tag
	Full  string // resolved path on disk
	Name  string // file name inside OEBPS/images
	Href  string // path relative to OEBPS, e.g. "images/001.png"
	Media string // media type for the OPF manifest
}

// imageMediaType maps an image extension to the media type used in the OPF
// manifest.
func imageMediaType(ext string) string {
	switch strings.ToLower(ext) {
	case ".jpg", ".jpeg":
		return "image/jpeg"
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".gif":
		return "image/gif"
	case ".tif", ".tiff":
		return "image/tiff"
	case ".svg":
		return "image/svg+xml"
	}
	return ""
}

// collectImages finds every [IMAGE: ...] tag at the start of a line (the form
// renderContent understands), resolves it against the book's SourceDir and
// assigns it a name inside OEBPS/images. Images that cannot be found, or whose
// type is unsupported, are reported and left out.
func collectImages(book Book) ([]imageAsset, map[string]string) {
	var assets []imageAsset
	hrefFor := make(map[string]string)
	for _, ch := range book.Chapters {
		for _, page := range ch.Pages {
			for _, line := range strings.Split(page.Content, "\n") {
				trimmed := strings.TrimSpace(line)
				if !strings.HasPrefix(trimmed, "[IMAGE:") {
					continue
				}
				src := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "[IMAGE:"), "]"))
				if src == "" {
					continue
				}
				if _, seen := hrefFor[src]; seen {
					continue
				}
				full := src
				if !filepath.IsAbs(full) {
					full = filepath.Join(book.SourceDir, src)
				}
				media := imageMediaType(filepath.Ext(full))
				if media == "" {
					fmt.Fprintf(os.Stderr, "Warning: [IMAGE: %s] has an unsupported image type; skipping\n", src)
					continue
				}
				if fi, err := os.Stat(full); err != nil || fi.IsDir() {
					fmt.Fprintf(os.Stderr, "Warning: [IMAGE: %s] not found (%s); skipping\n", src, full)
					continue
				}
				name := fmt.Sprintf("%03d%s", len(assets)+1, strings.ToLower(filepath.Ext(full)))
				asset := imageAsset{Src: src, Full: full, Name: name, Href: "images/" + name, Media: media}
				assets = append(assets, asset)
				hrefFor[src] = asset.Href
			}
		}
	}
	return assets, hrefFor
}

// copyImages copies the collected images into OEBPS/images/.
func copyImages(oebpsDir string, assets []imageAsset) error {
	if len(assets) == 0 {
		return nil
	}
	imgDir := filepath.Join(oebpsDir, "images")
	if err := os.MkdirAll(imgDir, 0755); err != nil {
		return err
	}
	for _, a := range assets {
		data, err := os.ReadFile(a.Full)
		if err != nil {
			return fmt.Errorf("read image %s: %w", a.Src, err)
		}
		if err := os.WriteFile(filepath.Join(imgDir, a.Name), data, 0644); err != nil {
			return err
		}
	}
	return nil
}

const Version = "0.4.0"

func main() {
	// Check for --version flag
	for _, arg := range os.Args[1:] {
		if arg == "--version" || arg == "-v" {
			fmt.Printf("to-sympan-epub v%s\n", Version)
			return
		}
	}

	repoRoot := "."
	if len(os.Args) > 1 {
		repoRoot = os.Args[1]
	}
	repoRoot, _ = filepath.Abs(repoRoot)

	distDir := filepath.Join(repoRoot, "dist")
	os.MkdirAll(distDir, 0755)

	// Parse polytonic OCR
	fmt.Printf("ΤΟ ΣΥΜΠΑΝ EPUB Generator v%s\n\n", Version)

	ocrBook := parseOCRFiles(filepath.Join(repoRoot, "ocr"), true)
	ocrBook.Title = "ΤΟ ΣΥΜΠΑΝ — Πολυτονικό Κείμενο (1888)"

	// Parse modern Greek EL
	elBook := parseOCRFiles(filepath.Join(repoRoot, "el"), false)
	elBook.Title = "ΤΟ ΣΥΜΠΑΝ — Νεοελληνική Απόδοση"

	fmt.Println("=== OCR (πολυτονικό) ===")
	for _, ch := range ocrBook.Chapters {
		fmt.Printf("  Κεφάλαιο: %s (%d σελίδες)\n", ch.Title, len(ch.Pages))
	}
	fmt.Printf("  Σύνολο σελίδων: %d\n", countPages(ocrBook))
	fmt.Println()

	fmt.Println("=== EL (νεοελληνικό) ===")
	for _, ch := range elBook.Chapters {
		fmt.Printf("  Κεφάλαιο: %s (%d σελίδες)\n", ch.Title, len(ch.Pages))
	}
	fmt.Printf("  Σύνολο σελίδων: %d\n", countPages(elBook))
	fmt.Println()

	// Generate polytonic EPUB
	fmt.Println("→ Generating polytonic EPUB...")
	err := generateEPUB(ocrBook, filepath.Join(distDir, "to-sympan-polytonic.epub"), "el", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR (polytonic): %v\n", err)
	} else {
		fmt.Println("  OK")
	}

	// Generate neohellenic EPUB
	fmt.Println("→ Generating neohellenic EPUB...")
	err = generateEPUB(elBook, filepath.Join(distDir, "to-sympan-neohellenic.epub"), "el", false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR (neohellenic): %v\n", err)
	} else {
		fmt.Println("  OK")
	}

	// Generate dual EPUB
	fmt.Println("→ Generating dual EPUB...")
	err = generateDualEPUB(ocrBook, elBook, filepath.Join(distDir, "to-sympan-dual.epub"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "ERROR (dual): %v\n", err)
	} else {
		fmt.Println("  OK")
	}

	fmt.Println("\nDone! EPUB files in:", distDir)
}

func countPages(b Book) int {
	n := 0
	for _, ch := range b.Chapters {
		n += len(ch.Pages)
	}
	return n
}

// parseOCRFiles reads all .txt files from dir and returns a Book.
func parseOCRFiles(dir string, isOCR bool) Book {
	entries, err := os.ReadDir(dir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Warning: cannot read %s: %v\n", dir, err)
		return Book{}
	}

	var pages []Page
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".txt") {
			continue
		}
		if strings.Contains(e.Name(), "-old") {
			continue
		}

		base := strings.TrimSuffix(e.Name(), ".txt")
		if strings.HasSuffix(base, "g") {
			continue
		}
		num, err := strconv.Atoi(base)
		if err != nil {
			continue
		}

		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil || len(bytes.TrimSpace(data)) == 0 {
			continue
		}

		page := parsePage(string(data), num, isOCR)
		pages = append(pages, page)
	}

	sort.Slice(pages, func(i, j int) bool {
		return pages[i].Number < pages[j].Number
	})

	chapters := groupChapters(pages)
	return Book{Chapters: chapters, SourceDir: filepath.Dir(dir)}
}

func parsePage(text string, num int, isOCR bool) Page {
	lines := strings.Split(text, "\n")
	page := Page{Number: num, Header: ""}

	pageTag := "[PAGE:"
	headerTag := "[HEADER:"
	if !isOCR {
		pageTag = "[ΣΕΛΙΔΑ:"
		headerTag = "[ΚΕΦΑΛΙΔΑ:"
	}

	var contentLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, pageTag) {
			continue
		}
		if strings.HasPrefix(trimmed, headerTag) {
			h := trimmed[len(headerTag):]
			if idx := strings.LastIndex(h, "]"); idx >= 0 {
				h = h[:idx]
			}
			page.Header = strings.TrimSpace(h)
			continue
		}
		contentLines = append(contentLines, line)
	}

	page.Content = strings.Join(contentLines, "\n")
	return page
}

func groupChapters(pages []Page) []Chapter {
	var chapters []Chapter
	var current *Chapter
	runningHeader := "ΤΟ ΣΥΜΠΑΝ"

	for _, p := range pages {
		header := p.Header
		if header == "" {
			if current != nil {
				current.Pages = append(current.Pages, p)
			} else {
				ch := Chapter{Title: "Προμετωπίδα", Pages: []Page{p}}
				chapters = append(chapters, ch)
				current = &chapters[len(chapters)-1]
			}
			continue
		}

		// Running header — merge into current chapter
		if header == runningHeader && len(chapters) > 0 {
			current.Pages = append(current.Pages, p)
			continue
		}

		// Same header as previous chapter → merge (handles multi-page sections)
		if current != nil && current.Title == header {
			current.Pages = append(current.Pages, p)
			continue
		}

		// New chapter
		ch := Chapter{Title: header, Pages: []Page{p}}
		chapters = append(chapters, ch)
		current = &chapters[len(chapters)-1]
	}

	return chapters
}

// ─── EPUB generation ────────────────────────────────────────────────

func generateEPUB(book Book, outputPath, lang string, isDual bool) error {
	tmpDir, err := os.MkdirTemp("", "epub-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	// Create directory structure
	oebpsDir := filepath.Join(tmpDir, "OEBPS")
	os.MkdirAll(oebpsDir, 0755)
	os.MkdirAll(filepath.Join(tmpDir, "META-INF"), 0755)

	// 1. mimetype
	if err := os.WriteFile(filepath.Join(tmpDir, "mimetype"), []byte("application/epub+zip"), 0644); err != nil {
		return err
	}

	// 2. container.xml
	if err := writeContainer(filepath.Join(tmpDir, "META-INF", "container.xml")); err != nil {
		return err
	}

	// 3. CSS
	if err := writeCSS(oebpsDir, isDual); err != nil {
		return err
	}

	// 3b. Images referenced by [IMAGE: ...] tags
	images, imageHrefs := collectImages(book)
	if err := copyImages(oebpsDir, images); err != nil {
		return err
	}

	// 4. NCX
	if err := writeNCX(oebpsDir, book); err != nil {
		return err
	}

	// 5. Nav
	if err := writeNav(oebpsDir, book, lang); err != nil {
		return err
	}

	// 6. OPF
	if err := writeOPF(oebpsDir, book, lang, images); err != nil {
		return err
	}

	// 7. Chapters
	for i, ch := range book.Chapters {
		if err := writeChapter(oebpsDir, i, ch, lang, isDual, imageHrefs); err != nil {
			return err
		}
	}

	// 8. ZIP it up as EPUB
	return zipDirectory(tmpDir, outputPath)
}

func generateDualEPUB(ocrBook, elBook Book, outputPath string) error {
	ocrMap := make(map[string]Chapter)
	for _, ch := range ocrBook.Chapters {
		ocrMap[ch.Title] = ch
	}

	var dualChapters []Chapter

	// Match EL chapters with OCR chapters
	for _, elCh := range elBook.Chapters {
		ocCh, ok := ocrMap[elCh.Title]
		if !ok {
			dualChapters = append(dualChapters, elCh)
			continue
		}

		var pages []Page
		maxPages := len(ocCh.Pages)
		if len(elCh.Pages) > maxPages {
			maxPages = len(elCh.Pages)
		}

		for i := 0; i < maxPages; i++ {
			var ocrContent, elContent string
			if i < len(ocCh.Pages) {
				ocrContent = ocCh.Pages[i].Content
			}
			if i < len(elCh.Pages) {
				elContent = elCh.Pages[i].Content
			}
			pageNum := 0
			if i < len(ocCh.Pages) {
				pageNum = ocCh.Pages[i].Number
			} else if i < len(elCh.Pages) {
				pageNum = elCh.Pages[i].Number
			}
			pages = append(pages, Page{
				Number:  pageNum,
				Header:  elCh.Title,
				Content: ocrContent + "\n%%DUAL_SEPARATOR%%\n" + elContent,
			})
		}

		dualChapters = append(dualChapters, Chapter{Title: elCh.Title, Pages: pages})
	}

	// Add OCR-only chapters with no EL translation
	for _, ocCh := range ocrBook.Chapters {
		if _, ok := ocrMap[ocCh.Title]; !ok {
			dualChapters = append(dualChapters, ocCh)
		}
	}

	dualBook := Book{
		Chapters:  dualChapters,
		Title:     "ΤΟ ΣΥΜΠΑΝ — Δίγλωσση Έκδοση (Πολυτονικό & Νεοελληνικά)",
		SourceDir: ocrBook.SourceDir,
	}

	return generateEPUB(dualBook, outputPath, "el", true)
}

// ─── Write helpers ──────────────────────────────────────────────────

func writeContainer(path string) error {
	content := `<?xml version="1.0" encoding="UTF-8"?>
<container version="1.0" xmlns="urn:oasis:names:tc:opendocument:xmlns:container">
  <rootfiles>
    <rootfile full-path="OEBPS/content.opf" media-type="application/oebps-package+xml"/>
  </rootfiles>
</container>`
	return os.WriteFile(path, []byte(content), 0644)
}

func writeCSS(dir string, isDual bool) error {
	var css string
	if isDual {
		css = `@charset "UTF-8";

body {
  font-family: "Noto Serif", "Georgia", "Times New Roman", serif;
  line-height: 1.7;
  margin: 1em;
  color: #111;
}

h1 {
  text-align: center;
  margin: 2em 0 1em;
  font-size: 1.4em;
  page-break-before: always;
}

.dual-page {
  display: flex;
  flex-wrap: wrap;
  gap: 1em;
}

.dual-polytonic {
  flex: 1 1 45%;
  min-width: 14em;
  border-right: 1px solid #ccc;
  padding-right: 1em;
}

.dual-modern {
  flex: 1 1 45%;
  min-width: 14em;
  padding-left: 0.5em;
}

@media (max-width: 40em) {
  .dual-polytonic, .dual-modern {
    flex: 1 1 100%;
    border-right: none;
    padding-right: 0;
    padding-left: 0;
  }
  .dual-polytonic {
    border-bottom: 1px solid #ccc;
    padding-bottom: 0.5em;
    margin-bottom: 0.5em;
  }
}

.page-number {
  text-align: center;
  font-size: 0.8em;
  color: #888;
  margin: 0.3em 0;
}

p {
  text-indent: 1.5em;
  margin: 0.3em 0;
  text-align: justify;
}

p.no-indent {
  text-indent: 0;
}

.verse {
  font-style: italic;
  margin-left: 1.5em;
}

.footnote {
  font-size: 0.85em;
  margin-top: 0.5em;
  border-top: 1px solid #ccc;
  padding-top: 0.3em;
}

hr {
  border: none;
  border-top: 1px solid #999;
  margin: 1em 0;
}
`
	} else {
		css = `@charset "UTF-8";

body {
  font-family: "Noto Serif", "Georgia", "Times New Roman", serif;
  line-height: 1.7;
  margin: 1em;
  color: #111;
}

h1 {
  text-align: center;
  margin: 2em 0 1em;
  font-size: 1.4em;
  page-break-before: always;
}

.page-number {
  text-align: center;
  font-size: 0.8em;
  color: #888;
  margin: 0.3em 0;
}

p {
  text-indent: 1.5em;
  margin: 0.3em 0;
  text-align: justify;
}

p.no-indent {
  text-indent: 0;
}

.verse {
  font-style: italic;
  margin-left: 1.5em;
}

.footnote {
  font-size: 0.85em;
  margin-top: 0.5em;
  border-top: 1px solid #ccc;
  padding-top: 0.3em;
}

hr {
  border: none;
  border-top: 1px solid #999;
  margin: 1em 0;
}
`
	}
	return os.WriteFile(filepath.Join(dir, "style.css"), []byte(css), 0644)
}

func writeNCX(dir string, book Book) error {
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE ncx PUBLIC "-//NISO//DTD ncx 2005-1//EN" "http://www.daisy.org/z3986/2005/ncx-2005-1.dtd">
<ncx xmlns="http://www.daisy.org/z3986/2005/ncx/" version="2005-1">
  <head>
    <meta name="dtb:uid" content="to-sympan-1888"/>
    <meta name="dtb:depth" content="1"/>
    <meta name="dtb:totalPageCount" content="0"/>
    <meta name="dtb:maxPageNumber" content="0"/>
  </head>
  <docTitle><text>`)
	buf.WriteString(escapeXML(book.Title))
	buf.WriteString(`</text></docTitle>
  <navMap>
`)
	for i, ch := range book.Chapters {
		fmt.Fprintf(&buf, `    <navPoint id="navpoint-%d" playOrder="%d">
      <navLabel><text>%s</text></navLabel>
      <content src="chapter-%04d.xhtml"/>
    </navPoint>
`, i+1, i+1, escapeXML(ch.Title), i+1)
	}
	buf.WriteString(`  </navMap>
</ncx>`)

	return os.WriteFile(filepath.Join(dir, "toc.ncx"), buf.Bytes(), 0644)
}

func writeNav(dir string, book Book, lang string) error {
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" xmlns:epub="http://www.idpf.org/2007/ops" lang="`)
	buf.WriteString(lang)
	buf.WriteString(`">
<head>
  <meta charset="UTF-8"/>
  <title>`)
	buf.WriteString(escapeXML(book.Title))
	buf.WriteString(`</title>
</head>
<body>
  <nav epub:type="toc" id="toc">
    <h1>Πίνακας Περιεχομένων</h1>
    <ol>
`)
	for i, ch := range book.Chapters {
		fmt.Fprintf(&buf, `      <li><a href="chapter-%04d.xhtml">%s</a></li>
`, i+1, escapeXML(ch.Title))
	}
	buf.WriteString(`    </ol>
  </nav>
</body>
</html>`)

	return os.WriteFile(filepath.Join(dir, "nav.xhtml"), buf.Bytes(), 0644)
}

func writeOPF(dir string, book Book, lang string, images []imageAsset) error {
	var buf bytes.Buffer
	buf.WriteString(`<?xml version="1.0" encoding="UTF-8"?>
<package xmlns="http://www.idpf.org/2007/opf" version="3.0" unique-identifier="book-id">
  <metadata xmlns:dc="http://purl.org/dc/elements/1.1/">
    <dc:identifier id="book-id">to-sympan-1888</dc:identifier>
    <dc:title>`)
	buf.WriteString(escapeXML(book.Title))
	buf.WriteString(`</dc:title>
    <dc:language>`)
	buf.WriteString(lang)
	buf.WriteString(`</dc:language>
    <dc:creator>Ν. Ζαλούχος</dc:creator>
    <dc:publisher>Σμύρνη, 1888</dc:publisher>
    <meta property="dcterms:modified">2025-01-01T00:00:00Z</meta>
  </metadata>
  <manifest>
    <item id="ncx" href="toc.ncx" media-type="application/x-dtbncx+xml"/>
    <item id="nav" href="nav.xhtml" media-type="application/xhtml+xml"/>
    <item id="css" href="style.css" media-type="text/css"/>
`)
	for i := range book.Chapters {
		fmt.Fprintf(&buf, `    <item id="chapter-%04d" href="chapter-%04d.xhtml" media-type="application/xhtml+xml"/>
`, i+1, i+1)
	}
	for _, img := range images {
		fmt.Fprintf(&buf, `    <item id="img-%s" href="%s" media-type="%s"/>
`, strings.TrimSuffix(img.Name, filepath.Ext(img.Name)), escapeXML(img.Href), img.Media)
	}
	buf.WriteString(`  </manifest>
  <spine>
    <itemref idref="nav"/>
`)
	for i := range book.Chapters {
		fmt.Fprintf(&buf, `    <itemref idref="chapter-%04d"/>
`, i+1)
	}
	buf.WriteString(`  </spine>
</package>`)

	return os.WriteFile(filepath.Join(dir, "content.opf"), buf.Bytes(), 0644)
}

func writeChapter(dir string, idx int, ch Chapter, lang string, isDual bool, images map[string]string) error {
	var bodyBuf strings.Builder

	bodyBuf.WriteString(fmt.Sprintf("<h1>%s</h1>\n", html.EscapeString(ch.Title)))

	for _, page := range ch.Pages {
		bodyBuf.WriteString(fmt.Sprintf(`<p class="page-number no-indent">— Σελ. %d —</p>`+"\n", page.Number))

		if isDual {
			parts := strings.SplitN(page.Content, "%%DUAL_SEPARATOR%%", 2)
			polyContent := ""
			modContent := ""
			if len(parts) > 0 {
				polyContent = strings.TrimSpace(parts[0])
			}
			if len(parts) > 1 {
				modContent = strings.TrimSpace(parts[1])
			}

			bodyBuf.WriteString(`<div class="dual-page">` + "\n")
			bodyBuf.WriteString(`<div class="dual-polytonic">` + "\n")
			bodyBuf.WriteString(renderContent(polyContent, images))
			bodyBuf.WriteString(`</div>` + "\n")
			bodyBuf.WriteString(`<div class="dual-modern">` + "\n")
			bodyBuf.WriteString(renderContent(modContent, images))
			bodyBuf.WriteString(`</div>` + "\n")
			bodyBuf.WriteString(`</div>` + "\n")
		} else {
			bodyBuf.WriteString(renderContent(page.Content, images))
		}
	}

	fullHTML := fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE html>
<html xmlns="http://www.w3.org/1999/xhtml" lang="%s">
<head>
  <meta charset="UTF-8"/>
  <link rel="stylesheet" type="text/css" href="style.css"/>
  <title>%s</title>
</head>
<body>
%s
</body>
</html>`, lang, html.EscapeString(ch.Title), bodyBuf.String())

	fname := fmt.Sprintf("chapter-%04d.xhtml", idx+1)
	return os.WriteFile(filepath.Join(dir, fname), []byte(fullHTML), 0644)
}

// ─── ZIP ────────────────────────────────────────────────────────────

func zipDirectory(srcDir, dstPath string) error {
	// Remove existing target if any
	os.Remove(dstPath)

	// Use the system `zip` command to create a proper EPUB
	// EPUB requires: mimetype first, stored (not compressed), then rest
	// We do a two-step zip: first mimetype stored, then the rest
	cmd := exec.Command("zip", "-0", "-X", dstPath, "mimetype")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zip mimetype: %v - %s", err, string(out))
	}

	// Add everything else with compression
	cmd = exec.Command("zip", "-r", "-9", dstPath, "META-INF", "OEBPS")
	cmd.Dir = srcDir
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("zip rest: %v - %s", err, string(out))
	}

	return nil
}

// ─── Content rendering ──────────────────────────────────────────────

var (
	inlineCmRe = regexp.MustCompile(`\[([^:]+?):\s*([^\]]+?)\]`)
)

func renderContent(content string, images map[string]string) string {
	// Remove remaining PAGE/HEADER tags
	content = regexp.MustCompile(`(?m)^\[(?:PAGE|ΣΕΛΙΔΑ):\s*\d+\]\s*$`).ReplaceAllString(content, "")
	content = regexp.MustCompile(`(?m)^\[(?:HEADER|ΚΕΦΑΛΙΔΑ):\s*[^\]]+\]\s*$`).ReplaceAllString(content, "")
	// Remove metadata tags like [[SPECIAL_PAGE: ...]] and [[NOTE: ...]]
	content = regexp.MustCompile(`(?m)^\[\[[^\]]+\]\]\s*$`).ReplaceAllString(content, "")

	lines := strings.Split(content, "\n")
	var outLines []string

	inFootnote := false
	var footnoteLines []string

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "[COMMENT:") {
			text := strings.TrimPrefix(trimmed, "[COMMENT:")
			text = strings.TrimSuffix(text, "]")
			text = strings.TrimSpace(text)
			outLines = append(outLines, fmt.Sprintf(`<p class="no-indent commentary">%s</p>`, html.EscapeString(text)))
			continue
		}

		if trimmed == "---" || trimmed == "-------" || trimmed == "----" {
			inFootnote = true
			outLines = append(outLines, `<hr/>`)
			continue
		}

		if inFootnote {
			footnoteLines = append(footnoteLines, trimmed)
			continue
		}

		if strings.HasPrefix(trimmed, "[IMAGE:") {
			src := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(trimmed, "[IMAGE:"), "]"))
			if href, ok := images[src]; ok {
				outLines = append(outLines, fmt.Sprintf(`<figure><img src="%s" alt="%s"/></figure>`, html.EscapeString(href), html.EscapeString(src)))
			}
			continue
		}

		if strings.HasPrefix(trimmed, "[CAPTION:") {
			cap := strings.TrimPrefix(trimmed, "[CAPTION:")
			cap = strings.TrimSuffix(cap, "]")
			cap = strings.TrimSpace(cap)
			outLines = append(outLines, fmt.Sprintf(`<figcaption>%s</figcaption>`, html.EscapeString(cap)))
			continue
		}

		// Remove inline comments [word: explanation]
		line = inlineCmRe.ReplaceAllString(line, "")

		if strings.HasPrefix(trimmed, ">") {
			verseText := strings.TrimPrefix(trimmed, ">")
			verseText = strings.TrimSpace(verseText)
			verseText = renderInline(verseText)
			outLines = append(outLines, fmt.Sprintf(`<p class="verse">%s</p>`, verseText))
			continue
		}

		// Regular paragraph: render inline (bold + HTML escape)
		processed := renderInline(line)
		outLines = append(outLines, fmt.Sprintf(`<p>%s</p>`, processed))
	}

	if len(footnoteLines) > 0 {
		var fnBuf strings.Builder
		fnBuf.WriteString(`<div class="footnote">`)
		for _, fl := range footnoteLines {
			processed := fl
			if strings.HasPrefix(processed, "[IMAGE:") {
				src := strings.TrimSpace(strings.TrimSuffix(strings.TrimPrefix(processed, "[IMAGE:"), "]"))
				if href, ok := images[src]; ok {
					fnBuf.WriteString(fmt.Sprintf(`<p class="no-indent"><img src="%s" alt="%s"/></p>`, html.EscapeString(href), html.EscapeString(src)))
				}
				continue
			}
			if strings.HasPrefix(processed, "[CAPTION:") {
				cap := strings.TrimPrefix(processed, "[CAPTION:")
				cap = strings.TrimSuffix(cap, "]")
				cap = strings.TrimSpace(cap)
				fnBuf.WriteString(fmt.Sprintf(`<p class="no-indent">%s</p>`, html.EscapeString(cap)))
				continue
			}
			processed = renderInline(processed)
			fnBuf.WriteString(fmt.Sprintf(`<p class="no-indent">%s</p>`, processed))
		}
		fnBuf.WriteString(`</div>`)
		outLines = append(outLines, fnBuf.String())
	}

	return strings.Join(outLines, "\n")
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	return s
}

// renderInline HTML-escapes text and converts bold markers (**text** and *text*) to <strong> tags.
func renderInline(text string) string {
	// Step 1: replace **text** with ##DBLD##text##DBLE## (double-asterisk markers)
	text = regexp.MustCompile(`\*\*([^*]+)\*\*`).ReplaceAllString(text, "\x00DBLD\x00$1\x00DBLE\x00")
	// Step 2: replace *text* with ##SNGL##text##SNGE## (single-asterisk markers)
	text = regexp.MustCompile(`\*([^*]+)\*`).ReplaceAllString(text, "\x00SNGL\x00$1\x00SNGE\x00")

	// Step 3: HTML-escape everything (markers survive since they're control chars)
	text = html.EscapeString(text)

	// Step 4: replace markers with <strong>/</strong>
	text = strings.ReplaceAll(text, "\x00DBLD\x00", "<strong>")
	text = strings.ReplaceAll(text, "\x00DBLE\x00", "</strong>")
	text = strings.ReplaceAll(text, "\x00SNGL\x00", "<strong>")
	text = strings.ReplaceAll(text, "\x00SNGE\x00", "</strong>")

	return text
}
