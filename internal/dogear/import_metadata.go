package dogear

import (
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"unicode"
)

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

func hashString(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
func hashBytes(value []byte) string { sum := sha256.Sum256(value); return hex.EncodeToString(sum[:]) }

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
