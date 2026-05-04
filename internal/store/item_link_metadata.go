package store

import (
	"net/url"
	"strings"
)

func uniqueNonEmptyStrings(values []string) []string {
	seen := map[string]struct{}{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func deriveItemLinkMetadata(links []string) (string, []string, []string) {
	domains := make([]string, 0, len(links))
	githubURLs := make([]string, 0)
	seenDomains := map[string]struct{}{}
	for _, link := range links {
		parsed, err := url.Parse(link)
		if err != nil || parsed.Hostname() == "" {
			continue
		}
		host := strings.ToLower(parsed.Hostname())
		if _, ok := seenDomains[host]; !ok {
			seenDomains[host] = struct{}{}
			domains = append(domains, host)
		}
		if host == "github.com" {
			githubURLs = append(githubURLs, link)
		}
	}
	primary := ""
	if len(domains) > 0 {
		primary = domains[0]
	}
	return primary, domains, githubURLs
}
