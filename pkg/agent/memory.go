// PicoClaw - Ultra-lightweight personal AI agent
// Inspired by and based on nanobot: https://github.com/HKUDS/nanobot
// License: MIT
//
// Copyright (c) 2026 PicoClaw contributors

package agent

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

const memoryContextFileMaxChars = 40 * 1024

const memoryContextTrimNotice = "[... older memory entries omitted from context; use memory_search to access full history ...]"

// MemoryStore manages persistent memory for the agent.
// - Long-term memory: memory/MEMORY.md
// - Daily notes: memory/YYYYMM/YYYYMMDD.md
type MemoryStore struct {
	workspace  string
	memoryDir  string
	memoryFile string
}

// NewMemoryStore creates a new MemoryStore with the given workspace path.
// It ensures the memory directory exists.
func NewMemoryStore(workspace string) *MemoryStore {
	memoryDir := filepath.Join(workspace, "memory")
	memoryFile := filepath.Join(memoryDir, "MEMORY.md")

	// Ensure memory directory exists
	os.MkdirAll(memoryDir, 0755)

	return &MemoryStore{
		workspace:  workspace,
		memoryDir:  memoryDir,
		memoryFile: memoryFile,
	}
}

// getTodayFile returns the path to today's daily note file (memory/YYYYMM/YYYYMMDD.md).
func (ms *MemoryStore) getTodayFile() string {
	today := time.Now().Format("20060102") // YYYYMMDD
	monthDir := today[:6]                  // YYYYMM
	filePath := filepath.Join(ms.memoryDir, monthDir, today+".md")
	return filePath
}

// ReadLongTerm reads the long-term memory (MEMORY.md).
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadLongTerm() string {
	return ms.readFileCapped(ms.memoryFile)
}

// WriteLongTerm writes content to the long-term memory file (MEMORY.md).
func (ms *MemoryStore) WriteLongTerm(content string) error {
	content = capMemoryContextContent(content)
	return os.WriteFile(ms.memoryFile, []byte(content), 0644)
}

// ReadToday reads today's daily note.
// Returns empty string if the file doesn't exist.
func (ms *MemoryStore) ReadToday() string {
	return ms.readFileCapped(ms.getTodayFile())
}

// AppendToday appends content to today's daily note.
// If the file doesn't exist, it creates a new file with a date header.
func (ms *MemoryStore) AppendToday(content string) error {
	todayFile := ms.getTodayFile()

	// Ensure month directory exists
	monthDir := filepath.Dir(todayFile)
	os.MkdirAll(monthDir, 0755)

	var existingContent string
	if data, err := os.ReadFile(todayFile); err == nil {
		existingContent = string(data)
	}

	var newContent string
	if existingContent == "" {
		// Add header for new day
		header := fmt.Sprintf("# %s\n\n", time.Now().Format("2006-01-02"))
		newContent = header + content
	} else {
		// Append to existing content
		newContent = existingContent + "\n" + content
	}

	newContent = capMemoryContextContent(newContent)

	return os.WriteFile(todayFile, []byte(newContent), 0644)
}

// GetRecentDailyNotes returns daily notes from the last N days.
// Contents are joined with "---" separator.
func (ms *MemoryStore) GetRecentDailyNotes(days int) string {
	var notes []string

	for i := 0; i < days; i++ {
		date := time.Now().AddDate(0, 0, -i)
		dateStr := date.Format("20060102") // YYYYMMDD
		monthDir := dateStr[:6]            // YYYYMM
		filePath := filepath.Join(ms.memoryDir, monthDir, dateStr+".md")

		note := ms.readFileCapped(filePath)
		if note != "" {
			notes = append(notes, note)
		}
	}

	if len(notes) == 0 {
		return ""
	}

	// Join with separator
	var result string
	for i, note := range notes {
		if i > 0 {
			result += "\n\n---\n\n"
		}
		result += note
	}
	return result
}

// GetMemoryContext returns formatted memory context for the agent prompt.
// Includes long-term memory and recent daily notes.
func (ms *MemoryStore) GetMemoryContext() string {
	var parts []string
	longTermBytes := 0
	recentNotesBytes := 0

	// Long-term memory
	longTerm := ms.ReadLongTerm()
	if longTerm != "" {
		parts = append(parts, "## Long-term Memory\n\n"+longTerm)
		longTermBytes = len([]byte(longTerm))
	}

	// Recent daily notes (last 3 days)
	recentNotes := ms.GetRecentDailyNotes(3)
	if recentNotes != "" {
		parts = append(parts, "## Recent Daily Notes\n\n"+recentNotes)
		recentNotesBytes = len([]byte(recentNotes))
	}

	if len(parts) == 0 {
		logger.InfoCF("agent", "Memory context prepared",
			map[string]interface{}{
				"memory_context_chars": 0,
				"memory_context_bytes": 0,
				"long_term_bytes":      longTermBytes,
				"recent_notes_bytes":   recentNotesBytes,
			})
		return ""
	}

	// Join parts with separator
	var result string
	for i, part := range parts {
		if i > 0 {
			result += "\n\n---\n\n"
		}
		result += part
	}
	context := fmt.Sprintf("# Memory\n\n%s", result)
	logger.InfoCF("agent", "Memory context prepared",
		map[string]interface{}{
			"memory_context_chars": len(context),
			"memory_context_bytes": len([]byte(context)),
			"long_term_bytes":      longTermBytes,
			"recent_notes_bytes":   recentNotesBytes,
		})

	return context
}

func (ms *MemoryStore) readFileCapped(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return capMemoryContextContent(string(data))
}

func capMemoryContextContent(content string) string {
	if len(content) <= memoryContextFileMaxChars {
		return content
	}

	header, body := splitMemoryHeader(content)
	trimPrefix := memoryContextTrimNotice + "\n\n"
	body = strings.TrimPrefix(body, trimPrefix)

	keep := memoryContextFileMaxChars - len(header) - len(trimPrefix)
	if keep <= 0 {
		header = ""
		keep = memoryContextFileMaxChars - len(trimPrefix)
		if keep < 0 {
			keep = 0
			trimPrefix = ""
		}
	}

	if len(body) > keep {
		body = body[len(body)-keep:]
		if idx := strings.IndexByte(body, '\n'); idx >= 0 && idx < len(body)-1 {
			body = body[idx+1:]
		}
	}

	result := header + trimPrefix + body
	if len(result) > memoryContextFileMaxChars {
		result = result[len(result)-memoryContextFileMaxChars:]
	}
	return result
}

func splitMemoryHeader(content string) (string, string) {
	if !strings.HasPrefix(content, "#") {
		return "", content
	}

	if sep := strings.Index(content, "\n\n"); sep >= 0 {
		return content[:sep+2], content[sep+2:]
	}

	if lineEnd := strings.IndexByte(content, '\n'); lineEnd >= 0 {
		return content[:lineEnd+1], content[lineEnd+1:]
	}

	return content + "\n\n", ""
}
