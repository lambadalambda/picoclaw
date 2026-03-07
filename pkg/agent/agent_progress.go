package agent

import (
	"fmt"
	"strings"
	"sync"

	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/providers"
)

type agentProgressCall struct {
	seq    int
	id     string
	tool   string
	label  string
	status string
}

type agentProgressTracker struct {
	mu         sync.Mutex
	bus        *bus.MessageBus
	channel    string
	chatID     string
	runID      string
	calls      []agentProgressCall
	callIndex  map[string]int
	lastSent   string
	seenErr    bool
	seenActive bool
}

func newAgentProgressTracker(msgBus *bus.MessageBus, channel, chatID, runID string, toolCalls []providers.ToolCall) *agentProgressTracker {
	channel = strings.TrimSpace(channel)
	chatID = strings.TrimSpace(chatID)
	runID = strings.TrimSpace(runID)

	tracker := &agentProgressTracker{
		bus:       msgBus,
		channel:   channel,
		chatID:    chatID,
		runID:     runID,
		callIndex: make(map[string]int),
	}

	seq := 0
	for _, tc := range toolCalls {
		name := strings.TrimSpace(tc.Name)
		if name == "" {
			continue
		}
		if !toolsToEcho[name] {
			continue
		}
		seq++
		call := agentProgressCall{
			seq:   seq,
			id:    strings.TrimSpace(tc.ID),
			tool:  name,
			label: safeAgentProgressLabel(tc),
		}
		if call.id == "" {
			seq--
			continue
		}
		tracker.callIndex[call.id] = len(tracker.calls)
		tracker.calls = append(tracker.calls, call)
	}

	return tracker
}

func safeAgentProgressLabel(tc providers.ToolCall) string {
	label := strings.TrimSpace(extractToolCallDescription(tc))
	label = strings.Join(strings.Fields(label), " ")

	if label == "" {
		name := strings.TrimSpace(tc.Name)
		if name == "" {
			label = "run tool"
		} else {
			label = name
		}
	}

	if len(label) > 120 {
		label = label[:117] + "..."
	}
	return label
}

func (t *agentProgressTracker) onToolStart(call providers.ToolCall) {
	callID := strings.TrimSpace(call.ID)
	if callID == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	idx, ok := t.callIndex[callID]
	if !ok {
		return
	}

	if t.calls[idx].status != "run" {
		t.calls[idx].status = "run"
		t.seenActive = true
	}

	t.publishLocked()
}

func (t *agentProgressTracker) onToolComplete(call providers.ToolCall, result providers.Message) {
	callID := strings.TrimSpace(call.ID)
	if callID == "" {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	idx, ok := t.callIndex[callID]
	if !ok {
		return
	}

	status := "ok"
	if strings.HasPrefix(strings.TrimSpace(result.Content), "Error:") {
		status = "err"
		t.seenErr = true
	}
	if t.calls[idx].status != status {
		t.calls[idx].status = status
	}

	t.publishLocked()
}

func (t *agentProgressTracker) publishLocked() {
	if t == nil || t.bus == nil {
		return
	}
	if strings.TrimSpace(t.channel) == "" || strings.TrimSpace(t.chatID) == "" {
		return
	}
	if !t.seenActive {
		return
	}

	content := t.renderLocked()
	if content == "" {
		return
	}
	if content == t.lastSent {
		return
	}
	t.lastSent = content

	t.bus.PublishOutbound(bus.OutboundMessage{
		Channel: t.channel,
		ChatID:  t.chatID,
		Content: content,
	})
}

func (t *agentProgressTracker) renderLocked() string {
	// Include only calls we have a status for.
	startedCalls := make([]agentProgressCall, 0, len(t.calls))
	for _, c := range t.calls {
		if strings.TrimSpace(c.status) == "" {
			continue
		}
		startedCalls = append(startedCalls, c)
	}

	if len(startedCalls) == 0 {
		return ""
	}

	state := "done"
	currentTool := startedCalls[len(startedCalls)-1].tool

	hasRunning := false
	for _, c := range startedCalls {
		if c.status == "run" {
			hasRunning = true
			currentTool = c.tool
		}
	}

	if hasRunning {
		state = "running"
	} else if t.seenErr {
		state = "failed"
	}

	runClause := ""
	if strings.TrimSpace(t.runID) != "" {
		runClause = ", run=" + strings.TrimSpace(t.runID)
	}

	header := fmt.Sprintf("Agent progress (v1%s): %s", runClause, state)
	currentTool = strings.TrimSpace(strings.ReplaceAll(currentTool, "\n", " "))
	if currentTool != "" {
		header += " " + currentTool
	}

	var sb strings.Builder
	sb.WriteString(header)
	sb.WriteString("\n")
	sb.WriteString("Calls:\n")

	for _, c := range startedCalls {
		tool := strings.TrimSpace(strings.ReplaceAll(c.tool, "\n", " "))
		label := strings.TrimSpace(strings.ReplaceAll(c.label, "\n", " "))
		status := strings.TrimSpace(c.status)
		if tool == "" || status == "" {
			continue
		}
		if label != "" {
			sb.WriteString(fmt.Sprintf("%d. %s %s - %s\n", c.seq, status, tool, label))
		} else {
			sb.WriteString(fmt.Sprintf("%d. %s %s\n", c.seq, status, tool))
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}
