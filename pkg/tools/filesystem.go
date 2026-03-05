package tools

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	filesystemReadFileMaxBytes  = 50000
	filesystemWriteFileMaxBytes = 200000
	filesystemListDirMaxEntries = 500
)

const filesystemTruncationNotice = "\n... (truncated)"

type ReadFileTool struct {
	name                string
	allowedDir          string
	restrictToWorkspace bool
}

func NewReadFileTool(allowedDir string) *ReadFileTool {
	return &ReadFileTool{name: "read_file", allowedDir: allowedDir, restrictToWorkspace: true}
}

func NewUnsafeReadFileTool() *ReadFileTool {
	return &ReadFileTool{name: "unsafe_read_file", restrictToWorkspace: false}
}

func (t *ReadFileTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *ReadFileTool) Name() string {
	if t != nil && t.name != "" {
		return t.name
	}
	return "read_file"
}

func (t *ReadFileTool) Description() string {
	if t != nil && strings.HasPrefix(strings.ToLower(t.Name()), "unsafe_") {
		return "Read file contents from any path (unsafe). Optional line ranges: start_line + max_lines"
	}
	if t != nil && !t.restrictToWorkspace {
		return "Read file contents from any path. Optional line ranges: start_line + max_lines"
	}
	return "Read file contents from the workspace. Optional line ranges: start_line + max_lines"
}

func (t *ReadFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to read",
			},
			"start_line": map[string]interface{}{
				"type":        "integer",
				"description": "Optional: 1-based start line (default: 1)",
			},
			"max_lines": map[string]interface{}{
				"type":        "integer",
				"description": "Optional: max number of lines to return (default: all)",
			},
		},
		"required": []string{"path"},
	}
}

func parseOptionalIntArg(args map[string]interface{}, key string, defaultValue int) (int, error) {
	raw, exists := args[key]
	if !exists || raw == nil {
		return defaultValue, nil
	}

	switch v := raw.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		return int(v), nil
	case uint:
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		return int(v), nil
	case uint64:
		return int(v), nil
	case float64:
		if math.Trunc(v) != v {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return int(v), nil
	case float32:
		if math.Trunc(float64(v)) != float64(v) {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return int(v), nil
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(v))
		if err != nil {
			return 0, fmt.Errorf("%s must be an integer", key)
		}
		return i, nil
	default:
		return 0, fmt.Errorf("%s must be an integer", key)
	}
}

func (t *ReadFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	resolvedPath, err := resolvePathWithOptionalRootMode(path, t.allowedDir, "workspace", t.restrictToWorkspace)
	if err != nil {
		return "", err
	}

	startLine, err := parseOptionalIntArg(args, "start_line", 1)
	if err != nil {
		return "", err
	}
	maxLines, err := parseOptionalIntArg(args, "max_lines", 0)
	if err != nil {
		return "", err
	}
	if startLine < 1 {
		return "", fmt.Errorf("start_line must be >= 1")
	}
	if maxLines < 0 {
		return "", fmt.Errorf("max_lines must be >= 0")
	}

	f, err := os.Open(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	defer f.Close()

	if startLine == 1 && maxLines == 0 {
		data, truncated, err := readBytesWithCap(f, filesystemReadFileMaxBytes)
		if err != nil {
			return "", fmt.Errorf("failed to read file: %w", err)
		}
		if truncated {
			return string(data) + filesystemTruncationNotice, nil
		}
		return string(data), nil
	}

	data, truncated, totalLines, err := readLineRangeWithCap(f, startLine, maxLines, filesystemReadFileMaxBytes)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}
	if totalLines == 0 {
		if startLine == 1 {
			return "", nil
		}
		return "", fmt.Errorf("start_line %d exceeds total lines 0", startLine)
	}
	if startLine > totalLines {
		return "", fmt.Errorf("start_line %d exceeds total lines %d", startLine, totalLines)
	}

	if truncated {
		return string(data) + filesystemTruncationNotice, nil
	}
	return string(data), nil
}

type WriteFileTool struct {
	name                string
	allowedDir          string
	restrictToWorkspace bool
}

func NewWriteFileTool(allowedDir string) *WriteFileTool {
	return &WriteFileTool{name: "write_file", allowedDir: allowedDir, restrictToWorkspace: true}
}

func NewUnsafeWriteFileTool() *WriteFileTool {
	return &WriteFileTool{name: "unsafe_write_file", restrictToWorkspace: false}
}

func (t *WriteFileTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *WriteFileTool) Name() string {
	if t != nil && t.name != "" {
		return t.name
	}
	return "write_file"
}

func (t *WriteFileTool) Description() string {
	if t != nil && strings.HasPrefix(strings.ToLower(t.Name()), "unsafe_") {
		return "Write content to a file at any path (unsafe)"
	}
	if t != nil && !t.restrictToWorkspace {
		return "Write content to a file at any path"
	}
	return "Write content to a file under the workspace"
}

func (t *WriteFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to the file to write",
			},
			"content": map[string]interface{}{
				"type":        "string",
				"description": "Content to write to the file",
			},
			"append": map[string]interface{}{
				"type":        "boolean",
				"description": "If true, append content to the file instead of overwriting it (default false)",
			},
		},
		"required": []string{"path", "content"},
	}
}

func (t *WriteFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	content, ok := args["content"].(string)
	if !ok {
		return "", fmt.Errorf("content is required")
	}
	if len(content) > filesystemWriteFileMaxBytes {
		return "", fmt.Errorf("content too large (max %d bytes)", filesystemWriteFileMaxBytes)
	}

	resolvedPath, err := resolvePathWithOptionalRootMode(path, t.allowedDir, "workspace", t.restrictToWorkspace)
	if err != nil {
		return "", err
	}

	dir := filepath.Dir(resolvedPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	appendMode, _ := args["append"].(bool)
	if appendMode {
		f, err := os.OpenFile(resolvedPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return "", fmt.Errorf("failed to open file: %w", err)
		}
		defer f.Close()

		if _, err := f.WriteString(content); err != nil {
			return "", fmt.Errorf("failed to append to file: %w", err)
		}

		return "File appended successfully", nil
	}

	if err := os.WriteFile(resolvedPath, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return "File written successfully", nil
}

type ListDirTool struct {
	name                string
	allowedDir          string
	restrictToWorkspace bool
}

func NewListDirTool(allowedDir string) *ListDirTool {
	return &ListDirTool{name: "list_dir", allowedDir: allowedDir, restrictToWorkspace: true}
}

func NewUnsafeListDirTool() *ListDirTool {
	return &ListDirTool{name: "unsafe_list_dir", restrictToWorkspace: false}
}

func (t *ListDirTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *ListDirTool) Name() string {
	if t != nil && t.name != "" {
		return t.name
	}
	return "list_dir"
}

func (t *ListDirTool) Description() string {
	if t != nil && strings.HasPrefix(strings.ToLower(t.Name()), "unsafe_") {
		return "List files and directories in any path (unsafe)"
	}
	if t != nil && !t.restrictToWorkspace {
		return "List files and directories in any path"
	}
	return "List files and directories under the workspace"
}

func (t *ListDirTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to list",
			},
			"offset": map[string]interface{}{
				"type":        "integer",
				"description": "Optional: 0-based offset into the sorted entries (default: 0)",
			},
			"limit": map[string]interface{}{
				"type":        "integer",
				"description": "Optional: max entries to return (default: capped)",
			},
		},
		"required": []string{"path"},
	}
}

func (t *ListDirTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		path = "."
	}

	resolvedPath, err := resolvePathWithOptionalRootMode(path, t.allowedDir, "workspace", t.restrictToWorkspace)
	if err != nil {
		return "", err
	}

	entries, err := os.ReadDir(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %w", err)
	}
	total := len(entries)

	offset, err := parseOptionalIntArg(args, "offset", 0)
	if err != nil {
		return "", err
	}
	if offset < 0 {
		return "", fmt.Errorf("offset must be >= 0")
	}
	limit, err := parseOptionalIntArg(args, "limit", 0)
	if err != nil {
		return "", err
	}
	if limit < 0 {
		return "", fmt.Errorf("limit must be >= 0")
	}

	maxEntries := filesystemListDirMaxEntries
	if maxEntries <= 0 {
		maxEntries = 500
	}
	if limit <= 0 || limit > maxEntries {
		limit = maxEntries
	}

	start := offset
	if start > len(entries) {
		start = len(entries)
	}
	end := start + limit
	if end > total {
		end = total
	}
	partial := offset > 0 || end < total

	entries = entries[start:end]

	var sb strings.Builder
	for _, entry := range entries {
		if entry.IsDir() {
			sb.WriteString("DIR:  ")
			sb.WriteString(entry.Name())
			sb.WriteString("\n")
		} else {
			sb.WriteString("FILE: ")
			sb.WriteString(entry.Name())
			sb.WriteString("\n")
		}
	}
	result := sb.String()
	if partial {
		result += filesystemTruncationNotice
	}
	return result, nil
}

func readBytesWithCap(r io.Reader, maxBytes int) ([]byte, bool, error) {
	if maxBytes <= 0 {
		maxBytes = 50000
	}
	limited := &io.LimitedReader{R: r, N: int64(maxBytes + 1)}
	b, err := io.ReadAll(limited)
	if err != nil {
		return nil, false, err
	}
	if len(b) > maxBytes {
		return b[:maxBytes], true, nil
	}
	return b, false, nil
}

func readLineRangeWithCap(r io.Reader, startLine, maxLines, maxBytes int) ([]byte, bool, int, error) {
	if startLine < 1 {
		return nil, false, 0, fmt.Errorf("start_line must be >= 1")
	}
	if maxLines < 0 {
		return nil, false, 0, fmt.Errorf("max_lines must be >= 0")
	}
	if maxBytes <= 0 {
		maxBytes = 50000
	}

	var buf strings.Builder
	buf.Grow(minInt(maxBytes, 4096))

	br := bufio.NewReaderSize(r, 64*1024)
	newlineCount := 0
	sawAny := false
	truncated := false

	endLineExclusive := 0
	lastLineWanted := 0
	if maxLines > 0 {
		endLineExclusive = startLine + maxLines
		lastLineWanted = endLineExclusive - 1
	}

	for {
		currentLine := newlineCount + 1
		seg, err := br.ReadSlice('\n')
		if len(seg) > 0 {
			sawAny = true
		}

		collect := currentLine >= startLine
		if collect && maxLines > 0 {
			collect = currentLine < endLineExclusive
		}
		if collect && len(seg) > 0 {
			remaining := maxBytes - buf.Len()
			if remaining <= 0 {
				truncated = true
				break
			}
			if len(seg) > remaining {
				buf.WriteString(string(seg[:remaining]))
				truncated = true
				break
			}
			buf.WriteString(string(seg))
		}

		if len(seg) > 0 && seg[len(seg)-1] == '\n' {
			newlineCount++
			if maxLines > 0 && newlineCount >= lastLineWanted {
				break
			}
		}

		if err != nil {
			if err == bufio.ErrBufferFull {
				continue
			}
			if err == io.EOF {
				break
			}
			return nil, false, 0, err
		}
	}

	totalLines := 0
	if sawAny {
		totalLines = newlineCount + 1
	}

	return []byte(buf.String()), truncated, totalLines, nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func resolvePathWithOptionalRoot(rawPath string, allowedDir string, allowedLabel string) (string, error) {
	return resolvePathWithOptionalRootMode(rawPath, allowedDir, allowedLabel, true)
}

func resolvePathWithOptionalRootMode(rawPath string, allowedDir string, allowedLabel string, enforceWithinRoot bool) (string, error) {
	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("path is required")
	}

	// No root restriction: preserve existing semantics.
	if strings.TrimSpace(allowedDir) == "" {
		return rawPath, nil
	}

	allowedAbs, err := filepath.Abs(allowedDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve %s root: %w", allowedLabel, err)
	}
	allowedAbs = filepath.Clean(allowedAbs)

	resolved := ""
	if filepath.IsAbs(rawPath) {
		resolved = filepath.Clean(rawPath)
	} else {
		resolved = filepath.Clean(filepath.Join(allowedAbs, rawPath))
	}

	if !enforceWithinRoot {
		return resolved, nil
	}

	rel, err := filepath.Rel(allowedAbs, resolved)
	if err != nil {
		return "", fmt.Errorf("failed to validate path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		label := allowedLabel
		if label == "" {
			label = "allowed directory"
		}
		return "", fmt.Errorf("path %s is outside %s", rawPath, label)
	}

	return resolved, nil
}
