#!/bin/sh
set -e

PICOCLAW_HOME="${PICOCLAW_HOME:-/root/.picoclaw}"
BUILTIN_SKILLS="/opt/picoclaw/builtin-skills"
CONFIG="$PICOCLAW_HOME/config.json"

# First-run: initialize workspace and copy builtin skills
if [ ! -d "$PICOCLAW_HOME/workspace/skills" ]; then
    echo "[entrypoint] First run detected — initializing workspace..."
    mkdir -p "$PICOCLAW_HOME/workspace/skills"
fi

# Always sync builtin skills (updated on image rebuild)
if [ -d "$BUILTIN_SKILLS" ]; then
    for skill_dir in "$BUILTIN_SKILLS"/*/; do
        if [ -f "$skill_dir/SKILL.md" ]; then
            skill_name=$(basename "$skill_dir")
            cp -r "$skill_dir" "$PICOCLAW_HOME/workspace/skills/"
            echo "[entrypoint] Synced builtin skill: $skill_name"
        fi
    done
fi

# If the user hasn't provided a config yet, create a stub
if [ ! -f "$CONFIG" ]; then
    echo "[entrypoint] No config.json found — creating default config."
    cat > "$CONFIG" <<'EOF'
{
  "agents": {
    "defaults": {
      "workspace": "/root/.picoclaw/workspace",
      "model": "anthropic/claude-sonnet-4-20250514",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20,
      "llm_timeout_seconds": 120,
      "tool_timeout_seconds": 60,
      "max_parallel_tool_calls": 4,
      "request_max_messages": 0,
      "request_max_total_chars": 0,
      "request_max_message_chars": 0,
      "request_max_tool_message_chars": 0,
      "subagent_max_tasks": 200,
      "subagent_completed_ttl_seconds": 86400
    }
  },
  "providers": {},
  "channels": {},
  "gateway": {
    "host": "0.0.0.0",
    "port": 18790
  }
}
EOF
fi

# ---------------------------------------------------------------------------
# Patch config.json from environment variables.
#
# The ProviderConfig struct uses template-style env tags ({{.Name}}) that
# caarlos0/env does not actually resolve, so PICOCLAW_PROVIDERS_*_API_KEY
# env vars are silently ignored by the Go code. We fix that here by
# injecting them into config.json with jq before starting picoclaw.
# ---------------------------------------------------------------------------
patch_config() {
    field="$1"  # jq path, e.g. .providers.openrouter.api_key
    value="$2"  # env var value
    if [ -n "$value" ]; then
        tmp=$(mktemp)
        # Booleans and numbers are passed as raw JSON values; strings are quoted.
        case "$value" in
            true|false) jq "$field = $value" "$CONFIG" > "$tmp" ;;
            *)          jq "$field = \$v" --arg v "$value" "$CONFIG" > "$tmp" ;;
        esac
        mv "$tmp" "$CONFIG"
        # Don't log secrets
        case "$field" in
            *api_key*|*token*|*secret*) echo "[entrypoint] Set $field from environment" ;;
            *)                          echo "[entrypoint] Set $field = $value from environment" ;;
        esac
    fi
}

# Model
patch_config '.agents.defaults.model'       "${PICOCLAW_AGENTS_DEFAULTS_MODEL:-}"

# Providers
patch_config '.providers.openrouter.api_key' "${PICOCLAW_PROVIDERS_OPENROUTER_API_KEY:-}"
patch_config '.providers.openrouter.api_base' "${PICOCLAW_PROVIDERS_OPENROUTER_API_BASE:-}"
patch_config '.providers.anthropic.api_key'  "${PICOCLAW_PROVIDERS_ANTHROPIC_API_KEY:-}"
patch_config '.providers.anthropic.api_base' "${PICOCLAW_PROVIDERS_ANTHROPIC_API_BASE:-}"
patch_config '.providers.openai.api_key'     "${PICOCLAW_PROVIDERS_OPENAI_API_KEY:-}"
patch_config '.providers.openai.api_base'    "${PICOCLAW_PROVIDERS_OPENAI_API_BASE:-}"
patch_config '.providers.groq.api_key'       "${PICOCLAW_PROVIDERS_GROQ_API_KEY:-}"
patch_config '.providers.groq.api_base'      "${PICOCLAW_PROVIDERS_GROQ_API_BASE:-}"
patch_config '.providers.zhipu.api_key'      "${PICOCLAW_PROVIDERS_ZHIPU_API_KEY:-}"
patch_config '.providers.zhipu.api_base'     "${PICOCLAW_PROVIDERS_ZHIPU_API_BASE:-}"
patch_config '.providers.gemini.api_key'     "${PICOCLAW_PROVIDERS_GEMINI_API_KEY:-}"
patch_config '.providers.gemini.api_base'    "${PICOCLAW_PROVIDERS_GEMINI_API_BASE:-}"
patch_config '.providers.vllm.api_key'       "${PICOCLAW_PROVIDERS_VLLM_API_KEY:-}"
patch_config '.providers.vllm.api_base'      "${PICOCLAW_PROVIDERS_VLLM_API_BASE:-}"

# Channels — just the most common fields; edit config.json for the rest
patch_config '.channels.telegram.enabled'    "${PICOCLAW_CHANNELS_TELEGRAM_ENABLED:-}"
patch_config '.channels.telegram.token'      "${PICOCLAW_CHANNELS_TELEGRAM_TOKEN:-}"
patch_config '.channels.discord.enabled'     "${PICOCLAW_CHANNELS_DISCORD_ENABLED:-}"
patch_config '.channels.discord.token'       "${PICOCLAW_CHANNELS_DISCORD_TOKEN:-}"
patch_config '.channels.slack.enabled'       "${PICOCLAW_CHANNELS_SLACK_ENABLED:-}"
patch_config '.channels.slack.bot_token'     "${PICOCLAW_CHANNELS_SLACK_BOT_TOKEN:-}"
patch_config '.channels.slack.app_token'     "${PICOCLAW_CHANNELS_SLACK_APP_TOKEN:-}"

# Tools
patch_config '.tools.web.search.api_key'     "${PICOCLAW_TOOLS_WEB_SEARCH_API_KEY:-}"

echo "[entrypoint] Starting picoclaw $*"
exec picoclaw "$@"
