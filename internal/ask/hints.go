package ask

import (
	"strings"

	"github.com/darron/dbrain/internal/queryterms"
)

func Hints(question string) QueryHints {
	terms := queryterms.Terms(question)
	return QueryHints{
		TextQuery:  strings.Join(terms, " "),
		Terms:      terms,
		TagQueries: queryterms.TagQueries(terms),
	}
}
