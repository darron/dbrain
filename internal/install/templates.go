package install

import _ "embed"

//go:embed templates/config.yaml.sample
var DefaultConfigTemplate []byte

//go:embed templates/categories.yaml
var DefaultCategoriesTemplate []byte
