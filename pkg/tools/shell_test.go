package tools

import (
	"context"
	"strings"
	"testing"
)

func TestGuardCommand_DenyPatterns(t *testing.T) {
	tool := NewExecTool(t.TempDir())

	blocked := []struct {
		name    string
		command string
	}{
		{"rm -rf", "rm -rf /"},
		{"rm -f", "rm -f important.txt"},
		{"rm -r", "rm -r mydir"},
		{"del /f", "del /f somefile"},
		{"del /q", "del /q somefile"},
		{"rmdir /s", "rmdir /s somedir"},
		{"format disk", "format C:"},
		{"mkfs ext4", "mkfs ext4 /dev/sda1"},
		{"diskpart", "diskpart /s script.txt"},
		{"dd if=", "dd if=/dev/zero of=/dev/sda"},
		{"write to disk device", "echo bad > /dev/sda"},
		{"write to disk device sdb", "cat file > /dev/sdb"},
		{"shutdown", "shutdown -h now"},
		{"reboot", "reboot"},
		{"poweroff", "poweroff"},
		{"fork bomb", ":() { :|:& }; :"},
	}

	for _, tt := range blocked {
		t.Run("blocked/"+tt.name, func(t *testing.T) {
			result := tool.guardCommand(tt.command, t.TempDir())
			if result == "" {
				t.Errorf("expected command %q to be blocked, but it was allowed", tt.command)
			}
			if !strings.Contains(result, "dangerous pattern") {
				t.Errorf("expected dangerous pattern message, got %q", result)
			}
		})
	}
}

func TestGuardCommand_SafeCommands(t *testing.T) {
	tool := NewExecTool(t.TempDir())

	allowed := []struct {
		name    string
		command string
	}{
		{"ls", "ls -la"},
		{"cat", "cat file.txt"},
		{"echo", "echo hello"},
		{"grep", "grep -r pattern ."},
		{"find", "find . -name '*.go'"},
		{"go build", "go build ./..."},
		{"go test", "go test ./..."},
		{"git status", "git status"},
		{"mkdir", "mkdir newdir"},
		{"rm single file", "rm file.txt"},
		{"cp", "cp a.txt b.txt"},
		{"mv", "mv a.txt b.txt"},
		{"write to dev null", "echo test > /dev/null"},
		{"python", "python3 script.py"},
		{"curl", "curl https://example.com"},
	}

	for _, tt := range allowed {
		t.Run("allowed/"+tt.name, func(t *testing.T) {
			result := tool.guardCommand(tt.command, t.TempDir())
			if result != "" {
				t.Errorf("expected command %q to be allowed, but got: %s", tt.command, result)
			}
		})
	}
}

func TestGuardCommand_AllowPatterns(t *testing.T) {
	tool := NewExecTool(t.TempDir())
	err := tool.SetAllowPatterns([]string{`^git\s`, `^go\s`})
	if err != nil {
		t.Fatalf("SetAllowPatterns failed: %v", err)
	}

	t.Run("allowed by allowlist", func(t *testing.T) {
		result := tool.guardCommand("git status", t.TempDir())
		if result != "" {
			t.Errorf("expected 'git status' to be allowed, got: %s", result)
		}
	})

	t.Run("allowed by allowlist go", func(t *testing.T) {
		result := tool.guardCommand("go test ./...", t.TempDir())
		if result != "" {
			t.Errorf("expected 'go test' to be allowed, got: %s", result)
		}
	})

	t.Run("blocked by allowlist", func(t *testing.T) {
		result := tool.guardCommand("ls -la", t.TempDir())
		if result == "" {
			t.Error("expected 'ls -la' to be blocked by allowlist")
		}
		if !strings.Contains(result, "not in allowlist") {
			t.Errorf("expected allowlist message, got %q", result)
		}
	})

	t.Run("deny takes precedence over allow", func(t *testing.T) {
		// Even if "go" is allowed, a dangerous pattern should still be blocked
		// (deny is checked first)
		result := tool.guardCommand("rm -rf /", t.TempDir())
		if result == "" {
			t.Error("expected dangerous command to be blocked even with allowlist")
		}
	})
}

func TestGuardCommand_RestrictToWorkspace(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecTool(dir)
	tool.SetRestrictToWorkspace(true)

	t.Run("path traversal with ..", func(t *testing.T) {
		result := tool.guardCommand("cat ../../../etc/passwd", dir)
		if result == "" {
			t.Error("expected path traversal to be blocked")
		}
	})

	t.Run("path traversal with backslash", func(t *testing.T) {
		result := tool.guardCommand(`cat ..\..\windows\system32\config`, dir)
		if result == "" {
			t.Error("expected backslash path traversal to be blocked")
		}
	})

	t.Run("command within workspace", func(t *testing.T) {
		result := tool.guardCommand("cat file.txt", dir)
		if result != "" {
			t.Errorf("expected workspace-local command to be allowed, got: %s", result)
		}
	})
}

func TestExecTool_Execute(t *testing.T) {
	tool := NewExecTool(t.TempDir())

	t.Run("simple echo", func(t *testing.T) {
		result, err := tool.Execute(context.Background(), map[string]interface{}{
			"command": "echo hello",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "hello") {
			t.Errorf("expected 'hello' in output, got %q", result)
		}
	})

	t.Run("blocked command returns error string not error", func(t *testing.T) {
		result, err := tool.Execute(context.Background(), map[string]interface{}{
			"command": "rm -rf /",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "Error:") {
			t.Errorf("expected Error: prefix in result, got %q", result)
		}
	})

	t.Run("missing command returns error", func(t *testing.T) {
		_, err := tool.Execute(context.Background(), map[string]interface{}{})
		if err == nil {
			t.Error("expected error for missing command")
		}
	})
}

func TestSetAllowPatterns_InvalidRegex(t *testing.T) {
	tool := NewExecTool(t.TempDir())
	err := tool.SetAllowPatterns([]string{`[invalid`})
	if err == nil {
		t.Error("expected error for invalid regex pattern")
	}
}
