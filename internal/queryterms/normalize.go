package queryterms

import (
	"strings"
	"unicode"
)

func canonicalTerm(term string) string {
	switch term {
	case "models":
		return "model"
	case "kid", "kids":
		return "children"
	case "child":
		return "children"
	case "killed", "killing", "kills":
		return "kill"
	case "charged", "charges", "charging":
		return "charge"
	}
	return term
}

func normalizeQuestionText(question string) string {
	question = stripCorpusFramePhrases(question)
	question = strings.NewReplacer(`\n`, " ", `\r`, " ", `\t`, " ", "-", " ", "_", " ").Replace(strings.TrimSpace(question))
	var b strings.Builder
	b.Grow(len(question))
	lastSpace := false
	for _, r := range question {
		if unicode.IsLetter(r) || unicode.IsNumber(r) {
			b.WriteRune(r)
			lastSpace = false
			continue
		}
		if !lastSpace {
			b.WriteByte(' ')
			lastSpace = true
		}
	}
	return b.String()
}

func stripCorpusFramePhrases(question string) string {
	question = strings.ToLower(question)
	for _, phrase := range []string{
		"in my research",
		"from my research",
		"my research",
		"in your research",
		"from your research",
		"your research",
		"in the research",
		"from the research",
	} {
		question = strings.ReplaceAll(question, phrase, " ")
	}
	return question
}

func looksLikeSourceKeyFragment(term string) bool {
	if len(term) < 8 {
		return false
	}
	allDigits := true
	allHex := true
	for _, r := range term {
		if !unicode.IsDigit(r) {
			allDigits = false
		}
		if !unicode.IsDigit(r) && (r < 'a' || r > 'f') {
			allHex = false
		}
	}
	return allDigits || (allHex && len(term) >= 10)
}
