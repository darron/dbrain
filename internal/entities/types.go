package entities

type Kind string

const (
	KindPerson  Kind = "person"
	KindOrg     Kind = "org"
	KindProject Kind = "project"
	KindSite    Kind = "site"
)

type Reference struct {
	RefKind      string `json:"ref_kind"`
	SourceKey    string `json:"source_key"`
	Title        string `json:"title"`
	NotePath     string `json:"note_path"`
	URL          string `json:"url"`
	SourceType   string `json:"source_type"`
	Relationship string `json:"relationship"`
}

type Link struct {
	Key          string `json:"key"`
	Name         string `json:"name"`
	Kind         Kind   `json:"kind"`
	NotePath     string `json:"note_path"`
	Relationship string `json:"relationship"`
}

type Entity struct {
	Key            string      `json:"key"`
	Name           string      `json:"name"`
	Kind           Kind        `json:"kind"`
	Aliases        []string    `json:"aliases,omitempty"`
	CanonicalURL   string      `json:"canonical_url,omitempty"`
	Domain         string      `json:"domain,omitempty"`
	NotePath       string      `json:"note_path"`
	SourceTypes    []string    `json:"source_types,omitempty"`
	ReferenceCount int         `json:"reference_count"`
	References     []Reference `json:"references,omitempty"`
	Links          []Link      `json:"links,omitempty"`
}

type SearchOptions struct {
	Kind  string
	Limit int
}

type builder struct {
	entity          Entity
	aliases         map[string]struct{}
	sourceTypes     map[string]struct{}
	references      map[string]Reference
	links           map[string]Link
	referenceCounts map[string]struct{}
}
