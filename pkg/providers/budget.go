package providers

import "sort"

const defaultTruncationMarker = "... [truncated]"

// MessageBudget defines payload limits applied before provider calls.
// Limits are best-effort and deterministic.
type MessageBudget struct {
	MaxMessages         int
	MaxTotalChars       int
	MaxMessageChars     int
	MaxToolMessageChars int
	TruncationMarker    string
}

// BudgetFromContextWindow builds a conservative default budget from an estimated
// model context window size (in tokens).
func BudgetFromContextWindow(contextWindow int) MessageBudget {
	if contextWindow <= 0 {
		contextWindow = 8192
	}

	maxTotalChars := contextWindow * 4 * 80 / 100 // ~80% of estimated char capacity
	if maxTotalChars < 32000 {
		maxTotalChars = 32000
	}

	maxMessageChars := maxTotalChars / 3
	if maxMessageChars < 4000 {
		maxMessageChars = 4000
	}

	maxToolChars := maxMessageChars / 2
	if maxToolChars < 2000 {
		maxToolChars = 2000
	}

	return MessageBudget{
		MaxMessages:         200,
		MaxTotalChars:       maxTotalChars,
		MaxMessageChars:     maxMessageChars,
		MaxToolMessageChars: maxToolChars,
		TruncationMarker:    defaultTruncationMarker,
	}
}

func (b MessageBudget) Enabled() bool {
	return b.MaxMessages > 0 || b.MaxTotalChars > 0 || b.MaxMessageChars > 0 || b.MaxToolMessageChars > 0
}

func (b MessageBudget) marker() string {
	if b.TruncationMarker == "" {
		return defaultTruncationMarker
	}
	return b.TruncationMarker
}

// MessageBudgetStats reports what changed during payload budgeting.
type MessageBudgetStats struct {
	InputMessages     int
	OutputMessages    int
	CharsBefore       int
	CharsAfter        int
	TruncatedMessages int
	DroppedMessages   int
}

func (s MessageBudgetStats) Changed() bool {
	return s.TruncatedMessages > 0 || s.DroppedMessages > 0 || s.CharsAfter != s.CharsBefore || s.OutputMessages != s.InputMessages
}

// ApplyMessageBudget trims message payload size before sending to a provider.
// It never mutates the input slice.
func ApplyMessageBudget(messages []Message, budget MessageBudget) ([]Message, MessageBudgetStats) {
	stats := MessageBudgetStats{
		InputMessages: len(messages),
		CharsBefore:   sumMessageChars(messages),
	}

	if len(messages) == 0 || !budget.Enabled() {
		stats.OutputMessages = len(messages)
		stats.CharsAfter = stats.CharsBefore
		return append([]Message(nil), messages...), stats
	}

	marker := budget.marker()
	trimmed := append([]Message(nil), messages...)

	for i := range trimmed {
		limit := budget.MaxMessageChars
		if trimmed[i].Role == "tool" && budget.MaxToolMessageChars > 0 {
			limit = budget.MaxToolMessageChars
		}
		if limit > 0 && len(trimmed[i].Content) > limit {
			trimmed[i].Content = truncateWithMarker(trimmed[i].Content, limit, marker)
			stats.TruncatedMessages++
		}
	}

	if budget.MaxMessages > 0 && len(trimmed) > budget.MaxMessages {
		next := keepSystemAndLatest(trimmed, budget.MaxMessages)
		stats.DroppedMessages += len(trimmed) - len(next)
		trimmed = next
	}

	if budget.MaxTotalChars > 0 && sumMessageChars(trimmed) > budget.MaxTotalChars {
		next := keepWithinTotalChars(trimmed, budget.MaxTotalChars)
		stats.DroppedMessages += len(trimmed) - len(next)
		trimmed = next

		// Final fit pass: if still over budget, trim the latest non-system message.
		total := sumMessageChars(trimmed)
		if total > budget.MaxTotalChars {
			overflow := total - budget.MaxTotalChars
			for i := len(trimmed) - 1; i >= 0; i-- {
				if trimmed[i].Role == "system" {
					continue
				}
				target := len(trimmed[i].Content) - overflow
				if target < 1 {
					target = 1
				}
				if target < len(trimmed[i].Content) {
					trimmed[i].Content = truncateWithMarker(trimmed[i].Content, target, marker)
					stats.TruncatedMessages++
				}
				break
			}
		}
	}

	stats.OutputMessages = len(trimmed)
	stats.CharsAfter = sumMessageChars(trimmed)
	return trimmed, stats
}

func keepSystemAndLatest(messages []Message, maxMessages int) []Message {
	if maxMessages <= 0 || len(messages) <= maxMessages {
		return append([]Message(nil), messages...)
	}

	systemIdx := make([]int, 0, len(messages))
	otherIdx := make([]int, 0, len(messages))
	for i, m := range messages {
		if m.Role == "system" {
			systemIdx = append(systemIdx, i)
		} else {
			otherIdx = append(otherIdx, i)
		}
	}

	keepIdx := make([]int, 0, maxMessages)
	if len(systemIdx) >= maxMessages {
		keepIdx = append(keepIdx, systemIdx[:maxMessages]...)
	} else {
		keepIdx = append(keepIdx, systemIdx...)
		slots := maxMessages - len(keepIdx)
		for i := len(otherIdx) - 1; i >= 0 && slots > 0; i-- {
			keepIdx = append(keepIdx, otherIdx[i])
			slots--
		}
	}

	sort.Ints(keepIdx)
	out := make([]Message, 0, len(keepIdx))
	for _, idx := range keepIdx {
		out = append(out, messages[idx])
	}
	return out
}

func keepWithinTotalChars(messages []Message, maxTotalChars int) []Message {
	if maxTotalChars <= 0 || sumMessageChars(messages) <= maxTotalChars {
		return append([]Message(nil), messages...)
	}

	keep := make([]bool, len(messages))
	totalSystem := 0
	nonSystemCount := 0
	for i, m := range messages {
		if m.Role == "system" {
			keep[i] = true
			totalSystem += len(m.Content)
		} else {
			nonSystemCount++
		}
	}

	remaining := maxTotalChars - totalSystem
	if remaining < 0 {
		remaining = 0
	}

	used := 0
	selectedNonSystem := 0
	latestNonSystem := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "system" {
			continue
		}
		if latestNonSystem < 0 {
			latestNonSystem = i
		}
		contentLen := len(messages[i].Content)
		if used+contentLen <= remaining {
			keep[i] = true
			used += contentLen
			selectedNonSystem++
		}
	}

	// Keep at least the newest non-system message for conversational continuity.
	if selectedNonSystem == 0 && nonSystemCount > 0 && latestNonSystem >= 0 {
		keep[latestNonSystem] = true
	}

	out := make([]Message, 0, len(messages))
	for i, m := range messages {
		if keep[i] {
			out = append(out, m)
		}
	}
	return out
}

func sumMessageChars(messages []Message) int {
	total := 0
	for _, m := range messages {
		total += len(m.Content)
	}
	return total
}

func truncateWithMarker(content string, limit int, marker string) string {
	if limit <= 0 {
		return ""
	}
	if len(content) <= limit {
		return content
	}
	if marker == "" {
		marker = defaultTruncationMarker
	}
	if len(marker) >= limit {
		return marker[:limit]
	}
	keep := limit - len(marker)
	if keep < 0 {
		keep = 0
	}
	return content[:keep] + marker
}
