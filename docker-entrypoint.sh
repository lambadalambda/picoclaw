#!/bin/sh
set -e

PICOCLAW_HOME="${PICOCLAW_HOME:-/root/.picoclaw}"
BUILTIN_SKILLS="/opt/picoclaw/builtin-skills"
CONFIG="$PICOCLAW_HOME/config.json"
BASE_APT_PACKAGES_FILE="${PICOCLAW_BASE_APT_PACKAGES_FILE:-/opt/picoclaw/base-apt-packages.txt}"
BASE_APT_MANUAL_PACKAGES_FILE="${PICOCLAW_BASE_APT_MANUAL_PACKAGES_FILE:-/opt/picoclaw/base-apt-manual-packages.txt}"
RESTORE_APT_PACKAGES="${PICOCLAW_RESTORE_APT_PACKAGES:-true}"
APT_RESTORE_MARKER_FILE="${PICOCLAW_APT_RESTORE_MARKER_FILE:-/opt/picoclaw/.apt-restore.done}"
APT_RESTORE_MIN_FREE_KB="${PICOCLAW_APT_RESTORE_MIN_FREE_KB:-20480}"

cleanup_apt_cache() {
    apt-get clean >/dev/null 2>&1 || true
    rm -rf /var/cache/apt/archives/*.deb /var/cache/apt/archives/partial/* 2>/dev/null || true
}

available_kb() {
    target="$1"
    output=$(df -Pk "$target" 2>/dev/null | tail -n 1 || true)
    if [ -z "$output" ]; then
        echo 0
        return
    fi

    set -- $output
    echo "${4:-0}"
}

restore_runtime_apt_packages() {
    case "$RESTORE_APT_PACKAGES" in
        0|false|FALSE|no|NO)
            echo "[entrypoint] Runtime APT package restore disabled"
            return
            ;;
    esac

    if [ ! -f "$BASE_APT_PACKAGES_FILE" ]; then
        echo "[entrypoint] Base APT package manifest not found, skipping restore"
        return
    fi

    if [ -f "$APT_RESTORE_MARKER_FILE" ]; then
        return
    fi

    cleanup_apt_cache

    min_free_kb="$APT_RESTORE_MIN_FREE_KB"
    case "$min_free_kb" in
        ''|*[!0-9]*) min_free_kb=20480 ;;
    esac

    free_kb=$(available_kb /var/cache/apt)
    case "$free_kb" in
        ''|*[!0-9]*) free_kb=0 ;;
    esac

    if [ "$free_kb" -lt "$min_free_kb" ]; then
        echo "[entrypoint] WARNING: Skipping runtime APT package restore (free ${free_kb}KB < required ${min_free_kb}KB). Free Docker disk space and restart to retry."
        return
    fi

    current_manual_packages_file=$(mktemp)
    base_manual_packages_file=$(mktemp)
    extra_packages_file=$(mktemp)

    if [ -f "$BASE_APT_MANUAL_PACKAGES_FILE" ] && apt-mark showmanual 2>/dev/null | sort -u > "$current_manual_packages_file"; then
        sort -u "$BASE_APT_MANUAL_PACKAGES_FILE" > "$base_manual_packages_file"
        comm -13 "$base_manual_packages_file" "$current_manual_packages_file" > "$extra_packages_file"
    else
        current_packages_file=$(mktemp)
        base_packages_file=$(mktemp)
        if ! dpkg-query -W -f='${binary:Package}\n' 2>/dev/null | sort -u > "$current_packages_file"; then
            echo "[entrypoint] Failed to read installed package list, skipping restore"
            rm -f "$current_manual_packages_file" "$base_manual_packages_file" "$extra_packages_file" "$current_packages_file" "$base_packages_file"
            return
        fi
        sort -u "$BASE_APT_PACKAGES_FILE" > "$base_packages_file"
        comm -13 "$base_packages_file" "$current_packages_file" > "$extra_packages_file"
        rm -f "$current_packages_file" "$base_packages_file"
    fi

    repair_dpkg_statoverrides

    extra_count=$(grep -c . "$extra_packages_file" || true)
    if [ "$extra_count" -eq 0 ]; then
        marker_dir=$(dirname "$APT_RESTORE_MARKER_FILE")
        mkdir -p "$marker_dir" && touch "$APT_RESTORE_MARKER_FILE"
        rm -f "$current_manual_packages_file" "$base_manual_packages_file" "$extra_packages_file"
        return
    fi

    echo "[entrypoint] Restoring $extra_count runtime APT package(s)"
    if apt-get update >/dev/null 2>&1 && xargs -r apt-get install -y --no-install-recommends --reinstall < "$extra_packages_file"; then
        cleanup_apt_cache
        marker_dir=$(dirname "$APT_RESTORE_MARKER_FILE")
        mkdir -p "$marker_dir" && touch "$APT_RESTORE_MARKER_FILE"
        echo "[entrypoint] Runtime APT package restore complete"
    else
        cleanup_apt_cache
        echo "[entrypoint] WARNING: Runtime APT package restore failed; continuing startup"
    fi

    rm -f "$current_manual_packages_file" "$base_manual_packages_file" "$extra_packages_file"
}

repair_dpkg_statoverrides() {
    statoverride_file=$(mktemp)
    if ! dpkg-statoverride --list > "$statoverride_file" 2>/dev/null; then
        rm -f "$statoverride_file"
        return
    fi

    removed_count=0
    while IFS= read -r line; do
        [ -z "$line" ] && continue
        set -- $line
        owner="$1"
        group="$2"
        shift 3
        path="$*"

        owner_missing=0
        group_missing=0

        case "$owner" in
            ''|*[!0-9]*)
                if ! getent passwd "$owner" >/dev/null 2>&1; then
                    owner_missing=1
                fi
                ;;
        esac

        case "$group" in
            ''|*[!0-9]*)
                if ! getent group "$group" >/dev/null 2>&1; then
                    group_missing=1
                fi
                ;;
        esac

        if [ "$owner_missing" -eq 1 ] || [ "$group_missing" -eq 1 ]; then
            if dpkg-statoverride --remove "$path" >/dev/null 2>&1; then
                removed_count=$((removed_count + 1))
            fi
        fi
    done < "$statoverride_file"

    rm -f "$statoverride_file"

    if [ "$removed_count" -gt 0 ]; then
        echo "[entrypoint] Removed $removed_count invalid dpkg-statoverride entr$( [ "$removed_count" -eq 1 ] && echo y || echo ies )"
    fi
}

restore_runtime_apt_packages

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
patch_config '.providers.modal.api_key'      "${PICOCLAW_PROVIDERS_MODAL_API_KEY:-}"
patch_config '.providers.modal.api_base'     "${PICOCLAW_PROVIDERS_MODAL_API_BASE:-}"
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
patch_config '.tools.web.search.max_results' "${PICOCLAW_TOOLS_WEB_SEARCH_MAX_RESULTS:-}"
patch_config '.tools.web.search.provider'    "${PICOCLAW_TOOLS_WEB_SEARCH_PROVIDER:-}"
patch_config '.tools.web.search.zai_api_key' "${PICOCLAW_TOOLS_WEB_SEARCH_ZAI_API_KEY:-}"
patch_config '.tools.web.search.zai_api_base' "${PICOCLAW_TOOLS_WEB_SEARCH_ZAI_API_BASE:-}"
patch_config '.tools.web.search.zai_mcp_url' "${PICOCLAW_TOOLS_WEB_SEARCH_ZAI_MCP_URL:-}"
patch_config '.tools.web.search.zai_location' "${PICOCLAW_TOOLS_WEB_SEARCH_ZAI_LOCATION:-}"
patch_config '.tools.web.search.zai_search_engine' "${PICOCLAW_TOOLS_WEB_SEARCH_ZAI_SEARCH_ENGINE:-}"

# ---------------------------------------------------------------------------
# Optional: runit service supervisor for workspace-managed services.
#
# This enables long-running daemons to start automatically with the container
# and be managed by writing files under the workspace.
#
# Service directory layout:
#   $PICOCLAW_HOME/workspace/services/<name>/run
#   $PICOCLAW_HOME/workspace/services/<name>/log/run   (optional)
#   $PICOCLAW_HOME/workspace/services/<name>/down      (optional; disables autostart)
#
# NOTE: run scripts must be executable. The agent can create them via write_file
# and then run: chmod +x ... using the exec tool.
# ---------------------------------------------------------------------------
start_services_supervisor() {
    enabled="${PICOCLAW_SERVICES_ENABLED:-true}"
    case "$enabled" in
        0|false|FALSE|no|NO)
            echo "[entrypoint] Services supervisor disabled"
            return
            ;;
    esac

    if ! command -v runsvdir >/dev/null 2>&1; then
        echo "[entrypoint] runsvdir not found; skipping services supervisor"
        return
    fi

    services_dir="${PICOCLAW_SERVICES_DIR:-$PICOCLAW_HOME/workspace/services}"
    if [ -z "$services_dir" ]; then
        services_dir="$PICOCLAW_HOME/workspace/services"
    fi
    mkdir -p "$services_dir"

    # sv(8) uses /etc/service by default; set $SVDIR so `sv status <name>` works
    # for workspace-managed services.
    export SVDIR="$services_dir"

    supervisor_log_dir="$PICOCLAW_HOME/workspace/memory"
    mkdir -p "$supervisor_log_dir"
    supervisor_log="${PICOCLAW_SERVICES_LOG:-$supervisor_log_dir/services-supervisor.log}"
    if [ -z "$supervisor_log" ]; then
        supervisor_log="$supervisor_log_dir/services-supervisor.log"
    fi

    echo "[entrypoint] Starting services supervisor (runit): $services_dir"
    # Best-effort; if it fails for any reason, don't prevent picoclaw from starting.
    (runsvdir -P "$services_dir" >>"$supervisor_log" 2>&1 &) || true
}

start_services_supervisor

echo "[entrypoint] Starting picoclaw $*"
exec picoclaw "$@"
