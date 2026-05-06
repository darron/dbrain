package topics

type topicEvidence struct {
	Node    TopicMapNode
	Title   string
	Detail  string
	Phrases []string
}

type topicSignalCluster struct {
	Phrase     string
	SourceKeys []string
	Titles     []string
	Count      int
}
