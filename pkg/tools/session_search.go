package tools

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/session"
	"github.com/sipeed/picoclaw/pkg/utils"
)

type SessionSearchTool struct {
	workspace string
}

func NewSessionSearchTool(workspace string) *SessionSearchTool {
	return &SessionSearchTool{workspace: workspace}
}

func (t *SessionSearchTool) Name() string {
	return "session_search"
}

func (t *SessionSearchTool) Description() string {
	return "Search across all session transcript files to find past conversations. Use this to recall what was discussed in previous sessions (e.g., 'What did we discuss about X last week?')."
}

func (t *SessionSearchTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"query": map[string]interface{}{
				"type":        "string",
				"description": "The text to search for in past sessions",
			},
			"days_back": map[string]interface{}{
				"type":        "integer",
				"description": "How many days back to search (default 7, max 90)",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Maximum number of results to return (default 10, max 50)",
			},
			"roles": map[string]interface{}{
				"type":        "array",
				"description": "Optional filter: only search entries with these roles (user, assistant, tool)",
				"items": map[string]interface{}{
					"type": "string",
				},
			},
			"max_chars": map[string]interface{}{
				"type":        "integer",
				"description": "Truncate returned message content to this many chars (default 300)",
			},
			"cross_channel": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, search across all channels (default false, only searches current channel for privacy)",
			},
		},
		"required": []string{"query"},
	}
}

type searchResult struct {
	SessionKey string
	FilePath   string
	Line       int
	Entry      session.TranscriptEntry
}

func (t *SessionSearchTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	query, _ := args["query"].(string)
	query = strings.TrimSpace(query)
	if query == "" {
		return "", fmt.Errorf("query is required")
	}

	daysBack, err := parseOptionalIntArg(args, "days_back", 7)
	if err != nil {
		return "", err
	}
	if daysBack <= 0 {
		daysBack = 7
	}
	if daysBack > 90 {
		daysBack = 90
	}

	limit, err := parseOptionalIntArg(args, "limit", 10)
	if err != nil {
		return "", err
	}
	if limit <= 0 {
		limit = 10
	}
	if limit > 50 {
		limit = 50
	}

	maxChars, err := parseOptionalIntArg(args, "max_chars", 300)
	if err != nil {
		return "", err
	}
	if maxChars <= 0 {
		maxChars = 300
	}

	crossChannel, _ := args["cross_channel"].(bool)

	rolesFilter := parseStringArray(args["roles"])
	rolesSet := make(map[string]struct{})
	for _, r := range rolesFilter {
		rolesSet[strings.TrimSpace(r)] = struct{}{}
	}
	if len(rolesSet) == 0 {
		rolesSet = nil
	}

	currentChannel := ""
	if !crossChannel {
		ch, _ := getExecutionContext(args)
		currentChannel = ch
		if currentChannel == "" {
			sessionKey := getExecutionSessionKey(args)
			if sessionKey != "" {
				parts := strings.SplitN(sessionKey, ":", 2)
				if len(parts) > 0 {
					currentChannel = parts[0]
				}
			}
		}
	}

	cutoffTime := time.Now().AddDate(0, 0, -daysBack).UnixMilli()

	transcriptsDir := filepath.Join(t.workspace, "transcripts")
	entries, err := os.ReadDir(transcriptsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return "No transcripts directory found. No past sessions to search.", nil
		}
		return "", fmt.Errorf("failed to read transcripts directory: %w", err)
	}

	var results []searchResult
	queryLower := strings.ToLower(query)

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		if !strings.HasSuffix(filename, ".jsonl") {
			continue
		}

		sessionKey := strings.TrimSuffix(filename, ".jsonl")

		if !crossChannel && currentChannel != "" {
			if !strings.HasPrefix(sessionKey, currentChannel+":") && !strings.HasPrefix(sessionKey, currentChannel+"_") {
				continue
			}
		}

		filePath := filepath.Join(transcriptsDir, filename)
		fileResults, err := t.searchFile(filePath, sessionKey, queryLower, cutoffTime, rolesSet)
		if err != nil {
			continue
		}
		results = append(results, fileResults...)
	}

	if len(results) == 0 {
		return fmt.Sprintf("No matches found for query %q within the last %d days.", query, daysBack), nil
	}

	sortResultsByTime(results)

	if len(results) > limit {
		results = results[:limit]
	}

	return t.formatResults(results, query, daysBack, len(results), maxChars), nil
}

func (t *SessionSearchTool) searchFile(filePath, sessionKey, queryLower string, cutoffTime int64, rolesSet map[string]struct{}) ([]searchResult, error) {
	f, err := os.Open(filePath)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var results []searchResult
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)

	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var e session.TranscriptEntry
		if err := json.Unmarshal([]byte(line), &e); err != nil {
			continue
		}

		if e.TSMs > 0 && e.TSMs < cutoffTime {
			continue
		}

		if rolesSet != nil {
			if _, ok := rolesSet[e.Role]; !ok {
				continue
			}
		}

		contentLower := strings.ToLower(e.Content)
		if strings.Contains(contentLower, queryLower) {
			results = append(results, searchResult{
				SessionKey: sessionKey,
				FilePath:   filePath,
				Line:       lineNo,
				Entry:      e,
			})
		}

		if e.Role == "assistant" && len(e.ToolCalls) > 0 {
			for _, tc := range e.ToolCalls {
				if tc.Arguments != nil {
					if argBytes, err := json.Marshal(tc.Arguments); err == nil {
						if strings.Contains(strings.ToLower(string(argBytes)), queryLower) {
							results = append(results, searchResult{
								SessionKey: sessionKey,
								FilePath:   filePath,
								Line:       lineNo,
								Entry:      e,
							})
							break
						}
					}
				}
			}
		}
	}

	return results, scanner.Err()
}

func sortResultsByTime(results []searchResult) {
	for i := 0; i < len(results)-1; i++ {
		for j := i + 1; j < len(results); j++ {
			if results[j].Entry.TSMs > results[i].Entry.TSMs {
				results[i], results[j] = results[j], results[i]
			}
		}
	}
}

func (t *SessionSearchTool) formatResults(results []searchResult, query string, daysBack int, totalFound int, maxChars int) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Search: %q (last %d days)\n", query, daysBack))
	sb.WriteString(fmt.Sprintf("Found: %d results\n\n", totalFound))

	for i, r := range results {
		e := r.Entry
		ts := ""
		if e.TSMs > 0 {
			ts = time.UnixMilli(e.TSMs).Format("2006-01-02 15:04")
		}

		content := utils.Truncate(e.Content, maxChars)

		sb.WriteString(fmt.Sprintf("--- Result %d ---\n", i+1))
		sb.WriteString(fmt.Sprintf("Session: %s\n", r.SessionKey))
		if ts != "" {
			sb.WriteString(fmt.Sprintf("Time: %s\n", ts))
		}

		meta := ""
		switch e.Role {
		case "assistant":
			if len(e.ToolCalls) > 0 {
				meta = formatToolCalls(e.ToolCalls)
			}
		case "tool":
			if e.ToolCallID != "" {
				meta = fmt.Sprintf("call_id=%s", e.ToolCallID)
			}
			if e.Truncated {
				if meta != "" {
					meta += " "
				}
				meta += fmt.Sprintf("(truncated, original_chars=%d)", e.OriginalChars)
			}
		}

		if meta != "" {
			sb.WriteString(fmt.Sprintf("[%d] %s (%s): %s\n", r.Line, e.Role, meta, content))
		} else {
			sb.WriteString(fmt.Sprintf("[%d] %s: %s\n", r.Line, e.Role, content))
		}
		sb.WriteString("\n")
	}

	return sb.String()
}
