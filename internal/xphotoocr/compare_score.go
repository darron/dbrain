package xphotoocr

import (
	"regexp"
	"strings"
	"unicode"
)

func summarizeCompareRuns(models []string, images []CompareImageResult) []CompareModelSummary {
	summaryByModel := make(map[string]*CompareModelSummary, len(models))
	overlapTotals := make(map[string]float64, len(models))
	overlapCounts := make(map[string]int, len(models))
	for _, modelName := range models {
		summaryByModel[modelName] = &CompareModelSummary{Model: modelName}
	}
	for _, image := range images {
		for _, run := range image.Runs {
			row := summaryByModel[run.Model]
			if row == nil {
				row = &CompareModelSummary{Model: run.Model}
				summaryByModel[run.Model] = row
			}
			if run.Status == "ok" {
				row.OK++
				row.TotalDurationMS += run.DurationMS
				row.TotalChars += run.CharCount
				if run.BaselineWordOverlap > 0 {
					overlapTotals[run.Model] += run.BaselineWordOverlap
					overlapCounts[run.Model]++
				}
			} else {
				row.Errors++
			}
		}
	}
	out := make([]CompareModelSummary, 0, len(summaryByModel))
	for _, modelName := range models {
		row := summaryByModel[modelName]
		if row == nil {
			continue
		}
		if row.OK > 0 {
			row.AverageDurationMS = row.TotalDurationMS / int64(row.OK)
			row.AverageChars = row.TotalChars / row.OK
		}
		if overlapCounts[modelName] > 0 {
			row.AverageBaselineWordOverlap = overlapTotals[modelName] / float64(overlapCounts[modelName])
		}
		out = append(out, *row)
	}
	return out
}

func annotateBaselineOverlap(runs []CompareRun) {
	if len(runs) < 2 || runs[0].Status != "ok" {
		return
	}
	baseline := normalizedWordSet(runs[0].Text)
	if len(baseline) == 0 {
		return
	}
	for i := 1; i < len(runs); i++ {
		if runs[i].Status != "ok" {
			continue
		}
		candidate := normalizedWordSet(runs[i].Text)
		if len(candidate) == 0 {
			continue
		}
		shared := 0
		for word := range baseline {
			if _, ok := candidate[word]; ok {
				shared++
			}
		}
		runs[i].BaselineWordOverlap = float64(shared) / float64(len(baseline))
		runs[i].BaselineOnlyWordCount = len(baseline) - shared
		runs[i].CandidateOnlyWordCount = len(candidate) - shared
	}
}

var wordSplitRE = regexp.MustCompile(`[\p{L}\p{N}]+`)

func normalizedWordSet(text string) map[string]struct{} {
	words := wordSplitRE.FindAllString(strings.ToLower(text), -1)
	out := make(map[string]struct{}, len(words))
	for _, word := range words {
		word = strings.TrimFunc(word, func(r rune) bool {
			return !unicode.IsLetter(r) && !unicode.IsNumber(r)
		})
		if len([]rune(word)) < 2 {
			continue
		}
		out[word] = struct{}{}
	}
	return out
}

func countLines(text string) int {
	text = strings.TrimSpace(text)
	if text == "" {
		return 0
	}
	return strings.Count(text, "\n") + 1
}
