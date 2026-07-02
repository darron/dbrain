package llmclient

import (
	"net/http"
	"time"

	"github.com/darron/dbrain/internal/llmprovider"
)

type ContentType string

const (
	ContentText  ContentType = "text"
	ContentImage ContentType = "image"
)

type ContentPart struct {
	Type      ContentType
	Text      string
	ImageData []byte
	MIMEType  string
}

type Message struct {
	Role  string
	Parts []ContentPart
}

type ResponseContract string

const (
	ResponseContractText ResponseContract = "text"
	ResponseContractJSON ResponseContract = "json_prompt_only"
)

type Request struct {
	Model             string
	Messages          []Message
	SamplerParams     map[string]any
	ResponseContract  ResponseContract
	Timeout           time.Duration
	Task              llmprovider.Task
	RootDir           string
	Env               map[string]string
	ProviderOverrides map[llmprovider.Provider]llmprovider.ProviderOverrides
	Resolve           llmprovider.ResolveOptions
	HTTPClient        *http.Client
}

type Response struct {
	Text            string
	RawJSON         string
	Model           string
	Provider        llmprovider.Provider
	APIModel        string
	Transport       llmprovider.Transport
	Tool            string
	ToolVersion     string
	ParamAccounting llmprovider.ParamAccounting
}

func SystemMessage(text string) Message {
	return Message{Role: "system", Parts: []ContentPart{{Type: ContentText, Text: text}}}
}

func UserTextMessage(text string) Message {
	return Message{Role: "user", Parts: []ContentPart{{Type: ContentText, Text: text}}}
}
