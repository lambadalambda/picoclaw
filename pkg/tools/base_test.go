package tools

import (
	"context"
	"testing"
)

type schemaTestTool struct{}

func (t *schemaTestTool) Name() string { return "schema_test" }

func (t *schemaTestTool) Description() string { return "schema test tool" }

func (t *schemaTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{"type": "string"},
		},
		"required": []string{"path"},
	}
}

func (t *schemaTestTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return "ok", nil
}

func TestToolToSchema_AddsToolCallDescriptionParameter(t *testing.T) {
	schema := ToolToSchema(&schemaTestTool{})

	fn, ok := schema["function"].(map[string]interface{})
	if !ok {
		t.Fatalf("function schema missing: %#v", schema)
	}
	params, ok := fn["parameters"].(map[string]interface{})
	if !ok {
		t.Fatalf("parameters missing: %#v", fn)
	}
	properties, ok := params["properties"].(map[string]interface{})
	if !ok {
		t.Fatalf("properties missing: %#v", params)
	}

	if _, ok := properties[toolCallDescriptionParameter]; !ok {
		t.Fatalf("%q parameter missing from tool schema", toolCallDescriptionParameter)
	}

	required, _ := params["required"].([]string)
	if len(required) != 1 || required[0] != "path" {
		t.Fatalf("required changed unexpectedly: %#v", required)
	}
}
