package tools

import (
	"fmt"
	"strings"
)

// ToolExecutionPolicy controls which tools may execute.
//
// Behavior:
// - Enabled=false: no policy checks.
// - Deny list always blocks matching tools.
// - If Allow list is non-empty, only listed tools may run.
type ToolExecutionPolicy struct {
	Enabled bool
	Allow   map[string]struct{}
	Deny    map[string]struct{}
}

func NewToolExecutionPolicy(enabled bool, allow []string, deny []string) ToolExecutionPolicy {
	p := ToolExecutionPolicy{Enabled: enabled}
	if len(allow) > 0 {
		p.Allow = make(map[string]struct{}, len(allow))
		for _, name := range allow {
			name = strings.TrimSpace(strings.ToLower(name))
			if name == "" {
				continue
			}
			p.Allow[name] = struct{}{}
		}
	}
	if len(deny) > 0 {
		p.Deny = make(map[string]struct{}, len(deny))
		for _, name := range deny {
			name = strings.TrimSpace(strings.ToLower(name))
			if name == "" {
				continue
			}
			p.Deny[name] = struct{}{}
		}
	}
	return p
}

func (p ToolExecutionPolicy) check(toolName string) error {
	if !p.Enabled {
		return nil
	}

	name := strings.ToLower(strings.TrimSpace(toolName))
	if name == "" {
		return fmt.Errorf("tool name is empty")
	}

	if _, denied := p.Deny[name]; denied {
		return fmt.Errorf("tool %s is blocked by policy", toolName)
	}

	if len(p.Allow) > 0 {
		if _, allowed := p.Allow[name]; !allowed {
			return fmt.Errorf("tool %s is not allowed by policy", toolName)
		}
	}

	return nil
}
