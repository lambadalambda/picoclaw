package providers

import (
	"os"
	"path/filepath"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	path := filepath.Join("testdata", name)
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("failed to read fixture %s: %v", path, err)
	}
	return b
}

func TestParseResponse_Contract_OpenAIStyleToolCalls(t *testing.T) {
	p := NewHTTPProvider("test-key", "https://example.com")
	body := readFixture(t, "response_toolcalls_openai.json")

	resp, err := p.parseResponse(body)
	if err != nil {
		t.Fatalf("parseResponse error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.ID != "call_1" {
		t.Fatalf("ID = %q, want call_1", tc.ID)
	}
	if tc.Type != "function" {
		t.Fatalf("Type = %q, want function", tc.Type)
	}
	if tc.Function == nil {
		t.Fatal("Function should be non-nil")
	}
	if tc.Function.Name != "exec" || tc.Name != "exec" {
		t.Fatalf("unexpected tool name fields: Function.Name=%q Name=%q", tc.Function.Name, tc.Name)
	}
	if got, ok := tc.Arguments["command"].(string); !ok || got != "ls -la" {
		t.Fatalf("unexpected parsed args: %+v", tc.Arguments)
	}
}

func TestParseResponse_Contract_LegacyToolCalls(t *testing.T) {
	p := NewHTTPProvider("test-key", "https://example.com")
	body := readFixture(t, "response_toolcalls_legacy.json")

	resp, err := p.parseResponse(body)
	if err != nil {
		t.Fatalf("parseResponse error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.Type != "function" {
		t.Fatalf("Type = %q, want function", tc.Type)
	}
	if tc.Function == nil {
		t.Fatal("Function should be non-nil")
	}
	if tc.Function.Name != "read_file" || tc.Name != "read_file" {
		t.Fatalf("unexpected tool name fields: Function.Name=%q Name=%q", tc.Function.Name, tc.Name)
	}
	if got, ok := tc.Arguments["path"].(string); !ok || got != "README.md" {
		t.Fatalf("unexpected parsed args: %+v", tc.Arguments)
	}
}

func TestParseResponse_Contract_MalformedToolArgs(t *testing.T) {
	p := NewHTTPProvider("test-key", "https://example.com")
	body := readFixture(t, "response_toolcalls_malformed_args.json")

	resp, err := p.parseResponse(body)
	if err != nil {
		t.Fatalf("parseResponse error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	tc := resp.ToolCalls[0]
	if tc.Function == nil {
		t.Fatal("Function should be non-nil")
	}
	if got, ok := tc.Arguments["raw"].(string); !ok || got == "" {
		t.Fatalf("expected raw malformed arguments, got %+v", tc.Arguments)
	}
}

func FuzzHTTPProviderParseResponse_NoPanic(f *testing.F) {
	f.Add(string(readFixtureForFuzz("response_toolcalls_openai.json")))
	f.Add(string(readFixtureForFuzz("response_toolcalls_legacy.json")))
	f.Add(string(readFixtureForFuzz("response_toolcalls_malformed_args.json")))
	f.Add(`{"choices":[]}`)
	f.Add(`{}`)

	p := NewHTTPProvider("test-key", "https://example.com")
	f.Fuzz(func(t *testing.T, body string) {
		_, _ = p.parseResponse([]byte(body))
	})
}

func readFixtureForFuzz(name string) []byte {
	path := filepath.Join("testdata", name)
	b, err := os.ReadFile(path)
	if err != nil {
		return []byte(`{"choices":[]}`)
	}
	return b
}
