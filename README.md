# picoclaw (Fork)

> A production-focused fork of [sipeed/picoclaw](https://github.com/sipeed/picoclaw), tuned for reliability, safer tool execution, and Docker-first operation.

## At A Glance

| Area | What You Get |
|---|---|
| Runtime | Lightweight Go AI agent (CLI + chat gateway) |
| Orchestration | Parallel tools, bounded concurrency, per-call timeouts |
| Background Work | Subagents with `spawn/status/list/cancel` controls |
| Reliability | Retry-After aware retries, jittered backoff, panic-safe tool workers |
| Safety | Optional tool policy (`allow`/`deny` + safe mode) |
| Scalability | Payload budgeting before provider calls + configurable retention |

## Fork Scope

This fork keeps upstream architecture and extends it with operational hardening:

- Better provider resiliency (retry policy, Retry-After support, jitter)
- Safer tool execution (no mutable context bleed, panic recovery)
- Subagent task lifecycle controls (status, cancellation, retention)
- Configurable request budgeting to avoid oversized LLM payloads
- Expanded regression coverage and contract/fuzz tests

## Quick Start (Docker)

```bash
git clone <your-fork-url>
cd picoclaw
cp .env.example .env
# edit .env (set model + provider key)
docker compose up -d --build
docker compose logs -f picoclaw
```

Run a quick prompt inside the container:

```bash
docker compose exec picoclaw picoclaw agent -m "hello"
```

## Documentation

| Guide | When To Use |
|---|---|
| [`docs/docker.md`](docs/docker.md) | Full Docker setup, data persistence, lifecycle commands |
| [`docs/configuration.md`](docs/configuration.md) | Config reference and important tuning knobs |
| [`docs/features.md`](docs/features.md) | Capability overview, architecture, subagents |
| [`docs/troubleshooting.md`](docs/troubleshooting.md) | Common errors and fixes |

## Core Project Layout

```text
pkg/channels/   # Telegram/Discord/DingTalk/... adapters
pkg/bus/        # inbound/outbound message broker
pkg/agent/      # agent loop + orchestration
pkg/tools/      # tool implementations + policy + subagents
pkg/providers/  # LLM provider integrations
```

## Local (Non-Docker)

```bash
make deps
make build
./build/picoclaw onboard
./build/picoclaw agent -m "hello"
```

## License

MIT (same as upstream).
