# Configuration

Primary config file:

- Host: `picoclaw-home/config.json`
- In container: `/root/.picoclaw/config.json`

Base template:

- `config.example.json`

## High-Impact Agent Defaults

| Key | Purpose |
|---|---|
| `agents.defaults.model` | LLM model name |
| `agents.defaults.max_tokens` | Context window estimate used for compaction/budget defaults |
| `agents.defaults.max_tool_iterations` | Tool loop cap per turn |
| `agents.defaults.llm_timeout_seconds` | Per-LLM-call timeout |
| `agents.defaults.tool_timeout_seconds` | Per-tool-call timeout |
| `agents.defaults.max_parallel_tool_calls` | Max concurrent tools per iteration |

## Request Payload Budgeting

These prevent oversized requests to providers:

- `agents.defaults.request_max_messages`
- `agents.defaults.request_max_total_chars`
- `agents.defaults.request_max_message_chars`
- `agents.defaults.request_max_tool_message_chars`

Set to `0` to use auto-derived defaults from `max_tokens`.

## Subagent Retention

- `agents.defaults.subagent_max_tasks`
- `agents.defaults.subagent_completed_ttl_seconds`

This controls memory growth for completed/cancelled/failed subagent tasks.

## Tool Policy / Safe Mode

`tools.policy` supports optional allow/deny control:

```json
{
  "tools": {
    "policy": {
      "enabled": true,
      "safe_mode": true,
      "allow": ["read_file", "list_dir", "web_search"],
      "deny": ["exec", "write_file", "edit_file"]
    }
  }
}
```

Behavior:

- `deny` always blocks matching tools
- if `allow` is non-empty, only allowlisted tools run
- `safe_mode` adds default deny on risky tools (`exec`, `write_file`, `edit_file`)

## Providers

Configure provider keys under `providers.*`.

Common examples:

- `providers.zhipu.api_key`
- `providers.openrouter.api_key`
- `providers.openai.api_key`
- `providers.anthropic.api_key`

## Channels

Enable channels under `channels.*` (Telegram, Discord, DingTalk, etc.).

Telegram example:

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allow_from": ["YOUR_USER_ID"]
    }
  }
}
```
