package agent

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
	"github.com/sipeed/picoclaw/pkg/providers"
)

// parsedMemory is a single memory extracted from LLM output.
type parsedMemory struct {
	Category string
	Content  string
}

// memoryLineRe matches lines like "MEMORY(category): content"
var memoryLineRe = regexp.MustCompile(`^MEMORY\((\w+)\):\s*(.+)$`)

// parseMemoryLines extracts structured memories from LLM output.
// Expected format: one MEMORY(category): content per line.
// Non-matching lines (commentary, blank, "NONE") are ignored.
func parseMemoryLines(text string) []parsedMemory {
	var result []parsedMemory
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		m := memoryLineRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		category := strings.ToLower(m[1])
		content := strings.TrimSpace(m[2])
		if content == "" {
			continue
		}
		result = append(result, parsedMemory{Category: category, Content: content})
	}
	return result
}

const memoryExtractionPrompt = `Review this conversation and extract any notable information worth remembering long-term. Focus on:
- User preferences (likes, dislikes, settings)
- Personal facts (name, location, occupation, relationships)
- Important events or decisions
- Project-specific knowledge

Output each memory on its own line using this exact format:
MEMORY(category): content

Categories: preference, fact, event, note

If there is nothing worth remembering, output only: NONE

CONVERSATION:
%s`

// extractAndStoreMemories asks the LLM to extract notable memories from
// a set of messages and stores them in the memory DB. This is called
// during session summarization so that important information survives
// history compaction.
func (al *AgentLoop) extractAndStoreMemories(ctx context.Context, messages []providers.Message) {
	if al.memoryStore == nil {
		return
	}

	// Build conversation text from messages
	var sb strings.Builder
	for _, m := range messages {
		if m.Role != "user" && m.Role != "assistant" {
			continue
		}
		sb.WriteString(fmt.Sprintf("%s: %s\n", m.Role, m.Content))
	}
	conversation := sb.String()
	if strings.TrimSpace(conversation) == "" {
		return
	}

	extractCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	prompt := fmt.Sprintf(memoryExtractionPrompt, conversation)
	response, err := al.provider.Chat(extractCtx, []providers.Message{
		{Role: "user", Content: prompt},
	}, nil, al.model, map[string]interface{}{
		"max_tokens":  1024,
		"temperature": 0.3,
	})
	if err != nil {
		logger.WarnCF("agent", "Memory extraction failed",
			map[string]interface{}{"error": err.Error()})
		return
	}

	memories := parseMemoryLines(response.Content)
	if len(memories) == 0 {
		logger.DebugCF("agent", "No memories extracted from conversation", nil)
		return
	}

	stored := 0
	for _, mem := range memories {
		_, err := al.memoryStore.Store(mem.Content, mem.Category, "summarization", nil)
		if err != nil {
			logger.WarnCF("agent", "Failed to store extracted memory",
				map[string]interface{}{
					"category": mem.Category,
					"error":    err.Error(),
				})
			continue
		}
		stored++
	}

	logger.InfoCF("agent", "Memories extracted during summarization",
		map[string]interface{}{
			"extracted": len(memories),
			"stored":    stored,
		})
}
