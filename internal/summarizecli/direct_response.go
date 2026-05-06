package summarizecli

import (
	"encoding/json"
	"fmt"
	"strings"
)

func directSummaryText(target directSummaryTarget, respBody []byte) (string, error) {
	if target.nativeOllama {
		var payload ollamaChatResponse
		if err := json.Unmarshal(respBody, &payload); err != nil {
			bodyText := strings.TrimSpace(string(respBody))
			if len(bodyText) > 200 {
				bodyText = bodyText[:200]
			}
			return "", fmt.Errorf("parse %s summary response: %w (body prefix: %q)", target.label, err, bodyText)
		}
		return strings.TrimSpace(payload.Message.Content), nil
	}

	var payload chatCompletionsResponse
	if err := json.Unmarshal(respBody, &payload); err != nil {
		bodyText := strings.TrimSpace(string(respBody))
		if len(bodyText) > 200 {
			bodyText = bodyText[:200]
		}
		return "", fmt.Errorf("parse %s summary response: %w (body prefix: %q)", target.label, err, bodyText)
	}
	if len(payload.Choices) == 0 {
		return "", fmt.Errorf("run %s summary: response contained no choices", target.label)
	}
	return strings.TrimSpace(payload.Choices[0].Message.Content), nil
}
