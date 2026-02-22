package providers

import "strings"

// SanitizeToolTranscript removes invalid or incomplete tool-call sequences.
//
// This is primarily a defensive measure against persisted session history being
// truncated in the middle of a tool-call exchange (assistant tool_calls without
// matching tool results, or tool results without the originating tool_calls).
//
// It never mutates the input slice.
func SanitizeToolTranscript(messages []Message) (sanitized []Message, dropped int) {
	if len(messages) == 0 {
		return []Message{}, 0
	}

	out := make([]Message, 0, len(messages))

	type toolBatch struct {
		active   bool
		startIdx int
		expected map[string]struct{}
		seen     map[string]struct{}
	}

	batch := toolBatch{}

	endBatch := func() {
		if !batch.active {
			return
		}
		complete := len(batch.expected) > 0 && len(batch.expected) == len(batch.seen)
		if !complete {
			dropped += len(out) - batch.startIdx
			out = out[:batch.startIdx]
		}
		batch = toolBatch{}
	}

	for _, msg := range messages {
		role := strings.TrimSpace(strings.ToLower(msg.Role))
		switch role {
		case "assistant":
			endBatch()

			if len(msg.ToolCalls) == 0 {
				out = append(out, msg)
				continue
			}

			expected := make(map[string]struct{}, len(msg.ToolCalls))
			for _, tc := range msg.ToolCalls {
				id := strings.TrimSpace(tc.ID)
				if id == "" {
					continue
				}
				expected[id] = struct{}{}
			}
			if len(expected) == 0 {
				// Tool call IDs are missing; treat as a normal assistant message.
				msg.ToolCalls = nil
				out = append(out, msg)
				continue
			}

			batch = toolBatch{
				active:   true,
				startIdx: len(out),
				expected: expected,
				seen:     make(map[string]struct{}, len(expected)),
			}
			out = append(out, msg)

		case "tool":
			if !batch.active {
				dropped++
				continue
			}
			id := strings.TrimSpace(msg.ToolCallID)
			if id == "" {
				dropped++
				continue
			}
			if _, ok := batch.expected[id]; !ok {
				dropped++
				continue
			}
			if _, dup := batch.seen[id]; dup {
				dropped++
				continue
			}
			batch.seen[id] = struct{}{}
			out = append(out, msg)

		case "user", "system":
			endBatch()
			out = append(out, msg)

		default:
			endBatch()
			out = append(out, msg)
		}
	}

	endBatch()
	return out, dropped
}
