package sourceenrich

import (
	"fmt"
	"strconv"
	"strings"
)

func splitJSStatements(script string) []string {
	var (
		out     []string
		start   int
		quote   rune
		escaped bool
	)

	for i, r := range script {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}
		if r == '\'' || r == '"' {
			quote = r
			continue
		}
		if r == ';' {
			out = append(out, script[start:i])
			start = i + 1
		}
	}
	if start <= len(script) {
		out = append(out, script[start:])
	}
	return out
}

func evalJSConcatExpression(expr string, vars map[string]string) (string, error) {
	var builder strings.Builder
	for _, token := range splitJSConcatTokens(expr) {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		switch {
		case isQuotedJSToken(token):
			value, err := unquoteJSLiteral(token)
			if err != nil {
				return "", fmt.Errorf("unquote %s: %w", token, err)
			}
			builder.WriteString(value)
		case strings.HasPrefix(token, "String.fromCharCode(") && strings.HasSuffix(token, ")"):
			value, err := evalFromCharCode(token)
			if err != nil {
				return "", err
			}
			builder.WriteRune(rune(value))
		default:
			value, ok := vars[token]
			if !ok {
				return "", fmt.Errorf("unsupported token %q", token)
			}
			builder.WriteString(value)
		}
	}
	return builder.String(), nil
}

func splitJSConcatTokens(expr string) []string {
	var (
		out     []string
		start   int
		depth   int
		quote   rune
		escaped bool
	)

	for i, r := range expr {
		if quote != 0 {
			if escaped {
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				continue
			}
			if r == quote {
				quote = 0
			}
			continue
		}

		switch r {
		case '\'', '"':
			quote = r
		case '(':
			depth++
		case ')':
			if depth > 0 {
				depth--
			}
		case '+':
			if depth == 0 {
				out = append(out, expr[start:i])
				start = i + 1
			}
		}
	}
	out = append(out, expr[start:])
	return out
}

func isQuotedJSToken(token string) bool {
	return len(token) >= 2 && ((token[0] == '\'' && token[len(token)-1] == '\'') || (token[0] == '"' && token[len(token)-1] == '"'))
}

func unquoteJSLiteral(token string) (string, error) {
	if !isQuotedJSToken(token) {
		return "", fmt.Errorf("not a quoted js literal")
	}

	quote := token[0]
	inner := token[1 : len(token)-1]
	var builder strings.Builder
	for i := 0; i < len(inner); i++ {
		ch := inner[i]
		if ch != '\\' {
			builder.WriteByte(ch)
			continue
		}
		if i+1 >= len(inner) {
			return "", fmt.Errorf("unterminated escape")
		}
		i++
		switch inner[i] {
		case '\\', '"', '\'':
			builder.WriteByte(inner[i])
		case 'n':
			builder.WriteByte('\n')
		case 'r':
			builder.WriteByte('\r')
		case 't':
			builder.WriteByte('\t')
		case quote:
			builder.WriteByte(inner[i])
		default:
			builder.WriteByte(inner[i])
		}
	}
	return builder.String(), nil
}

func evalFromCharCode(token string) (int64, error) {
	value := strings.TrimSuffix(strings.TrimPrefix(token, "String.fromCharCode("), ")")
	value = strings.TrimSpace(value)
	decoded, err := strconv.ParseInt(value, 0, 64)
	if err != nil {
		return 0, fmt.Errorf("parse fromCharCode %q: %w", value, err)
	}
	return decoded, nil
}
