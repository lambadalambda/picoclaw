package tools

import (
	"context"
	"strings"
	"testing"
)

type policyTestTool struct {
	name   string
	result string
}

func (t *policyTestTool) Name() string        { return t.name }
func (t *policyTestTool) Description() string { return "policy test tool" }
func (t *policyTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *policyTestTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return t.result, nil
}

func TestToolRegistry_Policy_Deny(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&policyTestTool{name: "danger", result: "ok"})
	r.SetExecutionPolicy(NewToolExecutionPolicy(true, nil, []string{"danger"}))

	_, err := r.Execute(context.Background(), "danger", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected deny policy to block tool")
	}
	if !strings.Contains(err.Error(), "blocked by policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolRegistry_Policy_AllowList(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&policyTestTool{name: "safe", result: "ok"})
	r.Register(&policyTestTool{name: "other", result: "ok"})
	r.SetExecutionPolicy(NewToolExecutionPolicy(true, []string{"safe"}, nil))

	if _, err := r.Execute(context.Background(), "safe", map[string]interface{}{}); err != nil {
		t.Fatalf("safe tool should be allowed: %v", err)
	}

	_, err := r.Execute(context.Background(), "other", map[string]interface{}{})
	if err == nil {
		t.Fatal("expected non-allowlisted tool to be blocked")
	}
	if !strings.Contains(err.Error(), "not allowed by policy") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestToolRegistry_Policy_Disabled(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&policyTestTool{name: "danger", result: "ok"})
	r.SetExecutionPolicy(NewToolExecutionPolicy(false, nil, []string{"danger"}))

	result, err := r.Execute(context.Background(), "danger", map[string]interface{}{})
	if err != nil {
		t.Fatalf("policy disabled; expected success, got error: %v", err)
	}
	if result != "ok" {
		t.Fatalf("result = %q, want ok", result)
	}
}
