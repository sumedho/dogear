package evaluation

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/sumedho/dogear/internal/dogear"
)

type Fixture struct {
	Name  string `json:"name"`
	Cases []Case `json:"cases"`
}
type Case struct {
	ID             string     `json:"id"`
	Query          string     `json:"query"`
	DocumentID     string     `json:"document_id"`
	Relevant       []Relevant `json:"relevant"`
	AnswerContains []string   `json:"answer_contains"`
}
type Relevant struct {
	HeadingPath  string `json:"heading_path"`
	PageNumber   *int64 `json:"page_number,omitempty"`
	TextContains string `json:"text_contains,omitempty"`
}
type CaseReport struct {
	ID                string  `json:"id"`
	ReciprocalRank    float64 `json:"reciprocal_rank"`
	RelevantRanks     []int   `json:"relevant_ranks"`
	LatencyMS         int64   `json:"latency_ms"`
	AnswerTermRecall  float64 `json:"answer_term_recall,omitempty"`
	CitationValidity  float64 `json:"citation_validity,omitempty"`
	CitationPrecision float64 `json:"citation_precision,omitempty"`
	CitationRecall    float64 `json:"citation_recall,omitempty"`
	Error             string  `json:"error,omitempty"`
}
type Report struct {
	Mode             string          `json:"mode"`
	Cases            int             `json:"cases"`
	RecallAt         map[int]float64 `json:"recall_at"`
	NDCGAt           map[int]float64 `json:"ndcg_at"`
	MRR              float64         `json:"mrr"`
	AverageLatencyMS float64         `json:"average_latency_ms"`
	Results          []CaseReport    `json:"results"`
}

func Load(path string) (Fixture, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Fixture{}, err
	}
	var fixture Fixture
	if err := json.Unmarshal(raw, &fixture); err != nil {
		return Fixture{}, err
	}
	if len(fixture.Cases) == 0 {
		return Fixture{}, fmt.Errorf("evaluation fixture contains no cases")
	}
	for i, item := range fixture.Cases {
		if item.ID == "" || item.Query == "" || len(item.Relevant) == 0 {
			return Fixture{}, fmt.Errorf("case %d requires id, query, and relevant selectors", i+1)
		}
	}
	return fixture, nil
}

type RetrieveFunc func(context.Context, string, Case, int) (dogear.RetrievalResult, error)
type AnswerFunc func(context.Context, string, Case) (string, error)

func Run(ctx context.Context, fixture Fixture, mode string, ks []int, retrieve RetrieveFunc, answer AnswerFunc) Report {
	sort.Ints(ks)
	maxK := ks[len(ks)-1]
	report := Report{Mode: mode, Cases: len(fixture.Cases), RecallAt: map[int]float64{}, NDCGAt: map[int]float64{}}
	var latency int64
	for _, item := range fixture.Cases {
		started := time.Now()
		result, err := retrieve(ctx, mode, item, maxK)
		elapsed := time.Since(started).Milliseconds()
		latency += elapsed
		caseReport := CaseReport{ID: item.ID, LatencyMS: elapsed}
		if err != nil {
			caseReport.Error = err.Error()
			report.Results = append(report.Results, caseReport)
			continue
		}
		for i, block := range result.Blocks {
			if matchesAny(block, item.Relevant) {
				caseReport.RelevantRanks = append(caseReport.RelevantRanks, i+1)
			}
		}
		if len(caseReport.RelevantRanks) > 0 {
			caseReport.ReciprocalRank = 1 / float64(caseReport.RelevantRanks[0])
			report.MRR += caseReport.ReciprocalRank
		}
		for _, k := range ks {
			found := false
			dcg := 0.0
			for _, rank := range caseReport.RelevantRanks {
				if rank <= k {
					found = true
					dcg += 1 / math.Log2(float64(rank)+1)
				}
			}
			if found {
				report.RecallAt[k]++
			}
			ideal := 0.0
			for i := 1; i <= min(k, len(item.Relevant)); i++ {
				ideal += 1 / math.Log2(float64(i)+1)
			}
			if ideal > 0 {
				report.NDCGAt[k] += dcg / ideal
			}
		}
		if answer != nil {
			text, answerErr := answer(ctx, mode, item)
			if answerErr != nil {
				caseReport.Error = answerErr.Error()
			} else {
				for _, term := range item.AnswerContains {
					if strings.Contains(strings.ToLower(text), strings.ToLower(term)) {
						caseReport.AnswerTermRecall++
					}
				}
				if len(item.AnswerContains) > 0 {
					caseReport.AnswerTermRecall /= float64(len(item.AnswerContains))
				}
				caseReport.CitationValidity, caseReport.CitationPrecision, caseReport.CitationRecall = citationMetrics(text, len(result.Blocks), caseReport.RelevantRanks)
			}
		}
		report.Results = append(report.Results, caseReport)
	}
	for _, k := range ks {
		report.RecallAt[k] /= float64(report.Cases)
		report.NDCGAt[k] /= float64(report.Cases)
	}
	report.MRR /= float64(report.Cases)
	report.AverageLatencyMS = float64(latency) / float64(report.Cases)
	return report
}

func matchesAny(block dogear.ContextBlock, selectors []Relevant) bool {
	for _, selector := range selectors {
		if selector.HeadingPath != "" && !strings.HasSuffix(strings.ToLower(block.Source.HeadingPath), strings.ToLower(selector.HeadingPath)) {
			continue
		}
		if selector.PageNumber != nil && (!block.Source.PageNumber.Valid || block.Source.PageNumber.Int64 != *selector.PageNumber) {
			continue
		}
		if selector.TextContains != "" && !strings.Contains(strings.ToLower(block.Text), strings.ToLower(selector.TextContains)) {
			continue
		}
		return true
	}
	return false
}

var citationRE = regexp.MustCompile(`\[([0-9]+)\]`)

func citationMetrics(answer string, sourceCount int, relevantRanks []int) (float64, float64, float64) {
	matches := citationRE.FindAllStringSubmatch(answer, -1)
	if len(matches) == 0 {
		return 0, 0, 0
	}
	relevant := map[int]bool{}
	for _, rank := range relevantRanks {
		relevant[rank] = true
	}
	valid := 0
	relevantCitations := 0
	citedRelevant := map[int]bool{}
	for _, match := range matches {
		var n int
		_, _ = fmt.Sscanf(match[1], "%d", &n)
		if n >= 1 && n <= sourceCount {
			valid++
			if relevant[n] {
				relevantCitations++
				citedRelevant[n] = true
			}
		}
	}
	precision := 0.0
	if valid > 0 {
		precision = float64(relevantCitations) / float64(valid)
	}
	recall := 0.0
	if len(relevant) > 0 {
		recall = float64(len(citedRelevant)) / float64(len(relevant))
	}
	return float64(valid) / float64(len(matches)), precision, recall
}
