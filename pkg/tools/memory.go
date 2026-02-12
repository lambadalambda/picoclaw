package tools

import (
	"context"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/memory"
)

// MemorySearchTool provides FTS5 full-text search over stored memories.
type MemorySearchTool struct {
	store *memory.MemoryStore
}

func NewMemorySearchTool(store *memory.MemoryStore) *MemorySearchTool {
	return &MemorySearchTool{store: store}
}

func (t *MemorySearchTool) Name() string {
	return "memory_search"
}

func (t *MemorySearchTool) Description() string {
	return "Search stored memories using keyword search. Returns relevant memories ranked by relevance. Use this to recall user preferences, past facts, or previous events."
}

func (t *MemorySearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "The search query (keywords or natural language)",
			},
			"limit": map[string]interface{}{
				"type":        "number",
				"description": "Maximum number of results (default 5)",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "Filter by category: preference, fact, event, note, general",
			},
		},
		"required": []string{"query"},
	}
}

func (t *MemorySearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	query, ok := args["query"].(string)
	if !ok || strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query is required")
	}

	limit := 5
	if l, ok := args["limit"].(float64); ok && l > 0 {
		limit = int(l)
	}

	category := ""
	if c, ok := args["category"].(string); ok {
		category = c
	}

	results, err := t.store.Search(query, limit, category)
	if err != nil {
		return fmt.Sprintf("Search error: %v", err), nil
	}

	if len(results) == 0 {
		return "No memories found matching the query.", nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d memories:\n", len(results)))
	for _, m := range results {
		date := m.CreatedAt.Format("2006-01-02")
		sb.WriteString(fmt.Sprintf("[#%d] (%s, %s) %s\n", m.ID, m.Category, date, m.Content))
	}
	return sb.String(), nil
}

// MemoryStoreTool saves new memories to the database with markdown write-through.
type MemoryStoreTool struct {
	store *memory.MemoryStore
}

func NewMemoryStoreTool(store *memory.MemoryStore) *MemoryStoreTool {
	return &MemoryStoreTool{store: store}
}

func (t *MemoryStoreTool) Name() string {
	return "memory_store"
}

func (t *MemoryStoreTool) Description() string {
	return "Store a new memory. Use this to remember user preferences, important facts, or notable events. Memories are searchable and persist across sessions."
}

func (t *MemoryStoreTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The memory content to store",
			},
			"category": map[string]interface{}{
				"type":        "string",
				"description": "Category: preference, fact, event, note (default: general). Preferences/notes go to MEMORY.md, facts/events go to daily logs.",
			},
		},
		"required": []string{"content"},
	}
}

func (t *MemoryStoreTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	content, ok := args["content"].(string)
	if !ok || strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("content is required")
	}

	category := "general"
	if c, ok := args["category"].(string); ok && c != "" {
		category = c
	}

	id, err := t.store.Store(content, category, "chat", nil)
	if err != nil {
		return fmt.Sprintf("Failed to store memory: %v", err), nil
	}

	return fmt.Sprintf("Memory stored (id=%d, category=%s)", id, category), nil
}
