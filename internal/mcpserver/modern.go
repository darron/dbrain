package mcpserver

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math/big"
	"net/http"
	"strings"
	"time"
)

const (
	modernProtocolVersion       = "2026-07-28"
	modernProtocolVersionKey    = "io.modelcontextprotocol/protocolVersion"
	modernClientCapabilitiesKey = "io.modelcontextprotocol/clientCapabilities"
	modernServerInfoKey         = "io.modelcontextprotocol/serverInfo"
	modernHeaderVersion         = "MCP-Protocol-Version"
	modernHeaderMethod          = "Mcp-Method"
	modernHeaderName            = "Mcp-Name"
	modernHeaderSentinelPrefix  = "=?base64?"
	modernHeaderSentinelSuffix  = "?="
)

type modernRequest struct {
	request
	params       map[string]json.RawMessage
	version      string
	notification bool
}

func modernPayloadMarked(payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		return false
	}
	if trimmed[0] == '[' {
		var batch []json.RawMessage
		if err := json.Unmarshal(trimmed, &batch); err != nil {
			return false
		}
		for _, raw := range batch {
			if modernObjectMarked(raw) {
				return true
			}
		}
		return false
	}
	return modernObjectMarked(trimmed)
}

func modernObjectMarked(payload []byte) bool {
	var object map[string]json.RawMessage
	if err := json.Unmarshal(payload, &object); err != nil || object == nil {
		return false
	}
	if _, hasMethod := object["method"]; !hasMethod {
		if _, hasResult := object["result"]; hasResult {
			return true
		}
		if _, hasError := object["error"]; hasError {
			return true
		}
	}
	params, ok := object["params"]
	if !ok || bytes.Equal(bytes.TrimSpace(params), []byte("null")) {
		return false
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(params, &fields); err != nil || fields == nil {
		return false
	}
	metaRaw, ok := fields["_meta"]
	if !ok || bytes.Equal(bytes.TrimSpace(metaRaw), []byte("null")) {
		return false
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(metaRaw, &meta); err != nil || meta == nil {
		return false
	}
	_, ok = meta[modernProtocolVersionKey]
	return ok
}

func modernHeadersMarked(headers http.Header) bool {
	for _, key := range []string{modernHeaderMethod, modernHeaderName} {
		if len(headers.Values(key)) != 0 {
			return true
		}
	}
	for _, value := range headers.Values(modernHeaderVersion) {
		if value == modernProtocolVersion {
			return true
		}
	}
	return false
}

func (s *Server) processModernPayload(ctx context.Context, payload []byte) (interface{}, bool) {
	start := time.Now()
	resp, ok := s.handleModernPayload(ctx, payload)
	if ok {
		logMCPRequest(payload, resp, true, time.Since(start))
	}
	return resp, ok
}

func (s *Server) handleModernPayload(ctx context.Context, payload []byte) (response, bool) {
	req, invalid := decodeModernRequest(payload)
	if invalid != nil {
		if modernNotificationPayload(payload) {
			return response{}, false
		}
		return *invalid, true
	}
	return s.handleModernRequest(ctx, req)
}

func modernNotificationPayload(payload []byte) bool {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || trimmed[0] == '[' {
		return false
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil || object == nil {
		return false
	}
	if _, hasID := object["id"]; hasID {
		return false
	}
	jsonrpc, ok := rawString(object["jsonrpc"])
	if !ok || jsonrpc != "2.0" {
		return false
	}
	method, ok := rawString(object["method"])
	return ok && strings.TrimSpace(method) != ""
}

func decodeModernRequest(payload []byte) (modernRequest, *response) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 {
		resp := modernError(nil, -32700, "parse error", nil)
		return modernRequest{}, &resp
	}
	if trimmed[0] == '[' {
		resp := modernError(nil, -32600, "modern transports accept one JSON-RPC message per frame", nil)
		return modernRequest{}, &resp
	}

	var object map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &object); err != nil {
		resp := modernError(nil, -32700, "parse error", nil)
		return modernRequest{}, &resp
	}
	if object == nil {
		resp := modernError(nil, -32600, "invalid request", nil)
		return modernRequest{}, &resp
	}

	jsonrpc, ok := rawString(object["jsonrpc"])
	if !ok || jsonrpc != "2.0" {
		resp := modernError(rawRequestID(object), -32600, "invalid request: jsonrpc must be \"2.0\"", nil)
		return modernRequest{}, &resp
	}

	methodRaw, hasMethod := object["method"]
	if !hasMethod {
		resp := modernError(rawRequestID(object), -32600, "invalid request: method is required", nil)
		return modernRequest{}, &resp
	}
	method, ok := rawString(methodRaw)
	if !ok || strings.TrimSpace(method) == "" {
		resp := modernError(rawRequestID(object), -32600, "invalid request: method must be a string", nil)
		return modernRequest{}, &resp
	}
	if _, hasResult := object["result"]; hasResult {
		resp := modernError(rawRequestID(object), -32600, "invalid request: result is not allowed", nil)
		return modernRequest{}, &resp
	}
	if _, hasError := object["error"]; hasError {
		resp := modernError(rawRequestID(object), -32600, "invalid request: error is not allowed", nil)
		return modernRequest{}, &resp
	}

	id, hasID := object["id"]
	if hasID && !validModernID(id) {
		resp := modernError(rawRequestID(object), -32600, "invalid request: id must be a non-null string or integer", nil)
		return modernRequest{}, &resp
	}

	paramsRaw, hasParams := object["params"]
	if !hasParams {
		paramsRaw = json.RawMessage(`{}`)
	}
	if bytes.Equal(bytes.TrimSpace(paramsRaw), []byte("null")) {
		resp := modernError(idForResponse(id, hasID), -32600, "invalid request: params must be an object", nil)
		return modernRequest{}, &resp
	}
	var params map[string]json.RawMessage
	if err := json.Unmarshal(paramsRaw, &params); err != nil || params == nil {
		resp := modernError(idForResponse(id, hasID), -32600, "invalid request: params must be an object", nil)
		return modernRequest{}, &resp
	}

	metaRaw, hasMeta := params["_meta"]
	if !hasMeta || bytes.Equal(bytes.TrimSpace(metaRaw), []byte("null")) {
		resp := modernError(idForResponse(id, hasID), -32602, "modern request metadata is required", nil)
		return modernRequest{}, &resp
	}
	var meta map[string]json.RawMessage
	if err := json.Unmarshal(metaRaw, &meta); err != nil || meta == nil {
		resp := modernError(idForResponse(id, hasID), -32602, "modern request metadata must be an object", nil)
		return modernRequest{}, &resp
	}
	version, ok := rawString(meta[modernProtocolVersionKey])
	if !ok || strings.TrimSpace(version) == "" {
		resp := modernError(idForResponse(id, hasID), -32602, "modern request protocolVersion is required", nil)
		return modernRequest{}, &resp
	}
	capabilities, ok := meta[modernClientCapabilitiesKey]
	if !ok || bytes.Equal(bytes.TrimSpace(capabilities), []byte("null")) {
		resp := modernError(idForResponse(id, hasID), -32602, "modern request clientCapabilities is required", nil)
		return modernRequest{}, &resp
	}
	var capabilityObject map[string]json.RawMessage
	if err := json.Unmarshal(capabilities, &capabilityObject); err != nil || capabilityObject == nil {
		resp := modernError(idForResponse(id, hasID), -32602, "modern request clientCapabilities must be an object", nil)
		return modernRequest{}, &resp
	}
	if clientInfo, ok := meta["io.modelcontextprotocol/clientInfo"]; ok {
		var clientObject map[string]json.RawMessage
		if err := json.Unmarshal(clientInfo, &clientObject); err != nil || clientObject == nil {
			resp := modernError(idForResponse(id, hasID), -32602, "modern request clientInfo must be an object", nil)
			return modernRequest{}, &resp
		}
	}

	return modernRequest{
		request: request{
			JSONRPC: jsonrpc,
			ID:      id,
			Method:  method,
			Params:  paramsRaw,
		},
		params:       params,
		version:      version,
		notification: !hasID,
	}, nil
}

func rawString(raw json.RawMessage) (string, bool) {
	if len(raw) == 0 {
		return "", false
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return "", false
	}
	return value, true
}

func rawRequestID(object map[string]json.RawMessage) json.RawMessage {
	id, ok := object["id"]
	if !ok || bytes.Equal(bytes.TrimSpace(id), []byte("null")) {
		return nil
	}
	return id
}

func idForResponse(id json.RawMessage, hasID bool) json.RawMessage {
	if !hasID || bytes.Equal(bytes.TrimSpace(id), []byte("null")) {
		return nil
	}
	return id
}

func validModernID(raw json.RawMessage) bool {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return false
	}
	if trimmed[0] == '"' {
		var value string
		return json.Unmarshal(trimmed, &value) == nil
	}
	var integer big.Int
	if _, ok := integer.SetString(string(trimmed), 10); !ok {
		return false
	}
	return true
}

func (s *Server) handleModernRequest(ctx context.Context, req modernRequest) (resp response, ok bool) {
	if req.notification {
		defer func() {
			if ok {
				resp = response{}
				ok = false
			}
		}()
	}
	if req.version != modernProtocolVersion {
		return modernError(req.ID, -32022, "unsupported protocol version", map[string]interface{}{
			"supported": []string{modernProtocolVersion},
			"requested": req.version,
		}), true
	}
	if req.notification {
		switch req.Method {
		case "server/discover", "tools/list", "resources/list", "resources/templates/list", "resources/read", "prompts/list", "prompts/get", "tools/call":
			// Dispatch below for validation and side effects; no response is emitted.
		default:
			return response{}, false
		}
	}

	switch req.Method {
	case "server/discover":
		return modernSuccess(req.ID, modernDiscoveryResult(), true), !req.notification
	case "tools/list":
		return modernSuccess(req.ID, map[string]interface{}{
			"tools": filterToolDefinitions(toolDefinitions(), s.capabilities),
		}, true), !req.notification
	case "resources/list":
		return modernSuccess(req.ID, s.handleResourcesList(), true), !req.notification
	case "resources/templates/list":
		return modernSuccess(req.ID, s.handleResourceTemplatesList(), true), !req.notification
	case "resources/read":
		result, err := s.handleResourceRead(ctx, req.Params)
		if err != nil {
			return modernResourceError(req.ID, req.params, err.Error()), true
		}
		return modernSuccess(req.ID, result, true), !req.notification
	case "prompts/list":
		return modernSuccess(req.ID, s.handlePromptsList(), true), !req.notification
	case "prompts/get":
		result, err := s.handlePromptGet(req.Params)
		if err != nil {
			return modernError(req.ID, -32602, err.Error(), nil), true
		}
		return modernSuccess(req.ID, result, false), !req.notification
	case "tools/call":
		var args struct {
			Name string `json:"name"`
		}
		if err := json.Unmarshal(req.Params, &args); err != nil || strings.TrimSpace(args.Name) == "" {
			return modernError(req.ID, -32602, "tools/call requires a name", nil), true
		}
		result, err := s.handleToolCall(ctx, req.Params)
		if err != nil {
			var unknown unknownToolError
			if errors.As(err, &unknown) {
				return modernError(req.ID, -32602, err.Error(), nil), true
			}
			return modernSuccess(req.ID, toolErrorResult(err), false), !req.notification
		}
		return modernSuccess(req.ID, result, false), !req.notification
	case "initialize", "notifications/initialized", "ping":
		if req.notification {
			return response{}, false
		}
		return modernError(req.ID, -32601, "method not found", nil), true
	default:
		if req.notification {
			return response{}, false
		}
		return modernError(req.ID, -32601, "method not found", nil), true
	}
}

func modernDiscoveryResult() map[string]interface{} {
	return map[string]interface{}{
		"supportedVersions": []string{modernProtocolVersion},
		"capabilities": map[string]interface{}{
			"tools":     map[string]interface{}{},
			"resources": map[string]interface{}{},
			"prompts":   map[string]interface{}{},
		},
		"instructions": "dbrain exposes read-only tools, prompts, and resources backed by the local brain.",
	}
}

func modernSuccess(id json.RawMessage, payload map[string]interface{}, cacheable bool) response {
	result := make(map[string]interface{}, len(payload)+4)
	for key, value := range payload {
		result[key] = value
	}
	result["resultType"] = "complete"
	result["_meta"] = map[string]interface{}{
		modernServerInfoKey: map[string]string{
			"name":    "dbrain",
			"version": serverVersion(),
		},
	}
	if cacheable {
		result["ttlMs"] = int64(0)
		result["cacheScope"] = "private"
	}
	return response{JSONRPC: "2.0", ID: id, Result: result}
}

func modernError(id json.RawMessage, code int, message string, data interface{}) response {
	return response{
		JSONRPC: "2.0",
		ID:      id,
		Error: &responseError{
			Code:    code,
			Message: message,
			Data:    data,
		},
	}
}

func modernResourceError(id json.RawMessage, params map[string]json.RawMessage, message string) response {
	var data interface{}
	if uri, ok := rawString(params["uri"]); ok && uri != "" {
		data = map[string]string{"uri": uri}
	}
	return modernError(id, -32602, message, data)
}

func (s *Server) processModernHTTPPayload(ctx context.Context, payload []byte, headers http.Header) (interface{}, bool, int) {
	req, invalid := decodeModernRequest(payload)
	if invalid != nil {
		if modernNotificationPayload(payload) {
			return nil, false, http.StatusAccepted
		}
		return *invalid, true, http.StatusBadRequest
	}
	if invalid := validateModernHTTPHeaders(headers, req); invalid != nil {
		if req.notification {
			return nil, false, http.StatusAccepted
		}
		return *invalid, true, http.StatusBadRequest
	}
	result, ok := s.handleModernRequest(ctx, req)
	if !ok {
		return nil, false, http.StatusAccepted
	}
	status := http.StatusOK
	if result.Error != nil {
		if result.Error.Code == -32601 {
			status = http.StatusNotFound
		} else {
			status = http.StatusBadRequest
		}
	}
	return result, true, status
}

func validateModernHTTPHeaders(headers http.Header, req modernRequest) *response {
	if !acceptsModernHTTP(headers.Values("Accept")) {
		resp := modernError(req.ID, -32020, "Header mismatch: Accept must include application/json and text/event-stream", nil)
		return &resp
	}
	version, ok := singleHeaderValue(headers, modernHeaderVersion)
	if !ok || !safeHTTPHeaderValue(version) || version != req.version {
		resp := modernError(req.ID, -32020, "Header mismatch: MCP-Protocol-Version does not match request metadata", nil)
		return &resp
	}
	method, ok := singleHeaderValue(headers, modernHeaderMethod)
	if !ok || !safeHTTPHeaderValue(method) || method != req.Method {
		resp := modernError(req.ID, -32020, "Header mismatch: Mcp-Method does not match request method", nil)
		return &resp
	}
	if expected, needsName := modernNameParameter(req.Method, req.params); needsName {
		headerName, ok := singleHeaderValue(headers, modernHeaderName)
		if !ok {
			resp := modernError(req.ID, -32020, "Header mismatch: Mcp-Name is required", nil)
			return &resp
		}
		decoded, valid := decodeMCPHeaderValue(headerName)
		if !valid || decoded != expected {
			resp := modernError(req.ID, -32020, "Header mismatch: Mcp-Name does not match request parameters", nil)
			return &resp
		}
	}
	return nil
}

func acceptsModernHTTP(values []string) bool {
	if len(values) == 0 {
		return false
	}
	var jsonOK, sseOK bool
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			mediaType := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
			switch mediaType {
			case "application/json":
				jsonOK = true
			case "text/event-stream":
				sseOK = true
			}
		}
	}
	return jsonOK && sseOK
}

func singleHeaderValue(headers http.Header, key string) (string, bool) {
	values := headers.Values(key)
	if len(values) != 1 || strings.TrimSpace(values[0]) == "" {
		return "", false
	}
	return values[0], true
}

func safeHTTPHeaderValue(value string) bool {
	for _, char := range []byte(value) {
		if char == 0x7f || char < 0x20 && char != '\t' || char > 0x7e {
			return false
		}
	}
	return strings.TrimSpace(value) == value
}

func decodeMCPHeaderValue(value string) (string, bool) {
	if !safeHTTPHeaderValue(value) {
		return "", false
	}
	hasPrefix := strings.HasPrefix(value, modernHeaderSentinelPrefix)
	hasSuffix := strings.HasSuffix(value, modernHeaderSentinelSuffix)
	if hasPrefix || hasSuffix {
		if !hasPrefix || !hasSuffix {
			return "", false
		}
		encoded := strings.TrimSuffix(strings.TrimPrefix(value, modernHeaderSentinelPrefix), modernHeaderSentinelSuffix)
		decoded, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return "", false
		}
		return string(decoded), true
	}
	return value, true
}

func modernNameParameter(method string, params map[string]json.RawMessage) (string, bool) {
	var key string
	switch method {
	case "tools/call", "prompts/get":
		key = "name"
	case "resources/read":
		key = "uri"
	default:
		return "", false
	}
	value, ok := rawString(params[key])
	if !ok || value == "" {
		return "", true
	}
	return value, true
}
