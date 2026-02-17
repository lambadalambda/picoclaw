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

Important host paths:

- `picoclaw-home/config.json`
- `picoclaw-home/workspace/sessions/`
- `picoclaw-home/workspace/memory/`
- `picoclaw-home/workspace/skills/`

## First-Run Behavior

`docker-entrypoint.sh` will:

1. create a default `config.json` if missing
2. sync built-in skills into workspace
3. patch config values from `PICOCLAW_*` environment variables

## Running Interactive CLI

```bash
docker compose exec picoclaw picoclaw agent
```
