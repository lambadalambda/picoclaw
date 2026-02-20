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
| `agents.defaults.max_tokens` | Context window estimate used for compaction heuristics |
| `agents.defaults.max_tool_iterations` | Tool loop cap per turn |
| `agents.defaults.llm_timeout_seconds` | Per-LLM-call timeout |
| `agents.defaults.tool_timeout_seconds` | Per-tool-call timeout |
| `agents.defaults.max_parallel_tool_calls` | Max concurrent tools per iteration |

## Request Payload Budgeting

These optional limits prevent oversized requests to providers:

- `agents.defaults.request_max_messages`
- `agents.defaults.request_max_total_chars`
- `agents.defaults.request_max_message_chars`
- `agents.defaults.request_max_tool_message_chars`

Budgeting is disabled by default. Set one or more values above `0` to enable it.

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
- `providers.modal.api_key`

### Modal GLM-5

This fork supports Modal's OpenAI-compatible GLM-5 endpoint.

Recommended model and provider settings:

```json
{
  "agents": {
    "defaults": {
      "model": "zai-org/GLM-5-FP8"
    }
  },
  "providers": {
    "modal": {
      "api_key": "YOUR_MODAL_API_KEY",
      "api_base": "https://api.us-west-2.modal.direct/v1"
    }
  }
}
```

If `providers.modal.api_base` is omitted, this default is used:

- `https://api.us-west-2.modal.direct/v1`

## Vision Fallback for Image Attachments

When a user sends image attachments and the active runtime cannot pass those images directly to the chat model (for example text-only models like `glm-5`), PicoClaw can auto-run a vision fallback request and inject the result into the user message context.

Config keys:

- `tools.vision.enabled`
- `tools.vision.model` (default: `glm-4.6v`)
- `tools.vision.api_key` (optional; falls back to `providers.zhipu.api_key`)
- `tools.vision.api_base` (optional; falls back to `providers.zhipu.api_base` or Zhipu default)
- `tools.vision.timeout_seconds`
- `tools.vision.max_images`

## Channels

Enable channels under `channels.*` (Telegram, DeltaChat, Discord, DingTalk, etc.).

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

DeltaChat bridge example:

```json
{
  "channels": {
    "deltachat": {
      "enabled": true,
      "bridge_url": "ws://deltachat-bridge:3100",
      "allow_from": ["alice@example.org"]
    }
  }
}
```

If you run PicoClaw without Docker sidecar/profile, use your local bridge address (for example `ws://localhost:3100`).
