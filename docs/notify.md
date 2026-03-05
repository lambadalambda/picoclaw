# Local Notify (Inbox Message Injection)

Local processes can queue messages for delivery into the active chat session (or an explicit target) using the `picoclaw notify` command. Messages are picked up by the gateway's inbox service and injected as inbound messages to the agent.

## Quick Start

```bash
# Simple notification
picoclaw notify "Build complete"

# With source label
picoclaw notify --source opencode "PR #42 merged, all tests pass"

# From stdin (useful for piping)
echo "Deploy finished at $(date)" | picoclaw notify --stdin --source deploy

# Explicit target (instead of active chat)
picoclaw notify --source ci --channel telegram --to 1696053078 "CI pipeline failed"
```

## How It Works

```text
Local Process                    Gateway
─────────────                    ───────
picoclaw notify "msg"
  │
  ├─ writes JSON to              workspace/inbox/
  │  <timestamp>-<id>.msg            │
  │                              InboxService polls (1s)
  │                                   │
  │                              reads .msg files (sorted)
  │                                   │
  │                              resolves target:
  │                                ├─ explicit (--channel + --to)
  │                                └─ last active chat (default)
  │                                   │
  │                              rate-limit check (1/min per source)
  │                                   │
  │                              publishes to message bus
  │                                   │
  │                              deletes .msg file
  │                                   │
  │                              agent sees inbound message:
  │                              "[local:opencode] Build complete"
```

## CLI Reference

```
picoclaw notify [options] <message>
```

| Option | Description |
|---|---|
| `--source <name>` | Source label for the notification (default: `local`) |
| `--stdin` | Read message body from stdin instead of arguments |
| `--channel <name>` | Explicit target channel (e.g., `telegram`, `deltachat`) |
| `--to <chat_id>` | Explicit target chat ID (must pair with `--channel`) |

**Rules:**
- `--channel` and `--to` must be provided together, or not at all.
- Without explicit target, the message goes to the most recently active chat.
- `--stdin` and positional message text are mutually exclusive.

## Message Format

Messages are queued as JSON files in `workspace/inbox/`:

```json
{
  "id": "1741182312000000000-12345-1",
  "source": "opencode",
  "content": "Build failed on main branch",
  "channel": "",
  "chat_id": "",
  "created_at": "2026-03-05T10:45:12.000000000Z"
}
```

Files are named `<unix_nano>-<id>.msg` and processed in timestamp order (FIFO).

## Rate Limiting

The inbox service enforces a **1-minute cooldown per source**. If source `opencode` sends a message, subsequent messages from the same source are held until the cooldown expires. Different sources are independent.

This prevents a runaway script from flooding the agent's context window.

## Delivery Behavior

- **Active chat available:** Message is delivered immediately (within 1 second).
- **No active chat:** Message stays queued in `workspace/inbox/` until a chat becomes active.
- **Malformed messages:** Renamed to `.msg.bad` (quarantined) and skipped.
- **Empty content:** Silently removed.

The agent receives the message as an inbound chat message prefixed with `[local:<source>]`, so it appears in conversation context like:

> [local:opencode] Build failed on main branch

The agent can then decide how to respond — relay to the user, take action, or ignore.

## Use Cases

### CI/CD Notifications
```bash
# In your build script or GitHub Actions
picoclaw notify --source ci "Build #${BUILD_NUMBER} passed ✓"
```

### OpenCode Integration
```bash
# After a coding session completes
opencode run "fix the tests" && \
  picoclaw notify --source opencode "Tests fixed and committed"
```

### Cron/Script Results
```bash
# Pipe script output
./check-something.sh 2>&1 | picoclaw notify --stdin --source monitor
```

### Cross-Channel Alerts
```bash
# Always send to a specific Telegram chat, regardless of active session
picoclaw notify --source alert --channel telegram --to 1696053078 "Server down!"
```

## Metadata

Delivered messages include metadata that the agent can inspect:

| Key | Value |
|---|---|
| `local_notify` | `"1"` (always) |
| `local_source` | Source label |
| `local_message_id` | Unique message ID |
| `local_created_at` | Original enqueue timestamp (RFC3339) |
