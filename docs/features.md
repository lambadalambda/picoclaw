# Features And Architecture

## What This Fork Focuses On

- Reliability under long-running, tool-heavy sessions
- Clear operational controls for background subagents
- Safer execution boundaries and better default guardrails

## Key Capabilities

| Capability | Notes |
|---|---|
| Parallel tool execution | Bounded concurrency with panic recovery |
| LLM/tool timeouts | Per-call and per-tool context deadlines |
| Subagent controls | `spawn`, `status`, `list`, `cancel` |
| Local notify | Inject messages from local processes via `picoclaw notify` |
| Provider resilience | Exponential retry, Retry-After, jitter |
| Payload budgeting | Truncation/clipping before provider calls |
| Policy guardrails | Optional allow/deny and safe mode |

## Subagents

Subagents are background workers with their own LLM/tool loop.

Supported actions via `spawn` tool:

- `action=spawn` - launch background task
- `action=status` - inspect one task
- `action=list` - show current/recent tasks
- `action=cancel` - stop a running task

Progress events remain internal to the main agent session unless completion requires user response.

## Architecture Overview

```text
Inbound Message
   -> pkg/channels
   -> pkg/bus
   -> pkg/agent (main loop)
      -> pkg/providers (LLM)
      -> pkg/tools (tool calls)
      -> optional subagent loop (pkg/tools/subagent)
   -> pkg/bus outbound
   -> pkg/channels

Local Notify (pkg/notify)
   picoclaw notify "msg"
   -> writes .msg to workspace/inbox/
   -> InboxService polls (1s interval)
   -> resolves target (explicit or last active chat)
   -> rate-limits per source (1/min)
   -> publishes to pkg/bus as inbound message
```
