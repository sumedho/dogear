package dogear

import (
	"bufio"
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type ImportMetadata struct {
	ID      string
	Title   string
	Brand   string
	Model   string
	Version string
	Tags    []string
}

type ImportResult struct {
	Documents int                     `json:"documents"`
	Chunks    int                     `json:"chunks"`
	Images    int                     `json:"images"`
	Warnings  []DocumentImportWarning `json:"warnings,omitempty"`
}

const maxEmbeddedImageBytes = 25 << 20

type DocumentImage struct {
	ID           int64
	DocumentID   string
	ChunkID      int64
	ChunkOrdinal int
	Ordinal      int
	Alt          string
	MediaType    string
	Data         []byte
	ContentHash  string
}

type embeddedImage struct {
	line      int
	alt       string
	mediaType string
	data      []byte
}

type section struct {
	heading      string
	headingPath  string
	headingLevel int
	pageNumber   sql.NullInt64
	startLine    int
	endLine      int
	lines        []string
	lineNumbers  []int
}

var (
	headingRE          = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	tocRowRE           = regexp.MustCompile(`^\|(.+?)\|\s*\.?\s*\.?\s*([0-9]{1,4})\s*\|`)
	pageMarkerRE       = regexp.MustCompile(`(?i)^<!--\s*(?:dogear:page=|page:\s*)([0-9]{1,5})\s*-->$`)
	embeddedImageRE    = regexp.MustCompile(`(?is)!\[([^]]*)\]\(\s*<?data:(image/(?:png|jpe?g|gif|webp));base64,([a-z0-9+/=\r\n\t ]+)>?(?:\s+["'][^"']*["'])?\s*\)`)
	anyEmbeddedImageRE = regexp.MustCompile(`(?is)!\[([^]]*)\]\(\s*<?data:(image/[a-z0-9.+-]+);base64,([a-z0-9+/=\r\n\t ]+)>?(?:\s+["'][^"']*["'])?\s*\)`)
)

func ImportPath(ctx context.Context, store *Store, path string, meta ImportMetadata, replace bool) (ImportResult, error) {
	files, err := markdownFiles(path)
	if err != nil {
		return ImportResult{}, err
	}
	if len(files) == 0 {
		return ImportResult{}, fmt.Errorf("no markdown files found under %s", path)
	}
	if len(files) > 1 && meta.ID != "" {
		return ImportResult{}, fmt.Errorf("--id can only be used when importing one file")
	}

	var result ImportResult
	for _, file := range files {
		raw, err := os.ReadFile(file)
		if err != nil {
			return ImportResult{}, err
		}
		doc, chunks, images, err := parseMarkdown(file, raw, meta)
		if err != nil {
			return ImportResult{}, err
		}
		if err := store.UpsertDocumentWithImages(ctx, doc, chunks, images, replace); err != nil {
			return ImportResult{}, err
		}
		result.Documents++
		result.Chunks += len(chunks)
		result.Images += len(images)
		result.Warnings = append(result.Warnings, doc.ImportWarnings...)
	}
	if _, err := store.RebuildIndex(ctx); err != nil {
		return ImportResult{}, err
	}
	return result, nil
}

func ImportMarkdown(ctx context.Context, store *Store, sourceName string, content []byte, meta ImportMetadata, replace bool) (ImportResult, error) {
	if !isMarkdown(sourceName) {
		return ImportResult{}, fmt.Errorf("%s is not a markdown file", sourceName)
	}
	doc, chunks, images, err := parseMarkdown(sourceName, content, meta)
	if err != nil {
		return ImportResult{}, err
	}
	if err := store.UpsertDocumentWithImages(ctx, doc, chunks, images, replace); err != nil {
		return ImportResult{}, err
	}
	if _, err := store.RebuildIndex(ctx); err != nil {
		return ImportResult{}, err
	}
	return ImportResult{Documents: 1, Chunks: len(chunks), Images: len(images), Warnings: doc.ImportWarnings}, nil
}

func markdownFiles(path string) ([]string, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if isMarkdown(path) {
			return []string{path}, nil
		}
		return nil, fmt.Errorf("%s is not a markdown file", path)
	}

	var files []string
	err = filepath.WalkDir(path, func(p string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		if isMarkdown(p) {
			files = append(files, p)
		}
		return nil
	})
	sort.Strings(files)
	return files, err
}

func isMarkdown(path string) bool {
	ext := strings.ToLower(filepath.Ext(path))
	return ext == ".md" || ext == ".markdown"
}

func parseMarkdownFile(path string, meta ImportMetadata) (Document, []Chunk, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Document{}, nil, err
	}
	doc, chunks, _, err := parseMarkdown(path, raw, meta)
	return doc, chunks, err
}

func parseMarkdown(path string, raw []byte, meta ImportMetadata) (Document, []Chunk, []DocumentImage, error) {
	lines := scanLines(string(raw))
	cleanedLines, embedded, warnings, err := extractEmbeddedImages(lines)
	if err != nil {
		return Document{}, nil, nil, err
	}
	lines = cleanedLines
	title := firstHeading(lines)
	if meta.Title != "" {
		title = meta.Title
	}
	if title == "" {
		title = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
	}

	docID := meta.ID
	if docID == "" {
		docID = Slug(title)
	}
	if docID == "" {
		docID = Slug(strings.TrimSuffix(filepath.Base(path), filepath.Ext(path)))
	}

	doc := Document{
		ID:             docID,
		Title:          title,
		Brand:          meta.Brand,
		Model:          meta.Model,
		Version:        meta.Version,
		SourcePath:     path,
		SourceHash:     hashString(string(raw)),
		Tags:           cleanTags(meta.Tags),
		ImportWarnings: warnings,
	}
	inferBrandModel(&doc)

	pageMap := parseTOCPages(lines)
	sections := splitSections(lines, pageMap)
	chunks := chunksFromSections(doc.ID, sections)
	images, attachmentWarnings := attachImagesToChunks(doc.ID, embedded, sections, chunks)
	doc.ImportWarnings = append(doc.ImportWarnings, attachmentWarnings...)
	if len(chunks) == 0 {
		doc.ImportWarnings = append(doc.ImportWarnings, DocumentImportWarning{Code: "no_content_chunks", Message: "The import produced no searchable content chunks."})
	}
	return doc, chunks, images, nil
}

func scanLines(content string) []string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024), 128<<20)
	var lines []string
	for scanner.Scan() {
		lines = append(lines, scanner.Text())
	}
	return lines
}

func firstHeading(lines []string) string {
	for _, line := range lines {
		if match := headingRE.FindStringSubmatch(line); match != nil {
			return cleanHeading(match[2])
		}
	}
	return ""
}

func parseTOCPages(lines []string) map[string]int {
	pages := make(map[string]int)
	for _, line := range lines {
		match := tocRowRE.FindStringSubmatch(line)
		if match == nil {
			continue
		}
		page, err := strconv.Atoi(match[2])
		if err != nil {
			continue
		}
		label := strings.TrimSpace(match[1])
		label = strings.ReplaceAll(label, ".", " ")
		label = strings.Join(strings.Fields(label), " ")
		key := normalizeHeadingKey(label)
		if key != "" {
			pages[key] = page
		}
	}
	return pages
}

func splitSections(lines []string, pageMap map[string]int) []section {
	var sections []section
	var current *section
	var stack []string
	var currentMarkerPage sql.NullInt64

	flush := func(endLine int) {
		if current == nil {
			return
		}
		current.endLine = endLine
		sections = append(sections, *current)
	}

	for idx, line := range lines {
		lineNo := idx + 1
		if page, ok := parsePageMarker(line); ok {
			currentMarkerPage = sql.NullInt64{Int64: int64(page), Valid: true}
			continue
		}
		match := headingRE.FindStringSubmatch(line)
		if match == nil {
			if current != nil && !skipLine(line) {
				current.lines = append(current.lines, line)
				current.lineNumbers = append(current.lineNumbers, lineNo)
			}
			continue
		}

		flush(lineNo - 1)
		level := len(match[1])
		heading := cleanHeading(match[2])
		if level <= len(stack) {
			stack = stack[:level-1]
		}
		for len(stack) < level-1 {
			stack = append(stack, "")
		}
		stack = append(stack, heading)

		page := currentMarkerPage
		if !page.Valid {
			if n, ok := pageMap[normalizeHeadingKey(heading)]; ok {
				page = sql.NullInt64{Int64: int64(n), Valid: true}
			}
		}
		current = &section{
			heading:      heading,
			headingPath:  compactHeadingPath(stack),
			headingLevel: level,
			pageNumber:   page,
			startLine:    lineNo,
		}
	}
	flush(len(lines))

	if len(sections) == 0 {
		sections = append(sections, section{
			heading:      "Document",
			headingPath:  "Document",
			headingLevel: 1,
			startLine:    1,
			endLine:      len(lines),
			lines:        lines,
		})
	}
	return sections
}

func parsePageMarker(line string) (int, bool) {
	match := pageMarkerRE.FindStringSubmatch(strings.TrimSpace(line))
	if match == nil {
		return 0, false
	}
	page, err := strconv.Atoi(match[1])
	if err != nil || page <= 0 {
		return 0, false
	}
	return page, true
}

func chunksFromSections(docID string, sections []section) []Chunk {
	const maxChars = 3200
	const targetChars = 1800

	var chunks []Chunk
	for _, sec := range sections {
		if isNonContentSection(sec.heading) {
			continue
		}
		paragraphs := paragraphs(sec.lines, sec.lineNumbers)
		var buf []string
		startLine := 0
		flush := func(endLine int) {
			text := strings.TrimSpace(strings.Join(buf, "\n\n"))
			if text == "" {
				return
			}
			chunks = append(chunks, Chunk{
				DocumentID:   docID,
				Ordinal:      len(chunks) + 1,
				HeadingPath:  sec.headingPath,
				HeadingLevel: sec.headingLevel,
				PageNumber:   sec.pageNumber,
				StartLine:    startLine,
				EndLine:      endLine,
				Text:         text,
				TextHash:     hashString(text),
			})
			buf = nil
		}

		for _, para := range paragraphs {
			if len(buf) == 0 {
				startLine = para.startLine
			}
			if len(strings.Join(buf, "\n\n"))+len(para.text) > maxChars && len(buf) > 0 {
				flush(para.startLine - 1)
				startLine = para.startLine
			}
			buf = append(buf, para.text)
			if len(strings.Join(buf, "\n\n")) >= targetChars {
				flush(para.endLine)
				startLine = para.endLine + 1
			}
		}
		flush(sec.endLine)
	}
	return chunks
}

func isNonContentSection(heading string) bool {
	key := normalizeHeadingKey(heading)
	return key == "table of contents" ||
		key == "contents" ||
		key == "index" ||
		strings.HasPrefix(key, "index ") ||
		key == "credits" ||
		key == "contact information" ||
		strings.Contains(key, "credits and contact")
}

type paragraph struct {
	text      string
	startLine int
	endLine   int
}

func paragraphs(lines []string, lineNumbers []int) []paragraph {
	var result []paragraph
	var buf []string
	start := 0
	flush := func(end int) {
		text := strings.TrimSpace(strings.Join(buf, "\n"))
		if text != "" {
			result = append(result, paragraph{text: text, startLine: start, endLine: end})
		}
		buf = nil
		start = 0
	}
	for idx, line := range lines {
		lineNo := idx + 1
		if idx < len(lineNumbers) {
			lineNo = lineNumbers[idx]
		}
		if strings.TrimSpace(line) == "" {
			flush(lineNo)
			continue
		}
		if start == 0 {
			start = lineNo
		}
		buf = append(buf, line)
	}
	endLine := len(lines)
	if len(lineNumbers) > 0 {
		endLine = lineNumbers[len(lineNumbers)-1]
	}
	flush(endLine)
	return result
}

func skipLine(line string) bool {
	trimmed := strings.TrimSpace(line)
	return strings.HasPrefix(trimmed, "![Image](data:image/")
}

func cleanHeading(value string) string {
	value = strings.ReplaceAll(value, "&amp;", "&")
	return strings.Join(strings.Fields(value), " ")
}

func compactHeadingPath(parts []string) string {
	var compact []string
	for _, part := range parts {
		if part != "" {
			compact = append(compact, part)
		}
	}
	return strings.Join(compact, " > ")
}

func normalizeHeadingKey(value string) string {
	value = strings.ToLower(cleanHeading(value))
	value = strings.Map(func(r rune) rune {
		switch {
		case unicode.IsLetter(r), unicode.IsDigit(r):
			return r
		case unicode.IsSpace(r):
			return ' '
		default:
			return ' '
		}
	}, value)
	return strings.Join(strings.Fields(value), " ")
}
