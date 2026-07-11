package researcheval

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/darron/dbrain/internal/ask"
	"github.com/darron/dbrain/internal/researchtrace"
)

var (
	turnHeadingRE       = regexp.MustCompile(`(?m)^## Turn [0-9]+[^\n]*\n`)
	sourceKeyBulletRE   = regexp.MustCompile("^- `([^`]+)`")
	inlineStatusRE      = regexp.MustCompile("(?m)^Status: `([^`]+)`")
	queryPlanTermsRE    = regexp.MustCompile(`^- terms: (.+)$`)
	queryPlanPlannerRE  = regexp.MustCompile(`^- planner: ([^(\n]+)(?: \(([^)]+)\))?`)
	backtickValueLineRE = regexp.MustCompile("`([^`]+)`")
)

func ProposeFromTrace(path string, opts ProposalOptions) (Proposal, error) {
	trace, resolvedPath, err := LoadTrace(path)
	if err != nil {
		return Proposal{}, err
	}
	if trace.Pack == nil {
		return Proposal{}, fmt.Errorf("trace %s does not contain a research pack", resolvedPath)
	}

	baseName := strings.TrimSpace(trace.Question)
	if baseName == "" {
		baseName = trace.Pack.Question
	}
	if baseName == "" {
		baseName = filepath.Base(filepath.Dir(resolvedPath))
	}

	plan := trace.Pack.QueryPlan
	sourceKeys := evidenceSourceKeys(trace.Pack.Evidence)
	promptKeys := tracePromptAdmittedKeys(trace)
	answerKeys := traceAnswerCitedKeys(trace)
	expectAny := firstNonEmptySlice(answerKeys, promptKeys, sourceKeys)
	primary := Case{
		Name:                           "trace: " + baseName,
		Question:                       firstNonEmpty(trace.Question, trace.Pack.Question),
		Limit:                          plan.Limit,
		MaxCharsPerDoc:                 plan.MaxCharsPerDoc,
		SourceTypes:                    append([]string(nil), plan.SourceTypes...),
		IncludeRelated:                 plan.IncludeRelated,
		RelatedLimit:                   plan.RelatedLimit,
		IncludeTopicBrief:              boolPtr(plan.IncludeTopicBrief),
		PlannerModel:                   plan.PlannerModel,
		MinEvidence:                    minPositive(1, len(sourceKeys)),
		ExpectSourceKeys:               sourceKeys,
		ExpectAnySourceKeys:            expectAny,
		ExpectPlanner:                  plan.Planner,
		ExpectQueryFamily:              plan.QueryFamily,
		ExpectQueryTerms:               append([]string(nil), plan.QueryTerms...),
		ExpectQueryVariants:            queryVariantStrings(plan.QueryVariants),
		ExpectConcepts:                 conceptStrings(plan.Concepts),
		ExpectAnswerStatus:             traceAnswerStatus(trace),
		ExpectCitationSourceKeys:       promptKeys,
		ExpectPromptAdmittedSourceKeys: promptKeys,
		MinRetrievalSignals:            minPositive(1, countTraceRetrievalSignals(trace)),
	}
	if plan.Planner == "deterministic" && plan.PlannerModel == "" {
		primary.DisablePlanner = true
	}
	if !opts.IncludeAnswerText {
		primary.ExpectAnswerText = nil
	} else if trace.Synthesis != nil && strings.TrimSpace(trace.Synthesis.Answer) != "" {
		primary.ExpectAnswerText = []string{trimForAssertion(trace.Synthesis.Answer)}
	}

	proposal := Proposal{
		SchemaVersion: ProposalSchemaVersion,
		Source:        resolvedPath,
		SourceType:    "trace",
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Cases:         []Case{primary},
		Notes: []string{
			"Review these cases before committing them under evals/local.",
			"Trace proposals use prompt-admitted source keys for no-call citation assertions; answer-cited keys require run_with_runner and explicit review.",
		},
	}
	if !primary.DisablePlanner && len(expectAny) > 0 {
		disabled := primary
		disabled.Name = primary.Name + " planner disabled"
		disabled.DisablePlanner = true
		disabled.PlannerModel = ""
		disabled.ExpectPlanner = "deterministic"
		disabled.ExpectSourceKeys = nil
		disabled.ExpectQueryVariants = nil
		disabled.ExpectConcepts = nil
		disabled.ExpectCitationSourceKeys = nil
		disabled.ExpectPromptAdmittedSourceKeys = nil
		disabled.ExpectRelevanceExcludedSourceKeys = nil
		disabled.ExpectBudgetDroppedSourceKeys = nil
		disabled.ExpectAnswerCitedSourceKeys = nil
		disabled.ExpectAnswerStatus = ""
		disabled.MinEvidence = 1
		proposal.Cases = append(proposal.Cases, disabled)
	}
	return proposal, nil
}

func ProposeFromTranscript(path string, opts ProposalOptions) (Proposal, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Proposal{}, fmt.Errorf("read transcript %s: %w", path, err)
	}
	turns := splitTranscriptTurns(string(data))
	if len(turns) == 0 {
		return Proposal{}, fmt.Errorf("transcript %s contains no turns", path)
	}

	cases := make([]Case, 0, len(turns))
	for i, turn := range turns {
		parsed := parseTranscriptTurn(turn)
		if parsed.question == "" {
			continue
		}
		sourceKeys := uniqueNonEmpty(append(parsed.citationKeys, parsed.evidenceKeys...))
		tc := Case{
			Name:                           fmt.Sprintf("transcript turn %d: %s", i+1, shortName(parsed.question)),
			Question:                       parsed.question,
			Limit:                          10,
			IncludeRelated:                 true,
			RelatedLimit:                   2,
			MinEvidence:                    minPositive(1, len(sourceKeys)),
			ExpectAnySourceKeys:            sourceKeys,
			ExpectCitationSourceKeys:       parsed.citationKeys,
			ExpectPromptAdmittedSourceKeys: parsed.citationKeys,
			ExpectQueryTerms:               parsed.queryTerms,
			ExpectPlanner:                  parsed.planner,
			ExpectAnswerStatus:             parsed.answerStatus(),
			MinRetrievalSignals:            1,
		}
		if parsed.plannerModel != "" {
			tc.PlannerModel = parsed.plannerModel
		}
		if opts.IncludeAnswerText && parsed.answer != "" {
			tc.ExpectAnswerText = []string{trimForAssertion(parsed.answer)}
		}
		cases = append(cases, tc)
	}
	if len(cases) == 0 {
		return Proposal{}, fmt.Errorf("transcript %s did not contain usable questions", path)
	}
	return Proposal{
		SchemaVersion: ProposalSchemaVersion,
		Source:        path,
		SourceType:    "transcript",
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
		Cases:         cases,
		Notes: []string{
			"Transcript answers are not treated as evidence.",
			"Answer-text assertions are omitted unless --include-answer-text is set.",
		},
	}, nil
}

type transcriptTurn struct {
	question     string
	answer       string
	status       string
	planner      string
	plannerModel string
	queryTerms   []string
	citationKeys []string
	evidenceKeys []string
}

func splitTranscriptTurns(markdown string) []string {
	matches := turnHeadingRE.FindAllStringIndex(markdown, -1)
	if len(matches) == 0 {
		return nil
	}
	turns := make([]string, 0, len(matches))
	for i, match := range matches {
		start := match[0]
		end := len(markdown)
		if i+1 < len(matches) {
			end = matches[i+1][0]
		}
		turns = append(turns, markdown[start:end])
	}
	return turns
}

func parseTranscriptTurn(markdown string) transcriptTurn {
	var out transcriptTurn
	out.question = sectionText(markdown, "Question")
	out.answer = sectionText(markdown, "Answer")
	if match := inlineStatusRE.FindStringSubmatch(markdown); len(match) == 2 {
		out.status = strings.TrimSpace(match[1])
	}
	out.citationKeys = sourceKeysInSection(markdown, "Citations")
	out.evidenceKeys = uniqueNonEmpty(append(sourceKeysInSection(markdown, "Evidence"), sourceKeysInSection(markdown, "Exact Tag Evidence")...))

	scanner := bufio.NewScanner(strings.NewReader(sectionText(markdown, "Research Pack")))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if match := queryPlanTermsRE.FindStringSubmatch(line); len(match) == 2 {
			for _, term := range strings.Split(match[1], ",") {
				out.queryTerms = append(out.queryTerms, strings.TrimSpace(term))
			}
		}
		if match := queryPlanPlannerRE.FindStringSubmatch(line); len(match) >= 2 {
			out.planner = strings.TrimSpace(match[1])
			if len(match) >= 3 {
				out.plannerModel = strings.TrimSpace(match[2])
			}
		}
	}
	out.queryTerms = uniqueNonEmpty(out.queryTerms)
	return out
}

func (turn transcriptTurn) answerStatus() string {
	if strings.EqualFold(turn.status, "ready") && (len(turn.citationKeys) > 0 || len(turn.evidenceKeys) > 0) {
		return "ok"
	}
	if strings.Contains(strings.ToLower(turn.answer), "no evidence") {
		return "no_evidence"
	}
	return ""
}

func sectionText(markdown string, heading string) string {
	startHeading := "### " + heading
	start := strings.Index(markdown, startHeading)
	if start < 0 {
		return ""
	}
	rest := markdown[start+len(startHeading):]
	rest = strings.TrimLeft(rest, " \t\r\n")
	if next := strings.Index(rest, "\n### "); next >= 0 {
		rest = rest[:next]
	}
	return strings.TrimSpace(rest)
}

func sourceKeysInSection(markdown string, heading string) []string {
	text := sectionText(markdown, heading)
	if text == "" {
		return nil
	}
	var keys []string
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if match := sourceKeyBulletRE.FindStringSubmatch(line); len(match) == 2 {
			keys = append(keys, strings.TrimSpace(match[1]))
			continue
		}
		if strings.HasPrefix(line, "- ") {
			for _, match := range backtickValueLineRE.FindAllStringSubmatch(line, -1) {
				if len(match) == 2 && looksLikeSourceKey(match[1]) {
					keys = append(keys, strings.TrimSpace(match[1]))
				}
			}
		}
	}
	return uniqueNonEmpty(keys)
}

func evidenceSourceKeys(rows []ask.Evidence) []string {
	keys := make([]string, 0, len(rows))
	for _, row := range rows {
		keys = append(keys, row.SourceKey)
	}
	return uniqueNonEmpty(keys)
}

func looksLikeSourceKey(value string) bool {
	value = strings.TrimSpace(value)
	return strings.Contains(value, ":") && !strings.Contains(value, " ")
}

func shortName(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= 48 {
		return value
	}
	return strings.TrimSpace(value[:48])
}

func trimForAssertion(value string) string {
	value = strings.Join(strings.Fields(value), " ")
	if len(value) <= 160 {
		return value
	}
	return strings.TrimSpace(value[:160])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return ""
}

func firstNonEmptySlice(values ...[]string) []string {
	for _, value := range values {
		if len(value) > 0 {
			return append([]string(nil), value...)
		}
	}
	return nil
}

func boolPtr(value bool) *bool {
	out := value
	return &out
}

func minPositive(min int, value int) int {
	if value <= 0 {
		return 0
	}
	if value < min {
		return value
	}
	return min
}

func tracePromptAdmittedKeys(trace researchtrace.ResearchTrace) []string {
	if trace.EvidenceFlow != nil && len(trace.EvidenceFlow.PromptAdmittedSourceKeys) > 0 {
		return append([]string(nil), trace.EvidenceFlow.PromptAdmittedSourceKeys...)
	}
	if trace.PreparedSynthesis != nil && len(trace.PreparedSynthesis.Citations) > 0 {
		return citationSourceKeys(trace.PreparedSynthesis.Citations)
	}
	// Traces written before prepared_synthesis/evidence_flow were added only
	// retained answer citations. Preserve proposal compatibility, while new
	// traces use the exact prompt-admitted stage above.
	if trace.Synthesis != nil && len(trace.Synthesis.Citations) > 0 {
		return citationSourceKeys(trace.Synthesis.Citations)
	}
	return nil
}

func traceAnswerCitedKeys(trace researchtrace.ResearchTrace) []string {
	if trace.EvidenceFlow != nil && len(trace.EvidenceFlow.AnswerCitedSourceKeys) > 0 {
		return append([]string(nil), trace.EvidenceFlow.AnswerCitedSourceKeys...)
	}
	if trace.Synthesis != nil && len(trace.Synthesis.Citations) > 0 {
		return citationSourceKeys(trace.Synthesis.Citations)
	}
	return nil
}

func traceAnswerStatus(trace researchtrace.ResearchTrace) string {
	if trace.Synthesis != nil {
		return trace.Synthesis.AnswerStatus
	}
	if trace.PreparedSynthesis != nil {
		return trace.PreparedSynthesis.Status
	}
	return ""
}

func countTraceRetrievalSignals(trace researchtrace.ResearchTrace) int {
	if trace.Pack == nil {
		return 0
	}
	total := 0
	for _, ev := range trace.Pack.Evidence {
		if ev.Retrieval != nil {
			total += len(ev.Retrieval.Signals)
		}
	}
	return total
}
