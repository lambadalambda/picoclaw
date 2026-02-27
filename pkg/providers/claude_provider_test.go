package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	anthropicoption "github.com/anthropics/anthropic-sdk-go/option"
)

func TestBuildClaudeParams_BasicMessage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hello"},
	}
	params, err := buildClaudeParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{
		"max_tokens": 1024,
	})
	if err != nil {
		t.Fatalf("buildClaudeParams() error: %v", err)
	}
	if string(params.Model) != "claude-sonnet-4-5-20250929" {
		t.Errorf("Model = %q, want %q", params.Model, "claude-sonnet-4-5-20250929")
	}
	if params.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %d, want 1024", params.MaxTokens)
	}
	if len(params.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(params.Messages))
	}
}

func TestBuildClaudeParams_EncodesInlineImagePart(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input.png")
	if err := os.WriteFile(imagePath, []byte("image-bytes"), 0644); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}

	messages := []Message{
		{
			Role:    "user",
			Content: "Describe this",
			Parts: []MessagePart{
				{Type: MessagePartTypeImage, Path: imagePath},
			},
		},
	}

	params, err := buildClaudeParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildClaudeParams() error: %v", err)
	}

	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal(params) error: %v", err)
	}

	var payload map[string]interface{}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal(payload) error: %v", err)
	}

	msgs, ok := payload["messages"].([]interface{})
	if !ok || len(msgs) != 1 {
		t.Fatalf("payload.messages = %#v, want 1 message", payload["messages"])
	}
	first, ok := msgs[0].(map[string]interface{})
	if !ok {
		t.Fatalf("payload.messages[0] has unexpected type: %T", msgs[0])
	}
	content, ok := first["content"].([]interface{})
	if !ok || len(content) != 2 {
		t.Fatalf("payload.messages[0].content = %#v, want 2 blocks", first["content"])
	}

	textBlock, ok := content[0].(map[string]interface{})
	if !ok || textBlock["type"] != "text" || textBlock["text"] != "Describe this" {
		t.Fatalf("unexpected text block: %#v", content[0])
	}

	imageBlock, ok := content[1].(map[string]interface{})
	if !ok || imageBlock["type"] != "image" {
		t.Fatalf("unexpected image block: %#v", content[1])
	}
	source, ok := imageBlock["source"].(map[string]interface{})
	if !ok {
		t.Fatalf("image source shape invalid: %#v", imageBlock["source"])
	}
	if source["type"] != "base64" {
		t.Fatalf("image source type = %#v, want base64", source["type"])
	}
	if source["media_type"] != "image/png" {
		t.Fatalf("image source media_type = %#v, want image/png", source["media_type"])
	}
	data, _ := source["data"].(string)
	if data == "" || strings.HasPrefix(data, "data:") {
		t.Fatalf("image source data should be raw base64, got: %q", data)
	}
}

func TestBuildClaudeParams_ToolResultWithImagePartsEmbedsInlineImage(t *testing.T) {
	tmpDir := t.TempDir()
	imagePath := filepath.Join(tmpDir, "input.png")
	if err := os.WriteFile(imagePath, []byte("image-bytes"), 0644); err != nil {
		t.Fatalf("write image fixture: %v", err)
	}

	messages := []Message{
		{Role: "user", Content: "hi"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "image_inspect", Arguments: map[string]interface{}{}},
			},
		},
		{
			Role:       "tool",
			ToolCallID: "call_1",
			Content:    "Image(s) attached.",
			Parts:      []MessagePart{{Type: MessagePartTypeImage, Path: imagePath}},
		},
	}

	params, err := buildClaudeParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildClaudeParams() error: %v", err)
	}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal(params) error: %v", err)
	}
	payload := string(encoded)
	if !strings.Contains(payload, `"type":"tool_result"`) {
		t.Fatalf("expected tool_result block, got: %s", payload)
	}
	if !strings.Contains(payload, `"type":"image"`) {
		t.Fatalf("expected inline image in tool_result content, got: %s", payload)
	}
	if !strings.Contains(payload, `"media_type":"image/png"`) {
		t.Fatalf("expected image/png media_type, got: %s", payload)
	}
}

func TestBuildClaudeParams_SystemMessage(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hi"},
	}
	params, err := buildClaudeParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildClaudeParams() error: %v", err)
	}
	if len(params.System) != 1 {
		t.Fatalf("len(System) = %d, want 1", len(params.System))
	}
	if params.System[0].Text != "You are helpful" {
		t.Errorf("System[0].Text = %q, want %q", params.System[0].Text, "You are helpful")
	}
	if len(params.Messages) != 1 {
		t.Fatalf("len(Messages) = %d, want 1", len(params.Messages))
	}
}

func TestBuildClaudeParams_ToolCallMessage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{
					ID:        "call_1",
					Name:      "get_weather",
					Arguments: map[string]interface{}{"city": "SF"},
				},
			},
		},
		{Role: "tool", Content: `{"temp": 72}`, ToolCallID: "call_1"},
	}
	params, err := buildClaudeParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildClaudeParams() error: %v", err)
	}
	if len(params.Messages) != 3 {
		t.Fatalf("len(Messages) = %d, want 3", len(params.Messages))
	}
}

func TestBuildClaudeParams_ToolResultMarksIsError(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role:    "assistant",
			Content: "",
			ToolCalls: []ToolCall{
				{
					ID:        "call_1",
					Name:      "get_weather",
					Arguments: map[string]interface{}{"city": "SF"},
				},
			},
		},
		{Role: "tool", Content: "Error: boom", ToolCallID: "call_1"},
	}
	params, err := buildClaudeParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildClaudeParams() error: %v", err)
	}

	b, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal(params) error: %v", err)
	}
	if !strings.Contains(string(b), `"is_error":true`) {
		t.Fatalf("expected is_error=true in tool_result JSON, got: %s", string(b))
	}
}

func TestBuildClaudeParams_WithTools(t *testing.T) {
	tools := []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name:        "get_weather",
				Description: "Get weather for a city",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"city": map[string]interface{}{"type": "string"},
					},
					"required": []interface{}{"city"},
				},
			},
		},
	}
	params, err := buildClaudeParams([]Message{{Role: "user", Content: "Hi"}}, tools, "claude-sonnet-4-5-20250929", map[string]interface{}{})
	if err != nil {
		t.Fatalf("buildClaudeParams() error: %v", err)
	}
	if len(params.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(params.Tools))
	}
}

func TestBuildClaudeParams_AnthropicCacheControlTopLevel(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hi"},
	}

	params, err := buildClaudeParams(messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{
		"anthropic_cache_ttl": "1h",
	})
	if err != nil {
		t.Fatalf("buildClaudeParams() error: %v", err)
	}

	var payload map[string]interface{}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal(params) error: %v", err)
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal payload error: %v", err)
	}

	cacheControl, ok := payload["cache_control"].(map[string]interface{})
	if !ok {
		t.Fatalf("cache_control = %#v, want object", payload["cache_control"])
	}
	if got, ok := cacheControl["type"].(string); !ok || got != "ephemeral" {
		t.Fatalf("cache_control.type = %#v, want ephemeral", cacheControl["type"])
	}
	if got, ok := cacheControl["ttl"].(string); !ok || got != "1h" {
		t.Fatalf("cache_control.ttl = %#v, want 1h", cacheControl["ttl"])
	}

	if systems, ok := payload["system"].([]interface{}); ok && len(systems) > 0 {
		if first, ok := systems[0].(map[string]interface{}); ok {
			if _, has := first["cache_control"]; has {
				t.Fatalf("system[0].cache_control should be omitted in automatic mode")
			}
		}
	}
	if msgs, ok := payload["messages"].([]interface{}); ok && len(msgs) > 0 {
		if firstMsg, ok := msgs[0].(map[string]interface{}); ok {
			if content, ok := firstMsg["content"].([]interface{}); ok && len(content) > 0 {
				if firstBlock, ok := content[0].(map[string]interface{}); ok {
					if _, has := firstBlock["cache_control"]; has {
						t.Fatalf("messages[0].content[0].cache_control should be omitted in automatic mode")
					}
				}
			}
		}
	}
}

func TestBuildClaudeParams_AnthropicCacheControlRejectsInvalidTTL(t *testing.T) {
	_, err := buildClaudeParams([]Message{{Role: "user", Content: "Hi"}}, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{
		"anthropic_cache_ttl": "2h",
	})
	if err == nil {
		t.Fatal("buildClaudeParams() error = nil, want invalid anthropic_cache_ttl error")
	}
	if !strings.Contains(err.Error(), "anthropic_cache_ttl") {
		t.Fatalf("error = %q, want mention of anthropic_cache_ttl", err)
	}
}

func TestBuildClaudeParams_AnthropicCacheControlTopLevel_DefaultTTL(t *testing.T) {
	params, err := buildClaudeParams([]Message{{Role: "user", Content: "Hi"}}, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{
		"anthropic_cache": true,
	})
	if err != nil {
		t.Fatalf("buildClaudeParams() error: %v", err)
	}

	var payload map[string]interface{}
	encoded, err := json.Marshal(params)
	if err != nil {
		t.Fatalf("json.Marshal(params) error: %v", err)
	}
	if err := json.Unmarshal(encoded, &payload); err != nil {
		t.Fatalf("json.Unmarshal payload error: %v", err)
	}

	cacheControl, ok := payload["cache_control"].(map[string]interface{})
	if !ok {
		t.Fatalf("cache_control = %#v, want object", payload["cache_control"])
	}
	if got, ok := cacheControl["type"].(string); !ok || got != "ephemeral" {
		t.Fatalf("cache_control.type = %#v, want ephemeral", cacheControl["type"])
	}
	if _, has := cacheControl["ttl"]; has {
		t.Fatalf("cache_control.ttl should be omitted when using default TTL")
	}
}

func TestAnthropicCacheHitRatio_ComputesAgainstTotalInput(t *testing.T) {
	ratio, ok := anthropicCacheHitRatio(1153, 39349)
	if !ok {
		t.Fatal("anthropicCacheHitRatio() ok = false, want true")
	}
	if ratio != 0.9715 {
		t.Fatalf("anthropicCacheHitRatio() = %v, want 0.9715", ratio)
	}
}

func TestAnthropicCacheHitRatio_HandlesZeroTotals(t *testing.T) {
	_, ok := anthropicCacheHitRatio(0, 0)
	if ok {
		t.Fatal("anthropicCacheHitRatio() ok = true, want false")
	}
}

func TestTranslateToolsForClaude_RequiredStringSlice(t *testing.T) {
	tools := []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name:        "write_file",
				Description: "Write content",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path":    map[string]interface{}{"type": "string"},
						"content": map[string]interface{}{"type": "string"},
					},
					"required": []string{"path", "content"},
				},
			},
		},
	}

	out := translateToolsForClaude(tools)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].OfTool == nil {
		t.Fatal("tool definition missing")
	}
	if !reflect.DeepEqual(out[0].OfTool.InputSchema.Required, []string{"path", "content"}) {
		t.Fatalf("required = %#v, want [path content]", out[0].OfTool.InputSchema.Required)
	}
}

func TestTranslateToolsForClaude_RequiredInterfaceSlice(t *testing.T) {
	tools := []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name: "edit_file",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"path": map[string]interface{}{"type": "string"},
					},
					"required": []interface{}{"path", "", 42},
				},
			},
		},
	}

	out := translateToolsForClaude(tools)
	if len(out) != 1 {
		t.Fatalf("len(out) = %d, want 1", len(out))
	}
	if out[0].OfTool == nil {
		t.Fatal("tool definition missing")
	}
	if !reflect.DeepEqual(out[0].OfTool.InputSchema.Required, []string{"path"}) {
		t.Fatalf("required = %#v, want [path]", out[0].OfTool.InputSchema.Required)
	}
}

func TestParseClaudeResponse_TextOnly(t *testing.T) {
	resp := &anthropic.Message{
		Content: []anthropic.ContentBlockUnion{},
		Usage: anthropic.Usage{
			InputTokens:  10,
			OutputTokens: 20,
		},
	}
	result := parseClaudeResponse(resp)
	if result.Usage.PromptTokens != 10 {
		t.Errorf("PromptTokens = %d, want 10", result.Usage.PromptTokens)
	}
	if result.Usage.CompletionTokens != 20 {
		t.Errorf("CompletionTokens = %d, want 20", result.Usage.CompletionTokens)
	}
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", result.FinishReason, "stop")
	}
}

func TestParseClaudeResponse_StopReasons(t *testing.T) {
	tests := []struct {
		stopReason anthropic.StopReason
		want       string
	}{
		{anthropic.StopReasonEndTurn, "stop"},
		{anthropic.StopReasonMaxTokens, "length"},
		{anthropic.StopReasonToolUse, "tool_calls"},
	}
	for _, tt := range tests {
		resp := &anthropic.Message{
			StopReason: tt.stopReason,
		}
		result := parseClaudeResponse(resp)
		if result.FinishReason != tt.want {
			t.Errorf("StopReason %q: FinishReason = %q, want %q", tt.stopReason, result.FinishReason, tt.want)
		}
	}
}

func TestClaudeProvider_ChatRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := map[string]interface{}{
			"id":          "msg_test",
			"type":        "message",
			"role":        "assistant",
			"model":       reqBody["model"],
			"stop_reason": "end_turn",
			"content": []map[string]interface{}{
				{"type": "text", "text": "Hello! How can I help you?"},
			},
			"usage": map[string]interface{}{
				"input_tokens":  15,
				"output_tokens": 8,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewClaudeProvider("test-token")
	provider.client = createAnthropicTestClient(server.URL, "test-token")

	messages := []Message{{Role: "user", Content: "Hello"}}
	resp, err := provider.Chat(t.Context(), messages, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "Hello! How can I help you?" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hello! How can I help you?")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage.PromptTokens != 15 {
		t.Errorf("PromptTokens = %d, want 15", resp.Usage.PromptTokens)
	}
}

func TestClaudeProvider_AddsOAuthBetaHeaderForOAuthToken(t *testing.T) {
	oauthToken := "sk-ant-oat01-test-oauth-token"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer "+oauthToken {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		beta := r.Header.Get("Anthropic-Beta")
		if !strings.Contains(beta, "oauth-2025-04-20") {
			http.Error(w, "missing oauth beta", http.StatusBadRequest)
			return
		}

		var reqBody map[string]interface{}
		json.NewDecoder(r.Body).Decode(&reqBody)

		resp := map[string]interface{}{
			"id":          "msg_test",
			"type":        "message",
			"role":        "assistant",
			"model":       reqBody["model"],
			"stop_reason": "end_turn",
			"content": []map[string]interface{}{
				{"type": "text", "text": "ok"},
			},
			"usage": map[string]interface{}{
				"input_tokens":  1,
				"output_tokens": 1,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewClaudeProviderWithTokenSource("ignored", func() (string, error) {
		return oauthToken, nil
	})
	provider.client = createAnthropicTestClient(server.URL, "ignored")

	resp, err := provider.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-opus-4-6", map[string]interface{}{"max_tokens": 8})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("Content = %q, want ok", resp.Content)
	}
}

func TestClaudeProvider_AddsPromptCachingBetaHeaderWhenCachingEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		beta := r.Header.Get("Anthropic-Beta")
		if !strings.Contains(beta, "prompt-caching-2024-07-31") {
			http.Error(w, "missing prompt caching beta", http.StatusBadRequest)
			return
		}

		var reqBody map[string]interface{}
		if err := json.NewDecoder(r.Body).Decode(&reqBody); err != nil {
			http.Error(w, "invalid json", http.StatusBadRequest)
			return
		}
		cacheControl, ok := reqBody["cache_control"].(map[string]interface{})
		if !ok {
			http.Error(w, "missing top-level cache_control", http.StatusBadRequest)
			return
		}
		if got, _ := cacheControl["type"].(string); got != "ephemeral" {
			http.Error(w, "invalid top-level cache_control.type", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"id":          "msg_test",
			"type":        "message",
			"role":        "assistant",
			"model":       "claude-sonnet-4-5-20250929",
			"stop_reason": "end_turn",
			"content": []map[string]interface{}{
				{"type": "text", "text": "ok"},
			},
			"usage": map[string]interface{}{
				"input_tokens":  1,
				"output_tokens": 1,
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewClaudeProvider("test-token")
	provider.client = createAnthropicTestClient(server.URL, "test-token")

	resp, err := provider.Chat(t.Context(), []Message{{Role: "user", Content: "hi"}}, nil, "claude-sonnet-4-5-20250929", map[string]interface{}{
		"max_tokens":      8,
		"anthropic_cache": true,
	})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("Content = %q, want ok", resp.Content)
	}
}

func TestClaudeProvider_GetDefaultModel(t *testing.T) {
	p := NewClaudeProvider("test-token")
	if got := p.GetDefaultModel(); got != "claude-sonnet-4-5-20250929" {
		t.Errorf("GetDefaultModel() = %q, want %q", got, "claude-sonnet-4-5-20250929")
	}
}

func createAnthropicTestClient(baseURL, token string) *anthropic.Client {
	c := anthropic.NewClient(
		anthropicoption.WithAuthToken(token),
		anthropicoption.WithBaseURL(baseURL),
	)
	return &c
}
