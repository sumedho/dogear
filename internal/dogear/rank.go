package dogear

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	QualityContent       = "content"
	QualityTOC           = "toc"
	QualityIndex         = "index"
	QualityReferenceOnly = "reference-only"
	QualityLowValue      = "low-value"
)

var wordRE = regexp.MustCompile(`[A-Za-z0-9]+`)

type rankableChunk struct {
	chunk RetrievedChunk
}

func qualityClass(heading, text string) string {
	key := normalizeHeadingKey(heading)
	switch {
	case key == "table of contents" || key == "contents":
		return QualityTOC
	case key == "index" || strings.HasPrefix(key, "index "):
		return QualityIndex
	case key == "credits" || key == "contact information" || strings.Contains(key, "credits and contact"):
		return QualityLowValue
	}

	words := tokenize(text)
	if len(words) == 0 {
		return QualityLowValue
	}
	if len(words) <= 6 && countNumbers(text) > 0 {
		return QualityReferenceOnly
	}
	if looksLikeReferenceList(text, words) {
		return QualityReferenceOnly
	}
	if len(words) < 8 {
		return QualityLowValue
	}
	return QualityContent
}

func rerankChunks(query string, candidates []RetrievedChunk, limit int) []RetrievedChunk {
	if limit <= 0 {
		limit = 8
	}
	queryTerms := uniqueTerms(tokenize(NormalizeFTSQuery(query)))
	ranked := make([]rankableChunk, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.Debug = rankCandidate(candidate, queryTerms)
		ranked = append(ranked, rankableChunk{chunk: candidate})
	}
	sort.SliceStable(ranked, func(i, j int) bool {
		if ranked[i].chunk.Debug.RerankScore == ranked[j].chunk.Debug.RerankScore {
			return ranked[i].chunk.Score < ranked[j].chunk.Score
		}
		return ranked[i].chunk.Debug.RerankScore > ranked[j].chunk.Debug.RerankScore
	})
	out := make([]RetrievedChunk, 0, min(limit, len(ranked)))
	for _, item := range ranked {
		if len(out) >= limit {
			break
		}
		out = append(out, item.chunk)
	}
	return out
}

func rankCandidate(candidate RetrievedChunk, queryTerms []string) RankDebug {
	quality := qualityClass(candidate.HeadingPath, candidate.Text)
	headingTerms := uniqueTerms(tokenize(candidate.HeadingPath))
	bodyTerms := uniqueTerms(tokenize(candidate.Text))
	headingOverlap := overlapCount(queryTerms, headingTerms)
	bodyOverlap := overlapCount(queryTerms, bodyTerms)

	reasons := []string{}
	score := -candidate.Score
	if headingOverlap > 0 {
		score += float64(headingOverlap) * 4
		reasons = append(reasons, "heading_match")
	}
	if bodyOverlap > 0 {
		score += float64(bodyOverlap) * 2
		reasons = append(reasons, "body_match")
	}
	if candidate.PageNumber.Valid {
		score += 0.75
		reasons = append(reasons, "page_boost")
	}
	switch quality {
	case QualityContent:
		score += 2
		reasons = append(reasons, "content_boost")
	case QualityTOC, QualityIndex, QualityReferenceOnly:
		score -= 12
		reasons = append(reasons, "low_value_penalty")
	case QualityLowValue:
		score -= 6
		reasons = append(reasons, "low_value_penalty")
	}
	if strings.Contains(normalizeHeadingKey(candidate.HeadingPath), "sync") && containsTerm(queryTerms, "sync") {
		score += 3
		reasons = append(reasons, "exact_heading_term")
	}
	if math.IsInf(score, 0) || math.IsNaN(score) {
		score = 0
	}
	return RankDebug{
		RawScore:    candidate.Score,
		RerankScore: score,
		Quality:     quality,
		Reasons:     reasons,
	}
}

func shouldIndexChunk(chunk Chunk) bool {
	return qualityClass(chunk.HeadingPath, chunk.Text) != QualityTOC &&
		qualityClass(chunk.HeadingPath, chunk.Text) != QualityIndex &&
		qualityClass(chunk.HeadingPath, chunk.Text) != QualityReferenceOnly
}

func tokenize(value string) []string {
	raw := wordRE.FindAllString(strings.ToLower(value), -1)
	out := make([]string, 0, len(raw))
	for _, term := range raw {
		term = strings.TrimFunc(term, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
		if term == "" || isQueryStopword(term) || term == "and" || term == "or" {
			continue
		}
		out = append(out, term)
	}
	return out
}

func uniqueTerms(terms []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(terms))
	for _, term := range terms {
		if seen[term] {
			continue
		}
		seen[term] = true
		out = append(out, term)
	}
	return out
}

func overlapCount(a, b []string) int {
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	set := map[string]bool{}
	for _, term := range b {
		set[term] = true
	}
	var count int
	for _, term := range a {
		if set[term] {
			count++
		}
	}
	return count
}

func containsTerm(terms []string, want string) bool {
	for _, term := range terms {
		if term == want {
			return true
		}
	}
	return false
}

func countNumbers(value string) int {
	var count int
	for _, word := range strings.Fields(value) {
		if _, err := parseLeadingInt(word); err == nil {
			count++
		}
	}
	return count
}

func parseLeadingInt(value string) (int, error) {
	var n int
	var found bool
	for _, r := range value {
		if r < '0' || r > '9' {
			break
		}
		found = true
		n = n*10 + int(r-'0')
	}
	if !found {
		return 0, errors.New("no leading int")
	}
	return n, nil
}

func looksLikeReferenceList(text string, words []string) bool {
	lines := strings.Split(strings.TrimSpace(text), "\n")
	if len(lines) <= 1 {
		return false
	}
	shortLines := 0
	numberLines := 0
	for _, line := range lines {
		lineWords := wordRE.FindAllString(line, -1)
		if len(lineWords) <= 4 {
			shortLines++
		}
		if countNumbers(line) > 0 {
			numberLines++
		}
	}
	return len(words) < 40 && shortLines >= len(lines)/2 && numberLines >= len(lines)/2
}
