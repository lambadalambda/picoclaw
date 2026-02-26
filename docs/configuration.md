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
| `agents.defaults.fallback_models` | Optional ordered fallback model list used when the primary model is unavailable/rate-limited |
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

## Model Fallbacks

Set `agents.defaults.fallback_models` to an ordered list of model names.

- PicoClaw tries the primary `agents.defaults.model` first.
- If the call fails with model availability errors (for example: 429/503/model unavailable), it automatically retries with the next fallback model.
- Fallbacks can cross providers (for example Claude -> GLM), as long as credentials for the fallback model's provider are configured.
- If a fallback model is not configured correctly, PicoClaw skips it and continues with remaining fallbacks.

## Prompt Caching

Anthropic cache controls can be set in agent defaults:

- `agents.defaults.anthropic_cache` (boolean)
- `agents.defaults.anthropic_cache_ttl` (`"5m"` or `"1h"`)

Notes:

- Anthropic cache controls are applied on Claude provider calls.
- Z.AI/GLM context caching is automatic (no explicit request toggle required).
- When a provider response includes cache-usage fields, PicoClaw logs them at `INFO` level.

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

## Web Search Backends

`tools.web.search` supports multiple backends for the `web_search` tool:

- `api_key` (Brave key)
- `provider` (`auto`, `brave`, `zai`)
- `zai_api_key`
- `zai_api_base` (default used by tool: `https://api.z.ai/api`)
- `zai_mcp_url` (default: `https://api.z.ai/api/mcp/web_search_prime/mcp`)
- `zai_location` (`us` or `cn`, optional; passed to MCP `location` argument)
- `zai_search_engine` (default: `search-prime`)

Behavior:

- `provider=auto`: prefer Z.AI when any Z.AI-compatible key exists (`zai_api_key`, `providers.zhipu.api_key`, then `providers.modal.api_key`); otherwise use Brave
- `provider=brave`: force Brave backend
- `provider=zai`: force Z.AI backend
- Z.AI backend tries MCP first (`zai_mcp_url`) and then direct API (`/paas/v4/web_search`)
- `provider=auto` with both keys configured: if Z.AI search still fails, fallback to Brave for that request

Runtime tool args:

- `search_type=web` (default) for normal web search
- `search_type=image` for image search (Brave image endpoint; Z.AI image mode is best-effort and falls back to Brave when `provider=auto` + Brave key exists)
- `safe_search=off|moderate|strict` to control content filtering level (provider support is best-effort)

If `zai_api_base` is not set, PicoClaw reuses `providers.zhipu.api_base` when available (including normalizing `/paas/v4` or `/coding/paas/v4` style bases for search).

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
      "allow_from": ["alice@example.org"],
      "ack_reaction": "",
      "done_reaction": "",
      "error_reaction": "",
      "forward_reactions": false
    }
  }
}
```

Optional DeltaChat reaction config:

- `channels.deltachat.ack_reaction`: emoji reaction used as quick "seen/working" ack (empty disables)
- `channels.deltachat.done_reaction`: emoji reaction sent after a reply is delivered (empty disables)
- `channels.deltachat.error_reaction`: emoji reaction sent when a reply fails (empty disables)
- `channels.deltachat.forward_reactions`: forward inbound DeltaChat reactions to the agent as synthetic messages (default: false)

If you run PicoClaw without Docker sidecar/profile, use your local bridge address (for example `ws://localhost:3100`).
