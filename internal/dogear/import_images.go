package dogear

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"strings"
	"unicode"
)

func extractEmbeddedImages(lines []string) ([]string, []embeddedImage, []DocumentImportWarning, error) {
	content := strings.Join(lines, "\n")
	var images []embeddedImage
	var warnings []DocumentImportWarning
	cleaned := embeddedImageRE.ReplaceAllStringFunc(content, preserveImageLineBreaks)
	for _, indexes := range anyEmbeddedImageRE.FindAllStringSubmatchIndex(content, -1) {
		mediaType := strings.ToLower(content[indexes[4]:indexes[5]])
		if mediaType == "image/png" || mediaType == "image/jpeg" || mediaType == "image/jpg" || mediaType == "image/gif" || mediaType == "image/webp" {
			continue
		}
		line := strings.Count(content[:indexes[0]], "\n") + 1
		warnings = append(warnings, DocumentImportWarning{Code: "unsupported_image", Message: fmt.Sprintf("An embedded %s image was skipped because its format is unsupported.", mediaType), Line: line})
	}
	cleaned = anyEmbeddedImageRE.ReplaceAllStringFunc(cleaned, preserveImageLineBreaks)
	for _, indexes := range embeddedImageRE.FindAllStringSubmatchIndex(content, -1) {
		line := strings.Count(content[:indexes[0]], "\n") + 1
		payload := strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, content[indexes[6]:indexes[7]])
		data, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("invalid embedded image on line %d: %w", line, err)
		}
		if len(data) > maxEmbeddedImageBytes {
			return nil, nil, nil, fmt.Errorf("embedded image on line %d exceeds %d bytes", line, maxEmbeddedImageBytes)
		}
		mediaType := strings.ToLower(content[indexes[4]:indexes[5]])
		if mediaType == "image/jpg" {
			mediaType = "image/jpeg"
		}
		if detected := http.DetectContentType(data); detected != mediaType {
			return nil, nil, nil, fmt.Errorf("embedded image on line %d declares %s but contains %s", line, mediaType, detected)
		}
		alt := strings.TrimSpace(content[indexes[2]:indexes[3]])
		if alt == "" {
			alt = "Manual image"
			warnings = append(warnings, DocumentImportWarning{Code: "missing_image_alt", Message: "An embedded image had no alternative text; the fallback “Manual image” was used.", Line: line})
		}
		images = append(images, embeddedImage{line: line, alt: alt, mediaType: mediaType, data: data})
	}
	return strings.Split(cleaned, "\n"), images, warnings, nil
}

func preserveImageLineBreaks(value string) string {
	return strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' {
			return r
		}
		return ' '
	}, value)
}

func attachImagesToChunks(documentID string, embedded []embeddedImage, sections []section, chunks []Chunk) ([]DocumentImage, []DocumentImportWarning) {
	images := make([]DocumentImage, 0, len(embedded))
	var warnings []DocumentImportWarning
	for i, image := range embedded {
		var sectionPath string
		for _, sec := range sections {
			if image.line >= sec.startLine && image.line <= sec.endLine {
				sectionPath = sec.headingPath
				break
			}
		}
		bestOrdinal := 0
		bestDistance := int(^uint(0) >> 1)
		for _, chunk := range chunks {
			if sectionPath != "" && chunk.HeadingPath != sectionPath {
				continue
			}
			distance := 0
			switch {
			case image.line < chunk.StartLine:
				distance = chunk.StartLine - image.line
			case image.line > chunk.EndLine:
				distance = image.line - chunk.EndLine
			}
			if distance < bestDistance {
				bestDistance = distance
				bestOrdinal = chunk.Ordinal
			}
		}
		if bestOrdinal == 0 {
			warnings = append(warnings, DocumentImportWarning{Code: "unattached_image", Message: "An embedded image could not be attached to searchable content.", Line: image.line})
			continue
		}
		images = append(images, DocumentImage{DocumentID: documentID, ChunkOrdinal: bestOrdinal, Ordinal: i + 1, Alt: image.alt, MediaType: image.mediaType, Data: image.data, ContentHash: hashBytes(image.data)})
	}
	return images, warnings
}
