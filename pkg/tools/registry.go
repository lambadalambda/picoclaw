package tools

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type ToolRegistry struct {
	tools  map[string]Tool
	policy ToolExecutionPolicy
	unsafe *UnsafeToolGate
	mu     sync.RWMutex
}

func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools: make(map[string]Tool),
	}
}

// SetExecutionPolicy updates the active tool execution policy.
func (r *ToolRegistry) SetExecutionPolicy(policy ToolExecutionPolicy) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.policy = policy
}

// SetUnsafeToolGate attaches an unsafe tool approval gate. When configured,
// tools whose names start with "unsafe_" are blocked unless explicitly approved
// for the current session.
func (r *ToolRegistry) SetUnsafeToolGate(gate *UnsafeToolGate) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.unsafe = gate
}

func (r *ToolRegistry) Register(tool Tool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.tools[tool.Name()] = tool
}

func (r *ToolRegistry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	tool, ok := r.tools[name]
	return tool, ok
}

func (r *ToolRegistry) Execute(ctx context.Context, name string, args map[string]interface{}) (string, error) {
	return r.ExecuteWithContext(ctx, name, args, "", "")
}

func (r *ToolRegistry) ExecuteWithContext(ctx context.Context, name string, args map[string]interface{}, channel, chatID string) (string, error) {
	res, err := r.ExecuteResultWithContext(ctx, name, args, channel, chatID)
	return res.Content, err
}

func (r *ToolRegistry) ExecuteResult(ctx context.Context, name string, args map[string]interface{}) (ToolResult, error) {
	return r.ExecuteResultWithContext(ctx, name, args, "", "")
}

func (r *ToolRegistry) ExecuteResultWithContext(ctx context.Context, name string, args map[string]interface{}, channel, chatID string) (ToolResult, error) {
	traceID := TraceIDFromContext(ctx)
	logger.InfoCF("tool", "Tool execution started",
		map[string]interface{}{
			"tool":     name,
			"args":     args,
			"trace_id": traceID,
		})

	tool, ok := r.Get(name)
	if !ok {
		logger.ErrorCF("tool", "Tool not found",
			map[string]interface{}{
				"tool": name,
			})
		return ToolResult{}, fmt.Errorf("tool '%s' not found", name)
	}

	if err := r.checkPolicy(name); err != nil {
		logger.WarnCF("tool", "Tool execution blocked by policy",
			map[string]interface{}{
				"tool":     name,
				"error":    err.Error(),
				"trace_id": traceID,
			})
		return ToolResult{}, err
	}

	if err := r.checkUnsafeGate(name, args); err != nil {
		logger.WarnCF("tool", "Tool execution blocked (unsafe tool requires approval)",
			map[string]interface{}{
				"tool":     name,
				"error":    err.Error(),
				"trace_id": traceID,
			})
		return ToolResult{}, err
	}

	normalizedArgs, err := normalizeAndValidateToolArgs(tool, args)
	if err != nil {
		logger.WarnCF("tool", "Tool argument validation failed",
			map[string]interface{}{
				"tool":     name,
				"error":    err.Error(),
				"trace_id": traceID,
			})
		return ToolResult{}, err
	}

	execArgs := withExecutionContext(normalizedArgs, channel, chatID, traceID)

	start := time.Now()
	var result ToolResult
	if richTool, ok := tool.(ToolWithResult); ok {
		result, err = richTool.ExecuteResult(ctx, execArgs)
	} else {
		result.Content, err = tool.Execute(ctx, execArgs)
	}
	duration := time.Since(start)

	if err != nil {
		logger.ErrorCF("tool", "Tool execution failed",
			map[string]interface{}{
				"tool":     name,
				"duration": duration.Milliseconds(),
				"error":    err.Error(),
				"trace_id": traceID,
			})
	} else {
		logger.InfoCF("tool", "Tool execution completed",
			map[string]interface{}{
				"tool":          name,
				"duration_ms":   duration.Milliseconds(),
				"result_length": len(result.Content),
				"parts_count":   len(result.Parts),
				"trace_id":      traceID,
			})
	}

	return result, err
}

func (r *ToolRegistry) GetDefinitions() []map[string]interface{} {
	r.mu.RLock()
	defer r.mu.RUnlock()

	definitions := make([]map[string]interface{}, 0, len(r.tools))
	for _, name := range sortedKeys(r.tools) {
		tool := r.tools[name]
		definitions = append(definitions, ToolToSchema(tool))
	}
	return definitions
}

// GetProviderDefinitions returns tool definitions in the providers.ToolDefinition
// format, ready to pass directly to an LLM provider's Chat call.
func (r *ToolRegistry) GetProviderDefinitions() []providers.ToolDefinition {
	schemas := r.GetDefinitions()
	defs := make([]providers.ToolDefinition, 0, len(schemas))
	for _, td := range schemas {
		fn, ok := td["function"].(map[string]interface{})
		if !ok {
			continue
		}
		name, _ := fn["name"].(string)
		desc, _ := fn["description"].(string)
		params, _ := fn["parameters"].(map[string]interface{})
		typeStr, _ := td["type"].(string)
		if name == "" || typeStr == "" {
			continue
		}
		defs = append(defs, providers.ToolDefinition{
			Type: typeStr,
			Function: providers.ToolFunctionDefinition{
				Name:        name,
				Description: desc,
				Parameters:  params,
			},
		})
	}
	return defs
}

// List returns a list of all registered tool names.
func (r *ToolRegistry) List() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	return sortedKeys(r.tools)
}

// Count returns the number of registered tools.
func (r *ToolRegistry) Count() int {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return len(r.tools)
}

// RegisterCoreTools registers the standard set of tools shared between the
// main agent and subagents: filesystem ops, exec, edit, web search, and web fetch.
type CoreToolsOptions struct {
	DisableSafeguards bool
}

func RegisterCoreTools(r *ToolRegistry, workspace string, webSearchCfg WebSearchToolConfig, opts CoreToolsOptions) {
	// Safe (workspace-scoped) filesystem tools.
	readTool := NewReadFileTool(workspace)
	writeTool := NewWriteFileTool(workspace)
	listTool := NewListDirTool(workspace)
	editTool := NewEditFileTool(workspace)

	if opts.DisableSafeguards {
		readTool.SetRestrictToWorkspace(false)
		writeTool.SetRestrictToWorkspace(false)
		listTool.SetRestrictToWorkspace(false)
		editTool.SetRestrictToWorkspace(false)
	}

	r.Register(readTool)
	r.Register(writeTool)
	r.Register(listTool)
	// Unsafe filesystem tools (require explicit user approval).
	r.Register(NewUnsafeReadFileTool())
	r.Register(NewUnsafeWriteFileTool())
	r.Register(NewUnsafeListDirTool())
	r.Register(NewSessionHistoryTool(workspace))
	// Safe exec is workspace-scoped.
	execTool := NewExecTool(workspace)
	execTool.SetRestrictToWorkspace(!opts.DisableSafeguards)
	execTool.SetDisableGuards(opts.DisableSafeguards)
	r.Register(execTool)
	// Unsafe exec (requires explicit user approval).
	unsafeExecTool := NewUnsafeExecTool(workspace)
	unsafeExecTool.SetDisableGuards(opts.DisableSafeguards)
	r.Register(unsafeExecTool)
	r.Register(editTool)
	r.Register(NewUnsafeEditFileTool())
	r.Register(NewWebFetchTool(50000))
	r.Register(NewWebSearchTool(webSearchCfg))
}

// GetSummaries returns human-readable summaries of all registered tools.
// Returns a slice of "name - description" strings.
func (r *ToolRegistry) GetSummaries() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	summaries := make([]string, 0, len(r.tools))
	for _, name := range sortedKeys(r.tools) {
		tool := r.tools[name]
		summaries = append(summaries, fmt.Sprintf("- `%s` - %s", tool.Name(), tool.Description()))
	}
	return summaries
}

func sortedKeys[T any](m map[string]T) []string {
	keys := make([]string, 0, len(m))
	for name := range m {
		keys = append(keys, name)
	}
	sort.Strings(keys)
	return keys
}

func (r *ToolRegistry) checkPolicy(name string) error {
	r.mu.RLock()
	policy := r.policy
	r.mu.RUnlock()
	return policy.check(name)
}

func (r *ToolRegistry) checkUnsafeGate(name string, args map[string]interface{}) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !strings.HasPrefix(name, "unsafe_") {
		return nil
	}

	r.mu.RLock()
	gate := r.unsafe
	r.mu.RUnlock()
	if gate == nil {
		// No gate configured => allow.
		return nil
	}

	sessionKey := strings.TrimSpace(getExecutionSessionKey(args))
	if gate.IsApproved(sessionKey) {
		return nil
	}

	return fmt.Errorf("tool %s requires explicit user approval. Ask the user to reply with UNSAFE_OK (optionally UNSAFE_OK 10m) to enable unsafe tools for this chat", name)
}
