package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type ExecTool struct {
	name                string
	workingDir          string
	timeout             time.Duration
	denyPatterns        []*regexp.Regexp
	allowPatterns       []*regexp.Regexp
	restrictToWorkspace bool
	disableGuards       bool
}

func NewExecTool(workingDir string) *ExecTool {
	denyPatterns := []*regexp.Regexp{
		regexp.MustCompile(`\brm\s+-[rf]{1,2}\b`),
		regexp.MustCompile(`\bdel\s+/[fq]\b`),
		regexp.MustCompile(`\brmdir\s+/s\b`),
		regexp.MustCompile(`\b(format|mkfs|diskpart)\b\s`), // Match disk wiping commands (must be followed by space/args)
		regexp.MustCompile(`\bdd\s+if=`),
		regexp.MustCompile(`>\s*/dev/sd[a-z]\b`), // Block writes to disk devices (but allow /dev/null)
		regexp.MustCompile(`\b(shutdown|reboot|poweroff)\b`),
		regexp.MustCompile(`:\(\)\s*\{.*\};\s*:`),
	}

	return &ExecTool{
		name:                "",
		workingDir:          workingDir,
		timeout:             60 * time.Second,
		denyPatterns:        denyPatterns,
		allowPatterns:       nil,
		restrictToWorkspace: false,
		disableGuards:       false,
	}
}

func NewUnsafeExecTool(workingDir string) *ExecTool {
	t := NewExecTool(workingDir)
	t.name = "unsafe_exec"
	return t
}

func (t *ExecTool) Name() string {
	if t != nil && t.name != "" {
		return t.name
	}
	return "exec"
}

func (t *ExecTool) Description() string {
	if t != nil && strings.HasPrefix(strings.ToLower(t.Name()), "unsafe_") {
		return "Execute a shell command and return its output (unsafe: can run outside the workspace). " +
			"Do not use this for chat slash commands (for example /react or /set_profile_picture); use the message tool for those."
	}
	if t != nil && t.disableGuards {
		return "Execute a shell command and return its output (safeguards disabled; unrestricted). " +
			"Do not use this for chat slash commands (for example /react or /set_profile_picture); use the message tool for those."
	}
	return "Execute a shell command and return its output (workspace-scoped). Use with caution. " +
		"Do not use this for chat slash commands (for example /react or /set_profile_picture); use the message tool for those."
}

func (t *ExecTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command": map[string]interface{}{
				"type":        "string",
				"description": "The shell command to execute",
			},
			"working_dir": map[string]interface{}{
				"type":        "string",
				"description": "Optional working directory for the command",
			},
			"timeout_seconds": map[string]interface{}{
				"type":        "number",
				"description": "Optional per-command timeout in seconds (must be > 0). Overrides the default timeout for this call.",
			},
		},
		"required": []string{"command"},
	}
}

func (t *ExecTool) Execute(ctx context.Context, args map[string]interface{}) (string, error) {
	command, ok := args["command"].(string)
	if !ok {
		return "", fmt.Errorf("command is required")
	}

	cwd := t.workingDir
	if wd, ok := args["working_dir"].(string); ok && strings.TrimSpace(wd) != "" {
		cwd = wd
	}

	if t.restrictToWorkspace {
		resolvedCwd, err := resolvePathWithOptionalRoot(cwd, t.workingDir, "workspace")
		if err != nil {
			return fmt.Sprintf("Error: %s", err.Error()), nil
		}
		cwd = resolvedCwd
	}

	if cwd == "" {
		wd, err := os.Getwd()
		if err == nil {
			cwd = wd
		}
	}

	if !t.disableGuards {
		if guardError := t.guardCommand(command, cwd); guardError != "" {
			return fmt.Sprintf("Error: %s", guardError), nil
		}
	}

	effectiveTimeout, err := resolveExecTimeout(args, t.timeout)
	if err != nil {
		return "", err
	}

	cmdCtx := ctx
	cancel := func() {}
	if effectiveTimeout > 0 {
		cmdCtx, cancel = context.WithTimeout(ctx, effectiveTimeout)
	}
	defer cancel()

	cmd := exec.Command("sh", "-c", command)
	configureExecCommand(cmd)
	if cwd != "" {
		cmd.Dir = cwd
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err = runCommandWithContext(cmdCtx, cmd)
	output := stdout.String()
	if stderr.Len() > 0 {
		output += "\nSTDERR:\n" + stderr.String()
	}

	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || cmdCtx.Err() == context.DeadlineExceeded {
			if effectiveTimeout > 0 {
				return fmt.Sprintf("Error: Command timed out after %v", effectiveTimeout), nil
			}
			return "Error: Command timed out", nil
		}
		output += fmt.Sprintf("\nExit code: %v", err)
	}

	if output == "" {
		output = "(no output)"
	}

	maxLen := 10000
	if len(output) > maxLen {
		output = output[:maxLen] + fmt.Sprintf("\n... (truncated, %d more chars)", len(output)-maxLen)
	}

	return output, nil
}

func (t *ExecTool) guardCommand(command, cwd string) string {
	cmd := strings.TrimSpace(command)
	lower := strings.ToLower(cmd)

	if looksLikeChatSlashCommand(cmd) {
		return "This looks like a chat slash command. Use the message tool instead of exec."
	}

	for _, pattern := range t.denyPatterns {
		if pattern.MatchString(lower) {
			return "Command blocked by safety guard (dangerous pattern detected)"
		}
	}

	if len(t.allowPatterns) > 0 {
		allowed := false
		for _, pattern := range t.allowPatterns {
			if pattern.MatchString(lower) {
				allowed = true
				break
			}
		}
		if !allowed {
			return "Command blocked by safety guard (not in allowlist)"
		}
	}

	if t.restrictToWorkspace {
		workspaceRoot := strings.TrimSpace(t.workingDir)
		if workspaceRoot == "" {
			workspaceRoot = cwd
		}

		workspaceAbs, err := filepath.Abs(workspaceRoot)
		if err == nil {
			workspaceAbs = filepath.Clean(workspaceAbs)
		}

		if strings.Contains(cmd, "..\\") || strings.Contains(cmd, "../") {
			return "Command blocked by safety guard (path traversal detected)"
		}

		cwdPath, err := filepath.Abs(cwd)
		if err != nil {
			return ""
		}
		cwdPath = filepath.Clean(cwdPath)

		if workspaceAbs != "" {
			relCwd, err := filepath.Rel(workspaceAbs, cwdPath)
			if err != nil {
				return ""
			}
			if relCwd == ".." || strings.HasPrefix(relCwd, ".."+string(os.PathSeparator)) {
				return "Command blocked by safety guard (working_dir outside workspace)"
			}
		}

		// NOTE: We only want to treat *actual* absolute filesystem paths as candidates.
		// The previous implementation matched any "/..." substring anywhere in the command,
		// which caused false positives for relative paths like "workflows/alice.json" (matched
		// the "/alice.json" substring). We now require a boundary that indicates the path is
		// starting (whitespace, quotes, or '='), plus a special-case for single-letter short
		// flags like "-C/tmp".
		absolutePathPattern := regexp.MustCompile(`(^|[\s"'=])([A-Za-z]:\\[^\s\"']+|/[^\s\"']+)`)
		shortFlagPathPattern := regexp.MustCompile(`(^|[\s"'=])-[A-Za-z]([A-Za-z]:\\[^\s\"']+|/[^\s\"']+)`)

		type pathCandidate struct {
			raw   string
			start int
		}

		candidates := make([]pathCandidate, 0, 8)
		for _, m := range absolutePathPattern.FindAllStringSubmatchIndex(cmd, -1) {
			if len(m) < 6 {
				continue
			}
			start, end := m[4], m[5]
			if start < 0 || end < 0 || start >= end {
				continue
			}
			candidates = append(candidates, pathCandidate{raw: cmd[start:end], start: start})
		}
		for _, m := range shortFlagPathPattern.FindAllStringSubmatchIndex(cmd, -1) {
			if len(m) < 6 {
				continue
			}
			start, end := m[4], m[5]
			if start < 0 || end < 0 || start >= end {
				continue
			}
			candidates = append(candidates, pathCandidate{raw: cmd[start:end], start: start})
		}

		for _, c := range candidates {
			raw := c.raw
			if c.start == 0 {
				// Allow absolute executable paths like /bin/ls.
				continue
			}
			if raw == "/dev/null" || strings.EqualFold(raw, "NUL") {
				continue
			}

			p, err := filepath.Abs(raw)
			if err != nil {
				continue
			}
			p = filepath.Clean(p)

			base := cwdPath
			if workspaceAbs != "" {
				base = workspaceAbs
			}

			rel, err := filepath.Rel(base, p)
			if err != nil {
				continue
			}

			if strings.HasPrefix(rel, "..") {
				if workspaceAbs != "" {
					return "Command blocked by safety guard (path outside workspace)"
				}
				return "Command blocked by safety guard (path outside working dir)"
			}
		}
	}

	return ""
}

func looksLikeChatSlashCommand(command string) bool {
	parts := strings.Fields(strings.TrimSpace(command))
	if len(parts) == 0 {
		return false
	}

	first := strings.ToLower(parts[0])
	switch first {
	case "/react", "/set_profile_picture", "/set_profile_photo":
		return true
	default:
		return false
	}
}

func (t *ExecTool) SetTimeout(timeout time.Duration) {
	t.timeout = timeout
}

func (t *ExecTool) SetRestrictToWorkspace(restrict bool) {
	t.restrictToWorkspace = restrict
}

func (t *ExecTool) SetDisableGuards(disable bool) {
	t.disableGuards = disable
}

func (t *ExecTool) SetAllowPatterns(patterns []string) error {
	t.allowPatterns = make([]*regexp.Regexp, 0, len(patterns))
	for _, p := range patterns {
		re, err := regexp.Compile(p)
		if err != nil {
			return fmt.Errorf("invalid allow pattern %q: %w", p, err)
		}
		t.allowPatterns = append(t.allowPatterns, re)
	}
	return nil
}

func resolveExecTimeout(args map[string]interface{}, defaultTimeout time.Duration) (time.Duration, error) {
	raw, exists := args["timeout_seconds"]
	if !exists || raw == nil {
		return defaultTimeout, nil
	}

	seconds, err := parseTimeoutSeconds(raw)
	if err != nil {
		return 0, err
	}
	return seconds, nil
}

func parseTimeoutSeconds(raw interface{}) (time.Duration, error) {
	var seconds float64

	switch v := raw.(type) {
	case float64:
		seconds = v
	case float32:
		seconds = float64(v)
	case int:
		seconds = float64(v)
	case int8:
		seconds = float64(v)
	case int16:
		seconds = float64(v)
	case int32:
		seconds = float64(v)
	case int64:
		seconds = float64(v)
	case uint:
		seconds = float64(v)
	case uint8:
		seconds = float64(v)
	case uint16:
		seconds = float64(v)
	case uint32:
		seconds = float64(v)
	case uint64:
		seconds = float64(v)
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, fmt.Errorf("timeout_seconds must be a positive number: %w", err)
		}
		seconds = f
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		if err != nil {
			return 0, fmt.Errorf("timeout_seconds must be a positive number: %w", err)
		}
		seconds = f
	default:
		return 0, fmt.Errorf("timeout_seconds must be a positive number")
	}

	if seconds <= 0 {
		return 0, fmt.Errorf("timeout_seconds must be greater than 0")
	}

	return time.Duration(seconds * float64(time.Second)), nil
}

func runCommandWithContext(ctx context.Context, cmd *exec.Cmd) error {
	if err := cmd.Start(); err != nil {
		return err
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		_ = killExecCommand(cmd)
		select {
		case <-done:
			return ctx.Err()
		case <-time.After(2 * time.Second):
			return ctx.Err()
		}
	}
}
