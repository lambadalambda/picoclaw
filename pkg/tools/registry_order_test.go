package tools

import (
	"context"
	"reflect"
	"testing"
)

type orderTestTool struct {
	name string
}

func (t *orderTestTool) Name() string        { return t.name }
func (t *orderTestTool) Description() string { return "order test tool" }
func (t *orderTestTool) Parameters() map[string]interface{} {
	return map[string]interface{}{"type": "object", "properties": map[string]interface{}{}}
}
func (t *orderTestTool) Execute(_ context.Context, _ map[string]interface{}) (string, error) {
	return "ok", nil
}

func TestToolRegistry_List_SortedDeterministically(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&orderTestTool{name: "zeta"})
	r.Register(&orderTestTool{name: "alpha"})
	r.Register(&orderTestTool{name: "beta"})

	got := r.List()
	want := []string{"alpha", "beta", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("List() = %v, want %v", got, want)
	}
}

func TestToolRegistry_GetSummaries_SortedDeterministically(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&orderTestTool{name: "zeta"})
	r.Register(&orderTestTool{name: "alpha"})
	r.Register(&orderTestTool{name: "beta"})

	got := r.GetSummaries()
	want := []string{
		"- `alpha` - order test tool",
		"- `beta` - order test tool",
		"- `zeta` - order test tool",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetSummaries() = %v, want %v", got, want)
	}
}

func TestToolRegistry_GetProviderDefinitions_SortedDeterministically(t *testing.T) {
	r := NewToolRegistry()
	r.Register(&orderTestTool{name: "zeta"})
	r.Register(&orderTestTool{name: "alpha"})
	r.Register(&orderTestTool{name: "beta"})

	defs := r.GetProviderDefinitions()
	got := make([]string, 0, len(defs))
	for _, d := range defs {
		got = append(got, d.Function.Name)
	}
	want := []string{"alpha", "beta", "zeta"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("GetProviderDefinitions() names = %v, want %v", got, want)
	}
}
