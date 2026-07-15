package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

const maxBatchRequests = 16

func (s *Server) processPayload(ctx context.Context, payload []byte) (interface{}, bool) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return rpcError(nil, -32700, "parse error"), true
	}
	if trimmed[0] != '[' {
		start := time.Now()
		resp, ok := s.handle(ctx, trimmed)
		logMCPRequest(trimmed, resp, ok, time.Since(start))
		return resp, ok
	}

	var batch []json.RawMessage
	if err := json.Unmarshal(trimmed, &batch); err != nil {
		return rpcError(nil, -32700, "parse error"), true
	}
	if len(batch) == 0 {
		return rpcError(nil, -32600, "invalid request"), true
	}
	return processBatch(ctx, batch, s.handle)
}

func processBatch(ctx context.Context, batch []json.RawMessage, dispatch func(context.Context, []byte) (response, bool)) (interface{}, bool) {
	if len(batch) > maxBatchRequests {
		return rpcError(nil, -32600, fmt.Sprintf("batch exceeds maximum of %d requests; split into smaller batches", maxBatchRequests)), true
	}

	responses := make([]response, 0, len(batch))
	for _, raw := range batch {
		start := time.Now()
		resp, ok := dispatch(ctx, raw)
		logMCPRequest(raw, resp, ok, time.Since(start))
		if ok {
			responses = append(responses, resp)
		}
	}
	if len(responses) == 0 {
		return nil, false
	}
	return responses, true
}

func logMCPRequest(payload []byte, resp response, responded bool, duration time.Duration) {
	var req request
	if err := json.Unmarshal(payload, &req); err != nil {
		_, _ = fmt.Fprintf(os.Stderr, "DEBUG %s mcp request method=parse_error status=error duration=%s\n", time.Now().Format("15:04:05.000"), duration)
		return
	}

	tool := ""
	if req.Method == "tools/call" {
		var params struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(req.Params, &params); err == nil {
			tool = strings.TrimSpace(params.Name)
		}
	}

	status := "ok"
	if !responded {
		status = "notification"
	} else if resp.Error != nil {
		status = "error"
	} else if result, ok := resp.Result.(map[string]interface{}); ok {
		if isError, ok := result["isError"].(bool); ok && isError {
			status = "tool_error"
		}
	}

	if tool != "" {
		_, _ = fmt.Fprintf(os.Stderr, "DEBUG %s mcp request method=%s tool=%s status=%s duration=%s\n", time.Now().Format("15:04:05.000"), req.Method, tool, status, duration)
		return
	}
	_, _ = fmt.Fprintf(os.Stderr, "DEBUG %s mcp request method=%s status=%s duration=%s\n", time.Now().Format("15:04:05.000"), req.Method, status, duration)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  interface{}     `json:"result,omitempty"`
	Error   *responseError  `json:"error,omitempty"`
}

type responseError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (s *Server) handle(ctx context.Context, payload []byte) (response, bool) {
	var req request
	if err := json.Unmarshal(payload, &req); err != nil {
		return rpcError(nil, -32700, "parse error"), true
	}

	isNotification := len(req.ID) == 0
	if req.Method == "" {
		return response{}, false
	}
	if isNotification {
		return response{}, false
	}
	switch req.Method {
	case "initialize":
		result := map[string]interface{}{
			"protocolVersion": protocolVersion,
			"serverInfo": map[string]string{
				"name":    "dbrain",
				"version": serverVersion(),
			},
			"capabilities": map[string]interface{}{
				"prompts": map[string]interface{}{
					"listChanged": false,
				},
				"resources": map[string]interface{}{
					"listChanged": false,
					"subscribe":   false,
				},
				"tools": map[string]interface{}{
					"listChanged": false,
				},
			},
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "notifications/initialized":
		return response{}, false
	case "ping":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{}}, true
	case "resources/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: s.handleResourcesList()}, true
	case "resources/templates/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: s.handleResourceTemplatesList()}, true
	case "resources/read":
		result, err := s.handleResourceRead(ctx, req.Params)
		if err != nil {
			return rpcError(req.ID, -32000, err.Error()), true
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "prompts/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: s.handlePromptsList()}, true
	case "prompts/get":
		result, err := s.handlePromptGet(req.Params)
		if err != nil {
			return rpcError(req.ID, -32000, err.Error()), true
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	case "tools/list":
		return response{JSONRPC: "2.0", ID: req.ID, Result: map[string]interface{}{"tools": filterToolDefinitions(toolDefinitions(), s.capabilities)}}, true
	case "tools/call":
		result, err := s.handleToolCall(ctx, req.Params)
		if err != nil {
			return response{JSONRPC: "2.0", ID: req.ID, Result: toolErrorResult(err)}, true
		}
		return response{JSONRPC: "2.0", ID: req.ID, Result: result}, true
	default:
		if isNotification {
			return response{}, false
		}
		return rpcError(req.ID, -32601, "method not found"), true
	}
}

func rpcError(id json.RawMessage, code int, message string) response {
	return response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &responseError{
			Code:    code,
			Message: message,
		},
	}
}
