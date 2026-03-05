package tools

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// EditFileTool edits a file by replacing old_text with new_text.
// The old_text must exist exactly in the file.
type EditFileTool struct {
	name                string
	allowedDir          string // Optional directory restriction for security
	restrictToWorkspace bool
}

// NewEditFileTool creates a new EditFileTool with optional directory restriction.
func NewEditFileTool(allowedDir string) *EditFileTool {
	return &EditFileTool{
		name:                "edit_file",
		allowedDir:          allowedDir,
		restrictToWorkspace: true,
	}
}

func NewUnsafeEditFileTool() *EditFileTool {
	return &EditFileTool{
		name:                "unsafe_edit_file",
		allowedDir:          "",
		restrictToWorkspace: false,
	}
}

func (t *EditFileTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *EditFileTool) Name() string {
	if t != nil && t.name != "" {
		return t.name
	}
	return "edit_file"
}

func (t *EditFileTool) Description() string {
	if t != nil && strings.HasPrefix(strings.ToLower(t.Name()), "unsafe_") {
		return "Edit a file by replacing old_text with new_text at any path (unsafe). The old_text must exist exactly in the file."
	}
	if t != nil && !t.restrictToWorkspace {
		return "Edit a file by replacing old_text with new_text at any path. The old_text must exist exactly in the file."
	}
	return "Edit a file by replacing old_text with new_text under the workspace. The old_text must exist exactly in the file."
}

func (t *EditFileTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"path": map[string]interface{}{
				"type":        "string",
				"description": "The file path to edit",
			},
			"old_text": map[string]interface{}{
				"type":        "string",
				"description": "The exact text to find and replace",
			},
			"new_text": map[string]interface{}{
				"type":        "string",
				"description": "The text to replace with",
			},
		},
		"required": []string{"path", "old_text", "new_text"},
	}
}

func (t *EditFileTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	path, ok := args["path"].(string)
	if !ok {
		return "", fmt.Errorf("path is required")
	}

	oldText, ok := args["old_text"].(string)
	if !ok {
		return "", fmt.Errorf("old_text is required")
	}

	newText, ok := args["new_text"].(string)
	if !ok {
		return "", fmt.Errorf("new_text is required")
	}

	resolvedPath, err := resolvePathWithOptionalRootMode(path, t.allowedDir, "workspace", t.restrictToWorkspace)
	if err != nil {
		return "", err
	}

	if _, err := os.Stat(resolvedPath); os.IsNotExist(err) {
		return "", fmt.Errorf("file not found: %s", path)
	}

	content, err := os.ReadFile(resolvedPath)
	if err != nil {
		return "", fmt.Errorf("failed to read file: %w", err)
	}

	contentStr := string(content)

	if !strings.Contains(contentStr, oldText) {
		return "", fmt.Errorf("old_text not found in file. Make sure it matches exactly")
	}

	count := strings.Count(contentStr, oldText)
	if count > 1 {
		return "", fmt.Errorf("old_text appears %d times. Please provide more context to make it unique", count)
	}

	newContent := strings.Replace(contentStr, oldText, newText, 1)

	if err := os.WriteFile(resolvedPath, []byte(newContent), 0644); err != nil {
		return "", fmt.Errorf("failed to write file: %w", err)
	}

	return fmt.Sprintf("Successfully edited %s", path), nil
}
