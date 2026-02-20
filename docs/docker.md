# Docker Guide

## Prerequisites

- Docker
- Docker Compose (`docker compose`)

## 1) Clone And Configure

```bash
git clone <your-fork-url>
cd picoclaw
cp .env.example .env
```

Edit `.env` and set at least one model and one provider key.

Example:

```env
PICOCLAW_AGENTS_DEFAULTS_MODEL=glm-5
PICOCLAW_PROVIDERS_ZHIPU_API_KEY=your_key_here
```

## 2) Build And Start

```bash
docker compose up -d --build
```

The container starts with:

```bash
picoclaw gateway
```

## 3) Verify

```bash
docker compose logs -f picoclaw
docker compose exec picoclaw picoclaw agent -m "hello"
```

## Useful Lifecycle Commands

```bash
# restart
docker compose restart picoclaw

# stop and remove container (keeps named volumes)
docker compose down

# rebuild image
docker compose build
```

## Data Persistence

`docker-compose.yml` bind-mounts `./picoclaw-home` to `/root/.picoclaw`.

Runtime software persistence notes:

- `/usr/local` and `/root/.local` are persisted via named volumes.
- Extra APT packages installed at runtime are automatically rehydrated on
  startup (based on persisted apt manual-state + image baseline manifest).
- Rehydration is marker-gated so it runs once per container instance.

Important host paths:

- `picoclaw-home/config.json`
- `picoclaw-home/workspace/sessions/`
- `picoclaw-home/workspace/memory/`
- `picoclaw-home/workspace/skills/`

Optional toggle:

```env
PICOCLAW_RESTORE_APT_PACKAGES=false
# PICOCLAW_APT_RESTORE_MIN_FREE_KB=20480
```

## First-Run Behavior

`docker-entrypoint.sh` will:

1. create a default `config.json` if missing
2. sync built-in skills into workspace
3. patch config values from `PICOCLAW_*` environment variables

## Running Interactive CLI

```bash
docker compose exec picoclaw picoclaw agent
```

## Optional DeltaChat Sidecar

This repo ships an optional `deltachat-bridge` service under the `deltachat` profile.

```bash
docker compose --profile deltachat up -d --build
docker compose logs -f deltachat-bridge
```

Set `channels.deltachat.bridge_url` to `ws://deltachat-bridge:3100` in `picoclaw-home/config.json`.
For onboarding with nine.testrun.org, set `DELTACHAT_SETUP_QR=DCACCOUNT:https://nine.testrun.org/new` in `.env`.
If PicoClaw started before the bridge was ready, run `docker compose restart picoclaw` once.
