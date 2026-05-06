package topics

type Options struct {
	SourceTypes  []string
	SeedLimit    int
	RelatedLimit int
}

type TopicMap struct {
	Topic        string         `json:"topic"`
	SourceTypes  []string       `json:"source_types,omitempty"`
	SeedLimit    int            `json:"seed_limit"`
	RelatedLimit int            `json:"related_limit"`
	Entities     []TopicEntity  `json:"entities,omitempty"`
	Pivots       TopicPivots    `json:"pivots,omitempty"`
	Synthesis    TopicSynthesis `json:"synthesis,omitempty"`
	Nodes        []TopicMapNode `json:"nodes"`
	Edges        []TopicMapEdge `json:"edges"`
}

type Definition struct {
	Topic        string   `json:"topic"`
	SourceTypes  []string `json:"source_types,omitempty"`
	SeedLimit    int      `json:"seed_limit"`
	RelatedLimit int      `json:"related_limit"`
	NotePath     string   `json:"note_path,omitempty"`
}

type TopicMapNode struct {
	SourceKey  string `json:"source_key"`
	Kind       string `json:"kind"`
	Title      string `json:"title"`
	URL        string `json:"url"`
	NotePath   string `json:"note_path"`
	SourceType string `json:"source_type"`
	Role       string `json:"role"`
}

type TopicMapEdge struct {
	From         string `json:"from"`
	To           string `json:"to"`
	Relationship string `json:"relationship"`
}

type TopicEntity struct {
	Key               string   `json:"key"`
	Name              string   `json:"name"`
	Kind              string   `json:"kind"`
	NotePath          string   `json:"note_path"`
	CanonicalURL      string   `json:"canonical_url,omitempty"`
	ReferenceCount    int      `json:"reference_count"`
	MatchedReferences int      `json:"matched_references"`
	MatchedSourceKeys []string `json:"matched_source_keys,omitempty"`
}

type TopicPivots struct {
	Projects     []TopicEntity  `json:"projects,omitempty"`
	Orgs         []TopicEntity  `json:"orgs,omitempty"`
	Sites        []TopicEntity  `json:"sites,omitempty"`
	People       []TopicEntity  `json:"people,omitempty"`
	SeedNodes    []TopicMapNode `json:"seed_nodes,omitempty"`
	RelatedNodes []TopicMapNode `json:"related_nodes,omitempty"`
}

type TopicSynthesis struct {
	Overview      string        `json:"overview,omitempty"`
	Angles        []string      `json:"angles,omitempty"`
	Signals       []TopicSignal `json:"signals,omitempty"`
	OpenQuestions []string      `json:"open_questions,omitempty"`
	WhyItMatters  string        `json:"why_it_matters,omitempty"`
}

type TopicSignal struct {
	Title      string   `json:"title"`
	Detail     string   `json:"detail"`
	SourceKeys []string `json:"source_keys,omitempty"`
}

type graphNode struct {
	TopicMapNode
	ItemID   int64
	SourceID int64
}
