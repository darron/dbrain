package categoryvocab

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Vocab holds the canonical tag/category vocabulary loaded from categories.yaml.
type Vocab struct {
	// Aliases maps a non-canonical form to its canonical replacement.
	// Keys and values should be lowercase-hyphenated; Title Case forms are
	// normalised automatically before lookup.
	Aliases map[string]string `yaml:"aliases"`

	// Drop lists tokens to silently remove after alias resolution.
	Drop []string `yaml:"drop"`

	// Internal: normalised-key versions of Aliases and Drop.
	aliasMap map[string]string
	dropSet  map[string]struct{}
}

// Load reads and parses a categories.yaml file. Returns an empty Vocab if the
// file does not exist, so callers can treat "no file" as "no rules".
func Load(path string) (Vocab, error) {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return Vocab{}, nil
	}
	if err != nil {
		return Vocab{}, fmt.Errorf("read categories.yaml: %w", err)
	}

	v, err := Parse(data)
	if err != nil {
		return Vocab{}, fmt.Errorf("parse categories.yaml: %w", err)
	}
	return v, nil
}

// Parse parses categories.yaml bytes and initialises lookup maps.
func Parse(data []byte) (Vocab, error) {
	var v Vocab
	if err := yaml.Unmarshal(data, &v); err != nil {
		return Vocab{}, err
	}
	v.init()
	return v, nil
}

func (v *Vocab) init() {
	v.dropSet = make(map[string]struct{}, len(v.Drop))
	for _, d := range v.Drop {
		v.dropSet[Normalize(d)] = struct{}{}
	}
	v.aliasMap = make(map[string]string, len(v.Aliases))
	for k, val := range v.Aliases {
		v.aliasMap[Normalize(k)] = val
	}
}

// Empty returns true when no rules are defined.
func (v Vocab) Empty() bool {
	return len(v.Aliases) == 0 && len(v.Drop) == 0
}

// Normalize converts a token to canonical lowercase-hyphenated form.
// "Canadian Politics" → "canadian-politics", "go-language" → "go-language".
// This is applied to every token regardless of whether a Vocab is loaded.
func Normalize(t string) string {
	t = strings.ToLower(strings.TrimSpace(t))
	var b strings.Builder
	prevHyphen := false
	for _, r := range t {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			prevHyphen = false
		} else {
			if !prevHyphen && b.Len() > 0 {
				b.WriteByte('-')
			}
			prevHyphen = true
		}
	}
	return strings.TrimRight(b.String(), "-")
}

// ApplyToTokens normalises every token to lowercase-hyphenated form, applies
// alias resolution and drop rules, and collapses duplicates.
// It always normalises — even when no Vocab rules are loaded.
func (v Vocab) ApplyToTokens(tokens []string) []string {
	seen := make(map[string]struct{}, len(tokens))
	out := make([]string, 0, len(tokens))
	for _, t := range tokens {
		t = v.resolve(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// ApplyToCSV applies normalisation and rules to a comma-separated user_tags
// string and returns the result plus whether anything changed.
func (v Vocab) ApplyToCSV(raw string) (string, bool) {
	if strings.TrimSpace(raw) == "" {
		return raw, false
	}
	parts := strings.Split(raw, ",")
	tokens := make([]string, 0, len(parts))
	for _, p := range parts {
		tokens = append(tokens, strings.TrimSpace(p))
	}
	applied := v.ApplyToTokens(tokens)
	out := strings.Join(applied, ",")
	changed := out != raw
	return out, changed
}

// PromptSection renders a concise vocabulary block for the LLM system prompt.
func (v Vocab) PromptSection() string {
	if v.Empty() {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("Canonical tag vocabulary — always prefer these exact forms:\n")

	canonical := make(map[string][]string)
	for from, to := range v.aliasMap {
		canonical[to] = append(canonical[to], from)
	}
	for to, froms := range canonical {
		_, _ = fmt.Fprintf(&sb, "  - %s (not: %s)\n", to, strings.Join(froms, ", "))
	}

	if len(v.Drop) > 0 {
		sb.WriteString("Tags to never emit:\n")
		sb.WriteString("  " + strings.Join(v.Drop, ", ") + "\n")
	}

	return sb.String()
}

func (v Vocab) resolve(token string) string {
	norm := Normalize(token)
	if _, drop := v.dropSet[norm]; drop {
		return ""
	}
	if canonical, ok := v.aliasMap[norm]; ok {
		canonNorm := Normalize(canonical)
		if _, drop := v.dropSet[canonNorm]; drop {
			return ""
		}
		return canonNorm
	}
	return norm
}
