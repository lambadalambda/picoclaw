package tools

import (
	"context"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type ReadFileTool struct{}

func (t *ReadFileTool) Name() string {
	return "read_file"
}

func (t *ReadFileTool) Description() string {
	return "Read file contents. Optional line ranges: start_line + max_lines"
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

	content, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	text := string(content)
	if startLine == 1 && maxLines == 0 {
		return text, nil
	}

	lines := strings.SplitAfter(text, "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = []string{}
	}
	totalLines := len(lines)
	if totalLines == 0 {
		if startLine == 1 {
			return "", nil
		}
		return "", fmt.Errorf("start_line %d exceeds total lines 0", startLine)
	}
	if startLine > totalLines {
		return "", fmt.Errorf("start_line %d exceeds total lines %d", startLine, totalLines)
	}

	start := startLine - 1
	end := totalLines
	if maxLines > 0 && start+maxLines < end {
		end = start + maxLines
	}

	return strings.Join(lines[start:end], ""), nil
}

type WriteFileTool struct{}

func (t *WriteFileTool) Name() string {
	return "write_file"
}

func (t *WriteFileTool) Description() string {
	return "Write content to a file"
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

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("failed to create directory: %w", err)
	}

	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return "File written successfully", nil
}

type ListDirTool struct{}

func (t *ListDirTool) Name() string {
	return "list_dir"
}

func (t *ListDirTool) Description() string {
	return "List files and directories in a path"
}

func (t *ListDirTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Path to list",
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

	entries, err := os.ReadDir(path)
	if err != nil {
		return "", fmt.Errorf("failed to read directory: %w", err)
	}

	result := ""
	for _, entry := range entries {
		if entry.IsDir() {
			result += "DIR:  " + entry.Name() + "\n"
		} else {
			result += "FILE: " + entry.Name() + "\n"
		}
	}

	return result, nil
}
