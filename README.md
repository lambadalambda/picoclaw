# picoclaw (Fork)

This repository is a fork of the upstream [sipeed/picoclaw](https://github.com/sipeed/picoclaw) project.

It keeps PicoClaw's lightweight Go architecture and adds reliability and agent-loop improvements focused on real-world operation (timeouts, retries, safer tool execution, subagent controls, and stronger tests).

## What This Project Is

`picoclaw` is a lightweight AI agent runtime in Go.

- It runs as a CLI assistant and as a gateway for chat channels.
- It supports tool-calling (filesystem, shell, web, messaging, scheduling, memory).
- It supports background subagents for long-running tasks.

Core architecture:

- `pkg/channels/` - platform I/O adapters (Telegram, Discord, DingTalk, etc.)
- `pkg/bus/` - inbound/outbound message broker
- `pkg/agent/` - main agent loop and orchestration
- `pkg/tools/` - callable tools
- `pkg/providers/` - LLM provider integrations

## Notable Features In This Fork

- Parallel tool execution with bounded concurrency
- Per-call LLM timeout and per-tool timeout
- Safer tool execution (panic recovery and no cross-chat context mutation)
- Background subagent task control:
  - `spawn` a task
  - query `status`
  - `list` tasks
  - `cancel` running tasks
- Improved provider retry behavior (including `Retry-After` support)
- Optional tool execution policy / safe mode (allow/deny lists)
- Session/memory/cron/channel bug fixes with regression tests

## Run With Docker (Recommended)

This repo includes a Docker setup designed for persistent local use.

### Prerequisites

- Docker
- Docker Compose (`docker compose`)

### 1) Clone

```bash
git clone <your-fork-url>
cd picoclaw
```

### 2) Configure environment (optional but recommended)

```bash
cp .env.example .env
```

Then edit `.env` and set at least:

- one model (optional if you will edit `config.json` directly)
- one provider API key

Example:

```env
PICOCLAW_AGENTS_DEFAULTS_MODEL=glm-5
PICOCLAW_PROVIDERS_ZHIPU_API_KEY=your_key_here
```

### 3) Build and start

```bash
docker compose build
docker compose up -d
```

The container starts with `picoclaw gateway` by default.

### 4) Inspect logs

```bash
docker compose logs -f picoclaw
```

### 5) Use the CLI inside the container

One-shot prompt:

```bash
docker compose exec picoclaw picoclaw agent -m "hello"
```

Interactive mode:

```bash
docker compose exec picoclaw picoclaw agent
```

## Docker Data Layout

The compose file bind-mounts `./picoclaw-home` to `/root/.picoclaw` in the container.

That means your data persists on the host in `picoclaw-home/`:

- `picoclaw-home/config.json`
- `picoclaw-home/workspace/`
- `picoclaw-home/workspace/sessions/`
- `picoclaw-home/workspace/memory/`
- `picoclaw-home/workspace/skills/`

On first run, the entrypoint script will:

- initialize `config.json` if missing
- sync built-in skills
- patch config fields from `PICOCLAW_*` env vars

## Configuration

Primary config file:

- `picoclaw-home/config.json` (inside container: `/root/.picoclaw/config.json`)

Template:

- `config.example.json`

Important notes:

- `agents.defaults.max_tokens` is used as context window sizing for compaction behavior in this fork.
- Request payload budgeting can be tuned with:
  - `agents.defaults.request_max_messages`
  - `agents.defaults.request_max_total_chars`
  - `agents.defaults.request_max_message_chars`
  - `agents.defaults.request_max_tool_message_chars`
  - Set these to `0` to use automatic defaults derived from `max_tokens`.
- Subagent task retention can be tuned with:
  - `agents.defaults.subagent_max_tasks`
  - `agents.defaults.subagent_completed_ttl_seconds`
- Tool policy can be tuned with:
  - `tools.policy.enabled`
  - `tools.policy.safe_mode`
  - `tools.policy.allow`
  - `tools.policy.deny`
- The gateway is not a general REST API; users interact through chat channels or CLI.

## Typical Docker Workflow

- Start/update service:

```bash
docker compose up -d --build
```

- Restart service:

```bash
docker compose restart picoclaw
```

- Stop service:

```bash
docker compose down
```

## Channel Setup (Example: Telegram)

Enable Telegram in `picoclaw-home/config.json`:

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

Then restart:

```bash
docker compose restart picoclaw
```

## Local (Non-Docker) Run

If you prefer local execution:

```bash
make deps
make build
./build/picoclaw onboard
./build/picoclaw agent -m "hello"
```

## Troubleshooting

- `no API key configured for provider`:
  - set provider keys in `.env` or `picoclaw-home/config.json`
- Telegram `Conflict: terminated by other getUpdates`:
  - only run one gateway instance per bot token
- Config changes not applied:
  - restart container after editing config/env

## License

MIT (same as upstream project).
