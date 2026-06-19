package dogear

import (
	"bufio"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
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
	Documents int
	Chunks    int
}

type section struct {
	heading      string
	headingPath  string
	headingLevel int
	pageNumber   sql.NullInt64
	startLine    int
	endLine      int
	lines        []string
}

var (
	headingRE    = regexp.MustCompile(`^(#{1,6})\s+(.+?)\s*$`)
	tocRowRE     = regexp.MustCompile(`^\|(.+?)\|\s*\.?\s*\.?\s*([0-9]{1,4})\s*\|`)
	pageMarkerRE = regexp.MustCompile(`(?i)^<!--\s*(?:dogear:page=|page:\s*)([0-9]{1,5})\s*-->$`)
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
		doc, chunks, err := parseMarkdownFile(file, meta)
		if err != nil {
			return ImportResult{}, err
		}
		if err := store.UpsertDocument(ctx, doc, chunks, replace); err != nil {
			return ImportResult{}, err
		}
		result.Documents++
		result.Chunks += len(chunks)
	}
	if _, err := store.RebuildIndex(ctx); err != nil {
		return ImportResult{}, err
	}
	return result, nil
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
	lines := scanLines(string(raw))
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
		ID:         docID,
		Title:      title,
		Brand:      meta.Brand,
		Model:      meta.Model,
		Version:    meta.Version,
		SourcePath: path,
		SourceHash: hashString(string(raw)),
		Tags:       cleanTags(meta.Tags),
	}
	inferBrandModel(&doc)

	pageMap := parseTOCPages(lines)
	sections := splitSections(lines, pageMap)
	chunks := chunksFromSections(doc.ID, sections)
	return doc, chunks, nil
}

func scanLines(content string) []string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 1024), 1024*1024)
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
		paragraphs := paragraphs(sec.lines)
		var buf []string
		startLine := sec.startLine
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

func paragraphs(lines []string) []paragraph {
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
		if strings.TrimSpace(line) == "" {
			flush(lineNo - 1)
			continue
		}
		if start == 0 {
			start = lineNo
		}
		buf = append(buf, line)
	}
	flush(len(lines))
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

func Slug(value string) string {
	value = strings.ToLower(value)
	var b strings.Builder
	lastDash := false
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			lastDash = false
			continue
		}
		if !lastDash && b.Len() > 0 {
			b.WriteByte('-')
			lastDash = true
		}
	}
	return strings.Trim(b.String(), "-")
}

func NormalizeFTSQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" {
		return ""
	}
	if strings.Contains(query, `"`) {
		return query
	}
	var terms []string
	for _, field := range strings.Fields(query) {
		clean := strings.Map(func(r rune) rune {
			if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' {
				return r
			}
			return -1
		}, field)
		if clean != "" && !isQueryStopword(strings.ToLower(clean)) {
			terms = append(terms, clean)
		}
	}
	return strings.Join(terms, " AND ")
}

func isQueryStopword(term string) bool {
	switch term {
	case "a", "an", "and", "are", "do", "does", "for", "how", "i", "in", "is", "it", "of", "on", "or", "the", "to", "what", "where":
		return true
	default:
		return false
	}
}

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}

func cleanTags(tags []string) []string {
	seen := make(map[string]bool)
	clean := make([]string, 0, len(tags))
	for _, tag := range tags {
		tag = strings.TrimSpace(tag)
		if tag == "" || seen[tag] {
			continue
		}
		seen[tag] = true
		clean = append(clean, tag)
	}
	return clean
}

func inferBrandModel(doc *Document) {
	if doc.Title == "" {
		return
	}
	parts := strings.Fields(doc.Title)
	if doc.Brand == "" && len(parts) > 0 {
		doc.Brand = parts[0]
	}
	if doc.Model == "" && len(parts) > 1 {
		doc.Model = strings.Join(parts[:min(len(parts), 4)], " ")
	}
}
