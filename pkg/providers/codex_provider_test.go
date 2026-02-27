package providers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/openai/openai-go/v3"
	openaiopt "github.com/openai/openai-go/v3/option"
	"github.com/openai/openai-go/v3/responses"
)

func TestBuildCodexParams_BasicMessage(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "Hello"},
	}
	params := buildCodexParams(messages, nil, "gpt-4o", map[string]interface{}{
		"max_tokens": 2048,
	})
	if params.Model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", params.Model, "gpt-4o")
	}
}

func TestBuildCodexParams_SystemAsInstructions(t *testing.T) {
	messages := []Message{
		{Role: "system", Content: "You are helpful"},
		{Role: "user", Content: "Hi"},
	}
	params := buildCodexParams(messages, nil, "gpt-4o", map[string]interface{}{})
	if !params.Instructions.Valid() {
		t.Fatal("Instructions should be set")
	}
	if params.Instructions.Or("") != "You are helpful" {
		t.Errorf("Instructions = %q, want %q", params.Instructions.Or(""), "You are helpful")
	}
}

func TestBuildCodexParams_ToolCallConversation(t *testing.T) {
	messages := []Message{
		{Role: "user", Content: "What's the weather?"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{
				{ID: "call_1", Name: "get_weather", Arguments: map[string]interface{}{"city": "SF"}},
			},
		},
		{Role: "tool", Content: `{"temp": 72}`, ToolCallID: "call_1"},
	}
	params := buildCodexParams(messages, nil, "gpt-4o", map[string]interface{}{})
	if params.Input.OfInputItemList == nil {
		t.Fatal("Input.OfInputItemList should not be nil")
	}
	if len(params.Input.OfInputItemList) != 3 {
		t.Errorf("len(Input items) = %d, want 3", len(params.Input.OfInputItemList))
	}
}

func TestBuildCodexParams_EncodesInlineImagePart(t *testing.T) {
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

	params := buildCodexParams(messages, nil, "gpt-4o", map[string]interface{}{})
	if params.Input.OfInputItemList == nil || len(params.Input.OfInputItemList) != 1 {
		t.Fatalf("input items = %#v, want 1 user message", params.Input.OfInputItemList)
	}

	first := params.Input.OfInputItemList[0]
	if first.OfMessage == nil {
		t.Fatalf("expected first input item to be a message, got %#v", first)
	}
	contentList := first.OfMessage.Content.OfInputItemContentList
	if len(contentList) != 2 {
		t.Fatalf("len(contentList) = %d, want 2", len(contentList))
	}

	if contentList[0].OfInputText == nil || contentList[0].OfInputText.Text != "Describe this" {
		t.Fatalf("unexpected text content item: %#v", contentList[0])
	}
	if contentList[1].OfInputImage == nil {
		t.Fatalf("expected second content item to be input_image, got %#v", contentList[1])
	}
	imageURL := contentList[1].OfInputImage.ImageURL.Or("")
	if !strings.HasPrefix(imageURL, "data:image/png;base64,") {
		t.Fatalf("input_image.image_url = %q, want data URL prefix", imageURL)
	}
	if string(contentList[1].OfInputImage.Detail) != "auto" {
		t.Fatalf("input_image.detail = %q, want auto", contentList[1].OfInputImage.Detail)
	}
}

func TestBuildCodexParams_ToolResultWithImagePartsEmbedsInlineImage(t *testing.T) {
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

	params := buildCodexParams(messages, nil, "gpt-4o", map[string]interface{}{})
	items := params.Input.OfInputItemList
	if items == nil || len(items) != 3 {
		t.Fatalf("input items = %#v, want 3", items)
	}

	toolOut := items[2]
	if toolOut.OfFunctionCallOutput == nil {
		t.Fatalf("expected third item to be function_call_output, got %#v", toolOut)
	}
	output := toolOut.OfFunctionCallOutput.Output
	if output.OfString.Valid() {
		t.Fatalf("expected structured output items, got string %q", output.OfString.Or(""))
	}
	if len(output.OfResponseFunctionCallOutputItemArray) != 2 {
		t.Fatalf("len(output items) = %d, want 2", len(output.OfResponseFunctionCallOutputItemArray))
	}
	imageItem := output.OfResponseFunctionCallOutputItemArray[1]
	if imageItem.OfInputImage == nil {
		t.Fatalf("expected output item[1] to be input_image, got %#v", imageItem)
	}
	url := imageItem.OfInputImage.ImageURL.Or("")
	if !strings.HasPrefix(url, "data:image/png;base64,") {
		t.Fatalf("output input_image.image_url = %q, want data URL prefix", url)
	}
	if string(imageItem.OfInputImage.Detail) != "auto" {
		t.Fatalf("output input_image.detail = %q, want auto", imageItem.OfInputImage.Detail)
	}
}

func TestBuildCodexParams_WithTools(t *testing.T) {
	tools := []ToolDefinition{
		{
			Type: "function",
			Function: ToolFunctionDefinition{
				Name:        "get_weather",
				Description: "Get weather",
				Parameters: map[string]interface{}{
					"type": "object",
					"properties": map[string]interface{}{
						"city": map[string]interface{}{"type": "string"},
					},
				},
			},
		},
	}
	params := buildCodexParams([]Message{{Role: "user", Content: "Hi"}}, tools, "gpt-4o", map[string]interface{}{})
	if len(params.Tools) != 1 {
		t.Fatalf("len(Tools) = %d, want 1", len(params.Tools))
	}
	if params.Tools[0].OfFunction == nil {
		t.Fatal("Tool should be a function tool")
	}
	if params.Tools[0].OfFunction.Name != "get_weather" {
		t.Errorf("Tool name = %q, want %q", params.Tools[0].OfFunction.Name, "get_weather")
	}
}

func TestBuildCodexParams_StoreIsFalse(t *testing.T) {
	params := buildCodexParams([]Message{{Role: "user", Content: "Hi"}}, nil, "gpt-4o", map[string]interface{}{})
	if !params.Store.Valid() || params.Store.Or(true) != false {
		t.Error("Store should be explicitly set to false")
	}
}

func TestParseCodexResponse_TextOutput(t *testing.T) {
	respJSON := `{
		"id": "resp_test",
		"object": "response",
		"status": "completed",
		"output": [
			{
				"id": "msg_1",
				"type": "message",
				"role": "assistant",
				"status": "completed",
				"content": [
					{"type": "output_text", "text": "Hello there!"}
				]
			}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 5,
			"total_tokens": 15,
			"input_tokens_details": {"cached_tokens": 0},
			"output_tokens_details": {"reasoning_tokens": 0}
		}
	}`

	var resp responses.Response
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	result := parseCodexResponse(&resp)
	if result.Content != "Hello there!" {
		t.Errorf("Content = %q, want %q", result.Content, "Hello there!")
	}
	if result.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", result.FinishReason, "stop")
	}
	if result.Usage.TotalTokens != 15 {
		t.Errorf("TotalTokens = %d, want 15", result.Usage.TotalTokens)
	}
}

func TestParseCodexResponse_FunctionCall(t *testing.T) {
	respJSON := `{
		"id": "resp_test",
		"object": "response",
		"status": "completed",
		"output": [
			{
				"id": "fc_1",
				"type": "function_call",
				"call_id": "call_abc",
				"name": "get_weather",
				"arguments": "{\"city\":\"SF\"}",
				"status": "completed"
			}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 8,
			"total_tokens": 18,
			"input_tokens_details": {"cached_tokens": 0},
			"output_tokens_details": {"reasoning_tokens": 0}
		}
	}`

	var resp responses.Response
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	result := parseCodexResponse(&resp)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	tc := result.ToolCalls[0]
	if tc.Name != "get_weather" {
		t.Errorf("ToolCall.Name = %q, want %q", tc.Name, "get_weather")
	}
	if tc.ID != "call_abc" {
		t.Errorf("ToolCall.ID = %q, want %q", tc.ID, "call_abc")
	}
	if tc.Arguments["city"] != "SF" {
		t.Errorf("ToolCall.Arguments[city] = %v, want SF", tc.Arguments["city"])
	}
	if result.FinishReason != "tool_calls" {
		t.Errorf("FinishReason = %q, want %q", result.FinishReason, "tool_calls")
	}
}

func TestParseCodexResponse_ExtractsToolCallDescription(t *testing.T) {
	respJSON := `{
		"id": "resp_test",
		"object": "response",
		"status": "completed",
		"output": [
			{
				"id": "fc_2",
				"type": "function_call",
				"call_id": "call_desc",
				"name": "exec",
				"arguments": "{\"description\":\"Check git status\",\"command\":\"git status -sb\"}",
				"status": "completed"
			}
		],
		"usage": {
			"input_tokens": 10,
			"output_tokens": 8,
			"total_tokens": 18,
			"input_tokens_details": {"cached_tokens": 0},
			"output_tokens_details": {"reasoning_tokens": 0}
		}
	}`

	var resp responses.Response
	if err := json.Unmarshal([]byte(respJSON), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	result := parseCodexResponse(&resp)
	if len(result.ToolCalls) != 1 {
		t.Fatalf("len(ToolCalls) = %d, want 1", len(result.ToolCalls))
	}
	if result.ToolCalls[0].Description != "Check git status" {
		t.Fatalf("Description = %q, want %q", result.ToolCalls[0].Description, "Check git status")
	}
}

func TestCodexProvider_ChatRoundTrip(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			http.Error(w, "not found: "+r.URL.Path, http.StatusNotFound)
			return
		}
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if r.Header.Get("Chatgpt-Account-Id") != "acc-123" {
			http.Error(w, "missing account id", http.StatusBadRequest)
			return
		}

		resp := map[string]interface{}{
			"id":     "resp_test",
			"object": "response",
			"status": "completed",
			"output": []map[string]interface{}{
				{
					"id":     "msg_1",
					"type":   "message",
					"role":   "assistant",
					"status": "completed",
					"content": []map[string]interface{}{
						{"type": "output_text", "text": "Hi from Codex!"},
					},
				},
			},
			"usage": map[string]interface{}{
				"input_tokens":          12,
				"output_tokens":         6,
				"total_tokens":          18,
				"input_tokens_details":  map[string]interface{}{"cached_tokens": 0},
				"output_tokens_details": map[string]interface{}{"reasoning_tokens": 0},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	provider := NewCodexProvider("test-token", "acc-123")
	provider.client = createOpenAITestClient(server.URL, "test-token", "acc-123")

	messages := []Message{{Role: "user", Content: "Hello"}}
	resp, err := provider.Chat(t.Context(), messages, nil, "gpt-4o", map[string]interface{}{"max_tokens": 1024})
	if err != nil {
		t.Fatalf("Chat() error: %v", err)
	}
	if resp.Content != "Hi from Codex!" {
		t.Errorf("Content = %q, want %q", resp.Content, "Hi from Codex!")
	}
	if resp.FinishReason != "stop" {
		t.Errorf("FinishReason = %q, want %q", resp.FinishReason, "stop")
	}
	if resp.Usage.TotalTokens != 18 {
		t.Errorf("TotalTokens = %d, want 18", resp.Usage.TotalTokens)
	}
}

func TestCodexProvider_GetDefaultModel(t *testing.T) {
	p := NewCodexProvider("test-token", "")
	if got := p.GetDefaultModel(); got != "gpt-4o" {
		t.Errorf("GetDefaultModel() = %q, want %q", got, "gpt-4o")
	}
}

func createOpenAITestClient(baseURL, token, accountID string) *openai.Client {
	opts := []openaiopt.RequestOption{
		openaiopt.WithBaseURL(baseURL),
		openaiopt.WithAPIKey(token),
	}
	if accountID != "" {
		opts = append(opts, openaiopt.WithHeader("Chatgpt-Account-Id", accountID))
	}
	c := openai.NewClient(opts...)
	return &c
}
