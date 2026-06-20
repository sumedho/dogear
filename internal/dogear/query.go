package dogear

import (
	"strings"
	"unicode"
)

// NormalizeFTSQuery converts natural-language input into a conservative FTS5
// AND query while preserving explicitly quoted FTS expressions.
func NormalizeFTSQuery(query string) string {
	query = strings.TrimSpace(query)
	if query == "" || strings.Contains(query, `"`) {
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
