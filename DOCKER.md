# Running PicoClaw in Docker

Run picoclaw inside a container so the assistant can freely install software
(apt, pip, npm, etc.) without affecting your host system. Installed packages
and all picoclaw state survive container restarts and recreations.

## Quick start

```bash
# 1. Configure your API keys
cp .env.example .env
#    Edit .env and uncomment/fill in at least one provider

# 2. Build and start the gateway
docker compose up -d

# 3. Verify it's running
docker compose exec picoclaw picoclaw status
```

## Usage

### Gateway mode (default)

`docker compose up -d` starts the gateway daemon on port 18790. Connect
messaging channels (Telegram, Discord, Slack, etc.) by setting the
appropriate variables in `.env` or editing the config directly.

### DeltaChat bridge mode (optional)

Run the optional DeltaChat sidecar profile:

```bash
docker compose --profile deltachat up -d --build
docker compose logs -f deltachat-bridge
```

When using this sidecar, set `channels.deltachat.bridge_url` to
`ws://deltachat-bridge:3100` in `picoclaw-home/config.json`.
If PicoClaw started before the bridge finished booting, run `docker compose restart picoclaw` once.

### Interactive chat

```bash
docker compose exec -it picoclaw picoclaw agent
```

### One-shot message

```bash
docker compose exec picoclaw picoclaw agent -m "install redis-tools and check the version"
```

### View logs

```bash
docker compose logs -f
```

## Configuration

There are two ways to configure picoclaw in Docker:

### Environment variables (recommended for secrets)

Copy `.env.example` to `.env` and set your values. All `config.json` fields
can be overridden with `PICOCLAW_*` environment variables:

```bash
PICOCLAW_PROVIDERS_OPENROUTER_API_KEY=sk-or-v1-...
PICOCLAW_CHANNELS_TELEGRAM_ENABLED=true
PICOCLAW_CHANNELS_TELEGRAM_TOKEN=123456:ABC-...
```

The **model name determines which provider is used**. Prefixed model names
like `anthropic/claude-sonnet-4-20250514` or `openai/gpt-4o` route through
OpenRouter. Direct names like `claude-3.5-sonnet` route to the matching
provider's own API (Anthropic, OpenAI, etc.). The Docker default is
`anthropic/claude-sonnet-4-20250514` via OpenRouter. To change it:

```bash
PICOCLAW_AGENTS_DEFAULTS_MODEL=google/gemini-2.5-pro
```

### Config file

The config lives in `./picoclaw-home/config.json` on your host (bind-mounted
into the container). Edit it directly with any editor:

```bash
$EDITOR picoclaw-home/config.json
docker compose restart
```

A default config is created automatically on first run.

## Persistence

| Location | Path in container | What it keeps |
|---|---|---|
| `./picoclaw-home/` (bind mount) | `/root/.picoclaw` | Config, workspace, skills, sessions |
| `picoclaw-usr-local` (volume) | `/usr/local` | Binaries the assistant installs |
| `picoclaw-var-lib-apt` (volume) | `/var/lib/apt` | APT package state (for restore) |
| `picoclaw-var-cache-apt` (volume) | `/var/cache/apt` | APT package cache |
| `picoclaw-var-lib-dpkg` (volume) | `/var/lib/dpkg` | dpkg database (for restore) |
| `picoclaw-pip` (volume) | `/root/.local` | pip-installed packages |

The picoclaw binary itself lives in `/opt/picoclaw/bin` (outside any volume),
so it always reflects the latest image build.

On startup, the entrypoint compares installed packages against the image's base
APT manual-package manifest and automatically reinstalls extra runtime packages
previously added by the agent.

To avoid repeated work on normal container restarts, this restore runs once per
container instance and writes a marker file in `/opt/picoclaw/.apt-restore.done`.

### Starting fresh

To wipe installed packages and start over:

```bash
docker compose down -v
docker compose up -d --build
```

To also reset picoclaw config/workspace, delete the local directory:

```bash
docker compose down -v
rm -rf picoclaw-home
docker compose up -d --build
```

## Rebuilding

After pulling new code or making changes:

```bash
docker compose up -d --build
```

The binary updates immediately. Volumes (config, workspace, installed
packages) are preserved. Builtin skills are re-synced from the image on
every start.

## What's in the image

The runtime image is based on Debian bookworm and comes with:

- `curl`, `git`, `jq`
- `python3`, `pip`, `venv`
- `build-essential` (gcc, g++, make)
- `sudo`

The assistant can install anything else it needs via `apt-get install` or
`pip install`, and those packages will persist across restarts/recreates.

If you ever want to disable automatic APT rehydration, set:

```bash
PICOCLAW_RESTORE_APT_PACKAGES=false
```

Optional marker path override:

```bash
PICOCLAW_APT_RESTORE_MARKER_FILE=/opt/picoclaw/.apt-restore.done
```

## Architecture

```
Dockerfile            Multi-stage build (Go builder -> Debian runtime)
docker-compose.yml    Service definition + volume mounts
docker-entrypoint.sh  First-run init, skill syncing, config bootstrap
.env.example          Template for API keys / config overrides
.dockerignore         Keeps build context clean
```
