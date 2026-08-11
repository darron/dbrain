package mcpserver

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/darron/dbrain/internal/config"
	"github.com/darron/dbrain/internal/store"
)

const modernProtocolTestVersion = "2026-07-28"

func TestModernStdioDiscoveryAndResultContracts(t *testing.T) {
	server := modernTestServer(t)
	input := modernLineRequest("discover-1", "server/discover", nil) +
		modernLineRequest(2, "tools/list", nil)

	var output bytes.Buffer
	if err := server.Serve(t.Context(), strings.NewReader(input), &output); err != nil {
		t.Fatalf("serve modern stdio: %v", err)
	}
	responses := parseResponses(t, output.Bytes())
	if len(responses) != 2 {
		t.Fatalf("modern response count = %d, want 2", len(responses))
	}

	discover := modernResult(t, responses[0])
	if discover["resultType"] != "complete" || discover["ttlMs"] != float64(0) || discover["cacheScope"] != "private" {
		t.Fatalf("discovery envelope = %#v", discover)
	}
	versions, ok := discover["supportedVersions"].([]interface{})
	if !ok || len(versions) != 1 || versions[0] != modernProtocolTestVersion {
		t.Fatalf("discovery supportedVersions = %#v", discover["supportedVersions"])
	}
	serverMeta := discover["_meta"].(map[string]interface{})["io.modelcontextprotocol/serverInfo"].(map[string]interface{})
	if serverMeta["name"] != "dbrain" || strings.TrimSpace(serverMeta["version"].(string)) == "" {
		t.Fatalf("discovery serverInfo = %#v", serverMeta)
	}

	tools := modernResult(t, responses[1])
	if tools["resultType"] != "complete" || tools["cacheScope"] != "private" {
		t.Fatalf("tools/list envelope = %#v", tools)
	}
	if _, ok := tools["tools"].([]interface{}); !ok {
		t.Fatalf("tools/list payload = %#v", tools)
	}

	var notificationOutput bytes.Buffer
	notification := `{"jsonrpc":"2.0","method":"tools/list","params":` + modernParamsJSON(nil) + "}\n"
	if err := server.Serve(t.Context(), strings.NewReader(notification), &notificationOutput); err != nil {
		t.Fatalf("serve modern notification: %v", err)
	}
	if notificationOutput.Len() != 0 {
		t.Fatalf("modern notification emitted %q", notificationOutput.String())
	}
}

func TestModernStdioValidationIsFailClosed(t *testing.T) {
	server := modernTestServer(t)
	tests := []struct {
		name string
		body string
		code int
	}{
		{
			name: "partial metadata",
			body: `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"` + modernProtocolTestVersion + `"}}}`,
			code: -32602,
		},
		{
			name: "client response is invalid input",
			body: `{"jsonrpc":"2.0","id":1,"result":{}}`,
			code: -32600,
		},
		{
			name: "array is not a modern batch",
			body: "[" + modernRequestJSON(1, "tools/list", nil) + "," + modernRequestJSON(2, "tools/list", nil) + "]",
			code: -32600,
		},
		{
			name: "null id is invalid",
			body: modernRequestJSONWithID(`null`, "tools/list", nil),
			code: -32600,
		},
		{
			name: "modern initialize does not use legacy dispatch",
			body: modernRequestJSON(3, "initialize", nil),
			code: -32601,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, ok := server.processPayload(t.Context(), []byte(test.body))
			if !ok {
				t.Fatal("modern validation did not return an error response")
			}
			response, ok := result.(response)
			if !ok {
				t.Fatalf("modern validation result type = %T, want response", result)
			}
			if response.Error == nil || response.Error.Code != test.code {
				t.Fatalf("modern validation response = %#v, want code %d", response, test.code)
			}
		})
	}

	legacy, ok := server.processPayload(t.Context(), testPingBatch(t, 2, false))
	if !ok {
		t.Fatal("markerless legacy batch did not respond")
	}
	responses, ok := legacy.([]response)
	if !ok || len(responses) != 2 {
		t.Fatalf("markerless batch result = %#v", legacy)
	}
	legacyInit, ok := server.processPayload(t.Context(), []byte(`{"jsonrpc":"2.0","id":9,"method":"initialize","params":{}}`))
	if !ok || legacyInit.(response).Error != nil {
		t.Fatalf("markerless initialize = %#v", legacyInit)
	}
}

func TestLegacyMetadataDoesNotSelectModernDispatch(t *testing.T) {
	server := modernTestServer(t)
	legacyRequests := []struct {
		name string
		body string
	}{
		{
			name: "initialize",
			body: `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","_meta":{"progressToken":"init"}}}`,
		},
		{
			name: "tools/list",
			body: `{"jsonrpc":"2.0","id":2,"method":"tools/list","params":{"_meta":{"progressToken":"list"}}}`,
		},
		{
			name: "tools/call",
			body: `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"dbrain_stats_backlog","arguments":{},"_meta":{"progressToken":"call"}}}`,
		},
		{
			name: "ping",
			body: `{"jsonrpc":"2.0","id":4,"method":"ping","params":{"_meta":{"progressToken":"ping"}}}`,
		},
	}

	for _, test := range legacyRequests {
		t.Run(test.name, func(t *testing.T) {
			result, ok := server.processPayload(t.Context(), []byte(test.body))
			if !ok {
				t.Fatal("legacy request did not return a response")
			}
			response, ok := result.(response)
			if !ok {
				t.Fatalf("legacy result type = %T, want response", result)
			}
			if response.Error != nil {
				t.Fatalf("legacy request was modern-validated: %#v", response.Error)
			}
			if test.name == "initialize" {
				initResult, ok := response.Result.(map[string]interface{})
				if !ok || initResult["protocolVersion"] != protocolVersion {
					t.Fatalf("legacy initialize result = %#v, want protocolVersion %q", response.Result, protocolVersion)
				}
			}
			if test.name == "tools/call" {
				if _, ok := response.Result.(map[string]interface{}); !ok {
					t.Fatalf("legacy tools/call result = %#v, want executed tool result", response.Result)
				}
			}
		})
	}

	notification := []byte(`{"jsonrpc":"2.0","method":"notifications/cancelled","params":{"requestId":1,"_meta":{"progressToken":"cancel"}}}`)
	if result, ok := server.processPayload(t.Context(), notification); ok {
		t.Fatalf("legacy notification emitted a response: %#v", result)
	}

	batch := []byte(`[{"jsonrpc":"2.0","id":5,"method":"ping","params":{"_meta":{"progressToken":"batch"}}},{"jsonrpc":"2.0","id":6,"method":"tools/list","params":{}}]`)
	result, ok := server.processPayload(t.Context(), batch)
	if !ok {
		t.Fatal("legacy batch did not return responses")
	}
	responses, ok := result.([]response)
	if !ok || len(responses) != 2 {
		t.Fatalf("legacy batch result = %#v, want two responses", result)
	}
	for _, response := range responses {
		if response.Error != nil {
			t.Fatalf("legacy batch response = %#v", response.Error)
		}
	}
}

func TestModernNotificationsWithValidationErrorsDoNotRespond(t *testing.T) {
	server := modernTestServer(t)
	tests := []struct {
		name    string
		body    string
		headers map[string]string
	}{
		{
			name: "missing client capabilities",
			body: `{"jsonrpc":"2.0","method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2026-07-28"}}}`,
			headers: map[string]string{
				"MCP-Protocol-Version": modernProtocolTestVersion,
				"Mcp-Method":           "tools/list",
			},
		},
		{
			name: "unsupported version",
			body: `{"jsonrpc":"2.0","method":"tools/list","params":{"_meta":{"io.modelcontextprotocol/protocolVersion":"2099-01-01","io.modelcontextprotocol/clientCapabilities":{}}}}`,
			headers: map[string]string{
				"MCP-Protocol-Version": "2099-01-01",
				"Mcp-Method":           "tools/list",
			},
		},
	}
	handler := server.HTTPHandler(HTTPOptions{Path: "/mcp"})
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if result, ok := server.processPayload(t.Context(), []byte(test.body)); ok {
				t.Fatalf("modern stdio notification emitted a response: %#v", result)
			}

			recorder := modernHTTPResponse(t, handler, test.body, test.headers)
			if recorder.Code != http.StatusAccepted || recorder.Body.Len() != 0 {
				t.Fatalf("modern notification = %d/%q, want 202 with no body", recorder.Code, recorder.Body.String())
			}
		})
	}

	missingAccept := modernHTTPWithAccept(t, handler, tests[0].body, tests[0].headers, false)
	if missingAccept.Code != http.StatusAccepted || missingAccept.Body.Len() != 0 {
		t.Fatalf("modern notification with invalid headers = %d/%q, want 202 with no body", missingAccept.Code, missingAccept.Body.String())
	}
}

func TestLegacyHTTPProtocolVersionHeadersDoNotSelectModern(t *testing.T) {
	server := modernTestServer(t)
	handler := server.HTTPHandler(HTTPOptions{Path: "/mcp"})
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`
	for _, version := range []string{"2025-03-26", "2025-06-18"} {
		t.Run(version, func(t *testing.T) {
			recorder := modernHTTPResponse(t, handler, body, map[string]string{
				"MCP-Protocol-Version": version,
			})
			if recorder.Code != http.StatusOK {
				t.Fatalf("legacy HTTP status = %d: %s", recorder.Code, recorder.Body.String())
			}
			var response map[string]interface{}
			if err := json.Unmarshal(recorder.Body.Bytes(), &response); err != nil {
				t.Fatalf("decode legacy HTTP response: %v", err)
			}
			if response["error"] != nil {
				t.Fatalf("legacy HTTP response = %#v", response)
			}
		})
	}
}

func TestModernVersionHeaderAloneSelectsModern(t *testing.T) {
	server := modernTestServer(t)
	handler := server.HTTPHandler(HTTPOptions{Path: "/mcp"})
	recorder := modernHTTPResponse(t, handler,
		`{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`,
		map[string]string{"MCP-Protocol-Version": modernProtocolTestVersion})
	assertModernHTTPError(t, recorder, http.StatusBadRequest, -32602)
}

func TestModernHTTPHeaderAndStatusMatrix(t *testing.T) {
	server := modernTestServer(t)
	handler := server.HTTPHandler(HTTPOptions{Path: "/mcp"})

	valid := modernHTTPRequest(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":`+modernParamsJSON(nil)+`}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "tools/list",
	})
	if valid.Code != http.StatusOK {
		t.Fatalf("valid modern status = %d: %s", valid.Code, valid.Body.String())
	}
	validBody := decodeModernHTTPBody(t, valid)
	if modernResult(t, validBody)["resultType"] != "complete" {
		t.Fatalf("valid modern body = %#v", validBody)
	}

	missingMethod := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":`+modernParamsJSON(nil)+`}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
	})
	assertModernHTTPError(t, missingMethod, http.StatusBadRequest, -32020)

	versionMismatch := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":`+modernParamsJSON(nil)+`}`, map[string]string{
		"MCP-Protocol-Version": "2099-01-01",
		"Mcp-Method":           "tools/list",
	})
	assertModernHTTPError(t, versionMismatch, http.StatusBadRequest, -32020)

	methodMismatch := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":`+modernParamsJSON(nil)+`}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "resources/list",
	})
	assertModernHTTPError(t, methodMismatch, http.StatusBadRequest, -32020)

	missingAccept := modernHTTPWithAccept(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":`+modernParamsJSON(nil)+`}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "tools/list",
	}, false)
	assertModernHTTPError(t, missingAccept, http.StatusBadRequest, -32020)

	missingMetadata := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "tools/list",
	})
	assertModernHTTPError(t, missingMetadata, http.StatusBadRequest, -32602)

	clientResponse := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","id":1,"result":{}}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "tools/list",
	})
	assertModernHTTPError(t, clientResponse, http.StatusBadRequest, -32600)

	scalarParams := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":"bad"}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "tools/list",
	})
	assertModernHTTPError(t, scalarParams, http.StatusBadRequest, -32600)

	unsupported := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":`+modernParamsJSONForVersion("2099-01-01")+`}`, map[string]string{
		"MCP-Protocol-Version": "2099-01-01",
		"Mcp-Method":           "tools/list",
	})
	assertModernHTTPError(t, unsupported, http.StatusBadRequest, -32022)
	unsupportedError := decodeModernHTTPBody(t, unsupported)["error"].(map[string]interface{})
	unsupportedData := unsupportedError["data"].(map[string]interface{})
	if unsupportedData["requested"] != "2099-01-01" {
		t.Fatalf("unsupported data = %#v", unsupportedData)
	}

	unknown := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","id":1,"method":"does/not-exist","params":`+modernParamsJSON(nil)+`}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "does/not-exist",
	})
	assertModernHTTPError(t, unknown, http.StatusNotFound, -32601)

	promptName := "brain_research"
	promptBody := `{"jsonrpc":"2.0","id":1,"method":"prompts/get","params":` + modernParamsJSON(map[string]interface{}{
		"name":      promptName,
		"arguments": map[string]interface{}{"question": "What is in the brain?"},
	}) + `}`
	encodedName := "=?base64?" + base64.StdEncoding.EncodeToString([]byte(promptName)) + "?="
	prompt := modernHTTPResponse(t, handler, promptBody, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "prompts/get",
		"Mcp-Name":             encodedName,
	})
	if prompt.Code != http.StatusOK {
		t.Fatalf("base64 prompts/get status = %d: %s", prompt.Code, prompt.Body.String())
	}
	if modernResult(t, decodeModernHTTPBody(t, prompt))["resultType"] != "complete" {
		t.Fatalf("base64 prompts/get body = %#v", decodeModernHTTPBody(t, prompt))
	}
	nameMismatch := modernHTTPResponse(t, handler, promptBody, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "prompts/get",
		"Mcp-Name":             "brain_topic_map",
	})
	assertModernHTTPError(t, nameMismatch, http.StatusBadRequest, -32020)

	resourceURI := "dbrain://mcp/overview"
	resourceBody := `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":` + modernParamsJSON(map[string]interface{}{"uri": resourceURI}) + `}`
	resource := modernHTTPResponse(t, handler, resourceBody, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "resources/read",
		"Mcp-Name":             resourceURI,
	})
	if resource.Code != http.StatusOK {
		t.Fatalf("resource read status = %d: %s", resource.Code, resource.Body.String())
	}

	invalidResource := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","id":1,"method":"resources/read","params":`+modernParamsJSON(map[string]interface{}{"uri": "dbrain://unknown/thing"})+`}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "resources/read",
		"Mcp-Name":             "dbrain://unknown/thing",
	})
	assertModernHTTPError(t, invalidResource, http.StatusBadRequest, -32602)
	resourceError := decodeModernHTTPBody(t, invalidResource)["error"].(map[string]interface{})
	resourceData := resourceError["data"].(map[string]interface{})
	if resourceData["uri"] != "dbrain://unknown/thing" {
		t.Fatalf("resource error data = %#v", resourceData)
	}

	httpNotification := modernHTTPResponse(t, handler, `{"jsonrpc":"2.0","method":"tools/list","params":`+modernParamsJSON(nil)+`}`, map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "tools/list",
	})
	if httpNotification.Code != http.StatusAccepted || httpNotification.Body.Len() != 0 {
		t.Fatalf("modern HTTP notification = %d/%q", httpNotification.Code, httpNotification.Body.String())
	}

	for _, method := range []string{http.MethodGet, http.MethodDelete} {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, "/mcp", nil)
		handler.ServeHTTP(recorder, req)
		if recorder.Code != http.StatusMethodNotAllowed {
			t.Fatalf("%s status = %d, want 405", method, recorder.Code)
		}
	}

	modernBatch := modernHTTPResponse(t, handler, "["+modernRequestJSON(1, "tools/list", nil)+"]", map[string]string{
		"MCP-Protocol-Version": modernProtocolTestVersion,
		"Mcp-Method":           "tools/list",
	})
	assertModernHTTPError(t, modernBatch, http.StatusBadRequest, -32600)

	options := httptest.NewRecorder()
	handler.ServeHTTP(options, httptest.NewRequest(http.MethodOptions, "/mcp", nil))
	if options.Code != http.StatusNoContent || !strings.Contains(options.Header().Get("Access-Control-Allow-Headers"), "Mcp-Name") {
		t.Fatalf("OPTIONS status/headers = %d/%q", options.Code, options.Header().Get("Access-Control-Allow-Headers"))
	}
}

func modernTestServer(t *testing.T) *Server {
	t.Helper()
	root := t.TempDir()
	cfg, err := config.Load(root)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatalf("ensure dirs: %v", err)
	}
	st, err := store.Open(cfg.DBPath)
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return New(cfg, st)
}

func modernLineRequest(id interface{}, method string, extra map[string]interface{}) string {
	return modernRequestJSON(id, method, extra) + "\n"
}

func modernRequestJSON(id interface{}, method string, extra map[string]interface{}) string {
	params := modernParams(extra)
	return modernRequestJSONWithParams(id, method, mustJSONForModernTest(params))
}

func modernRequestJSONWithParams(id interface{}, method string, params string) string {
	idJSON := mustJSONForModernTest(id)
	return modernRequestJSONWithIDAndParams(idJSON, method, params)
}

func modernRequestJSONWithID(idJSON string, method string, extra map[string]interface{}) string {
	return modernRequestJSONWithIDAndParams(idJSON, method, mustJSONForModernTest(modernParams(extra)))
}

func modernRequestJSONWithIDAndParams(idJSON string, method string, params string) string {
	return `{"jsonrpc":"2.0","id":` + idJSON + `,"method":"` + method + `","params":` + params + `}`
}

func modernParams(extra map[string]interface{}) map[string]interface{} {
	meta := map[string]interface{}{
		"io.modelcontextprotocol/protocolVersion":    modernProtocolTestVersion,
		"io.modelcontextprotocol/clientCapabilities": map[string]interface{}{},
	}
	params := map[string]interface{}{"_meta": meta}
	for key, value := range extra {
		params[key] = value
	}
	return params
}

func modernParamsForVersion(version string) string {
	meta := map[string]interface{}{
		"io.modelcontextprotocol/protocolVersion":    version,
		"io.modelcontextprotocol/clientCapabilities": map[string]interface{}{},
	}
	return mustJSONForModernTest(map[string]interface{}{"_meta": meta})
}

func modernParamsJSON(extra map[string]interface{}) string {
	return mustJSONForModernTest(modernParams(extra))
}

func modernParamsJSONForVersion(version string) string {
	return modernParamsForVersion(version)
}

func modernHTTPRequest(t *testing.T, handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json, text/event-stream")
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	handler.ServeHTTP(recorder, request)
	return recorder
}

func modernHTTPResponse(t *testing.T, handler http.Handler, body string, headers map[string]string) *httptest.ResponseRecorder {
	return modernHTTPWithAccept(t, handler, body, headers, true)
}

func modernHTTPWithAccept(t *testing.T, handler http.Handler, body string, headers map[string]string, accept bool) *httptest.ResponseRecorder {
	t.Helper()
	recorder := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/mcp", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	if accept {
		request.Header.Set("Accept", "application/json, text/event-stream")
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	handler.ServeHTTP(recorder, request)
	return recorder
}

func decodeModernHTTPBody(t *testing.T, recorder *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode modern HTTP body: %v: %s", err, recorder.Body.String())
	}
	return body
}

func modernResult(t *testing.T, response map[string]interface{}) map[string]interface{} {
	t.Helper()
	if response["error"] != nil {
		t.Fatalf("modern response error = %#v", response)
	}
	result, ok := response["result"].(map[string]interface{})
	if !ok {
		t.Fatalf("modern response result = %#v", response)
	}
	return result
}

func assertModernHTTPError(t *testing.T, recorder *httptest.ResponseRecorder, status int, code int) {
	t.Helper()
	if recorder.Code != status {
		t.Fatalf("modern HTTP status = %d, want %d: %s", recorder.Code, status, recorder.Body.String())
	}
	body := decodeModernHTTPBody(t, recorder)
	errorBody, ok := body["error"].(map[string]interface{})
	if !ok || int(errorBody["code"].(float64)) != code {
		t.Fatalf("modern HTTP error = %#v, want code %d", body, code)
	}
}

func mustJSONForModernTest(value interface{}) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}
