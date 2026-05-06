package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"
)

func (s *Server) handlePromptsList() map[string]interface{} {
	return map[string]interface{}{
		"prompts": promptDefinitions(),
	}
}

func (s *Server) handlePromptGet(raw json.RawMessage) (map[string]interface{}, error) {
	var args struct {
		Name      string                 `json:"name"`
		Arguments map[string]interface{} `json:"arguments"`
	}
	if err := json.Unmarshal(raw, &args); err != nil {
		return nil, fmt.Errorf("decode prompts/get args: %w", err)
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, fmt.Errorf("prompts/get requires a name")
	}

	description, text, err := renderPrompt(name, args.Arguments)
	if err != nil {
		return nil, err
	}

	return map[string]interface{}{
		"description": description,
		"messages": []map[string]interface{}{
			{
				"role": "user",
				"content": map[string]interface{}{
					"type": "text",
					"text": text,
				},
			},
		},
	}, nil
}
