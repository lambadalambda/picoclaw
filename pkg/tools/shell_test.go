package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
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
		{"absolute executable path", "/bin/ls -la"},
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

func TestGuardCommand_BlocksChatSlashCommands(t *testing.T) {
	tool := NewExecTool(t.TempDir())

	blocked := []string{
		"/react 123 👍",
		"/set_profile_picture /root/.picoclaw/workspace/avatar.png",
		"/set_profile_photo /root/.picoclaw/workspace/avatar.png",
	}

	for _, cmd := range blocked {
		t.Run(cmd, func(t *testing.T) {
			result := tool.guardCommand(cmd, t.TempDir())
			if result == "" {
				t.Fatalf("expected command %q to be blocked", cmd)
			}
			if !strings.Contains(strings.ToLower(result), "message tool") {
				t.Fatalf("expected guidance to use message tool, got %q", result)
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

	t.Run("working_dir outside workspace", func(t *testing.T) {
		outside := t.TempDir()
		result := tool.guardCommand("ls -la", outside)
		if result == "" {
			t.Fatalf("expected command to be blocked for cwd outside workspace")
		}
		if !strings.Contains(strings.ToLower(result), "working_dir") {
			t.Fatalf("expected working_dir guidance, got %q", result)
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

	t.Run("per-call timeout overrides default", func(t *testing.T) {
		tool.SetTimeout(5 * time.Second)

		start := time.Now()
		result, err := tool.Execute(context.Background(), map[string]interface{}{
			"command":         "sleep 2",
			"timeout_seconds": 0.5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "timed out") {
			t.Fatalf("expected timeout result, got %q", result)
		}

		elapsed := time.Since(start)
		if elapsed >= 1500*time.Millisecond {
			t.Fatalf("expected timeout to trigger quickly, elapsed=%v", elapsed)
		}
	})

	t.Run("invalid per-call timeout returns error", func(t *testing.T) {
		_, err := tool.Execute(context.Background(), map[string]interface{}{
			"command":         "echo hello",
			"timeout_seconds": "soon",
		})
		if err == nil {
			t.Fatal("expected error for invalid timeout_seconds")
		}
		if !strings.Contains(err.Error(), "timeout_seconds") {
			t.Fatalf("expected timeout_seconds error, got %v", err)
		}
	})

	t.Run("timeout kills process group", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("process-group behavior differs on windows")
		}

		marker := filepath.Join(t.TempDir(), "leaked.txt")
		cmd := fmt.Sprintf("(sleep 2; printf leaked > %q) & sleep 30", marker)

		result, err := tool.Execute(context.Background(), map[string]interface{}{
			"command":         cmd,
			"timeout_seconds": 0.5,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "timed out") {
			t.Fatalf("expected timeout result, got %q", result)
		}

		// Give any leaked child process enough time to run.
		time.Sleep(2500 * time.Millisecond)
		if _, statErr := os.Stat(marker); statErr == nil {
			t.Fatalf("expected timed-out command descendants to be killed, but %s was created", marker)
		}
	})
}

func TestExecTool_Execute_RestrictToWorkspaceWorkingDir(t *testing.T) {
	dir := t.TempDir()
	tool := NewExecTool(dir)
	tool.SetRestrictToWorkspace(true)

	t.Run("relative working_dir allowed", func(t *testing.T) {
		sub := filepath.Join(dir, "sub")
		if err := os.MkdirAll(sub, 0755); err != nil {
			t.Fatalf("mkdir failed: %v", err)
		}

		result, err := tool.Execute(context.Background(), map[string]interface{}{
			"command":      "pwd",
			"working_dir":  "sub",
			"timeout_seconds": 2,
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "sub") {
			t.Fatalf("expected pwd output to include sub, got %q", result)
		}
	})

	t.Run("working_dir escape blocked", func(t *testing.T) {
		result, err := tool.Execute(context.Background(), map[string]interface{}{
			"command":     "pwd",
			"working_dir": "..",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !strings.Contains(result, "Error:") {
			t.Fatalf("expected Error: result, got %q", result)
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
