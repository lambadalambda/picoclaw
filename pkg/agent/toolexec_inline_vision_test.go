package agent

import (
	"context"
	"testing"

	"github.com/sipeed/picoclaw/pkg/providers"
	"github.com/sipeed/picoclaw/pkg/tools"
)

type inlineVisionCtxProbeTool struct{}

func (t *inlineVisionCtxProbeTool) Name() string        { return "image_inspect" }
func (t *inlineVisionCtxProbeTool) Description() string { return "probe inline vision context" }
func (t *inlineVisionCtxProbeTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *inlineVisionCtxProbeTool) Execute(_ context.Context, args map[string]interface{}) (string, error) {
	v, ok := args["__context_inline_vision"].(bool)
	if !ok {
		return "missing", nil
	}
	if v {
		return "true", nil
	}
	return "false", nil
}

func TestExecuteToolsConcurrently_SetsInlineVisionContextForImageInspect(t *testing.T) {
	reg := tools.NewToolRegistry()
	reg.Register(&inlineVisionCtxProbeTool{})

	al := &AgentLoop{
		tools:             reg,
		provider:          &providers.HTTPProvider{},
		model:             "gpt-4o",
		modelCapabilities: providers.ModelCapabilitiesFor("gpt-4o"),
		echoToolCalls:     false,
	}

	results := al.executeToolsConcurrently(context.Background(), []providers.ToolCall{
		{ID: "tc1", Name: "image_inspect", Arguments: map[string]interface{}{}},
	}, 1, processOptions{SessionKey: "telegram:1", Channel: "telegram", ChatID: "1", TraceID: "trace"})

	if len(results) != 1 {
		t.Fatalf("len(results) = %d, want 1", len(results))
	}
	if results[0].Content != "true" {
		t.Fatalf("tool result content = %q, want true", results[0].Content)
	}
}
