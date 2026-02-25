#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

REMOTE="${PICOCLAW_REMOTE:-alice@100.79.8.81}"
LOCAL_BIN="${PICOCLAW_LOCAL_BIN:-build/picoclaw-linux-amd64}"
REMOTE_BIN="${PICOCLAW_REMOTE_BIN:-/home/alice/.local/bin/picoclaw}"
REMOTE_SERVICE="${PICOCLAW_REMOTE_SERVICE:-/home/alice/.config/runit/service/picoclaw-gateway}"
REMOTE_LOG="${PICOCLAW_REMOTE_LOG:-/home/alice/.picoclaw/log/picoclaw-gateway/current}"
REMOTE_BRIDGE_SERVICE="${PICOCLAW_REMOTE_BRIDGE_SERVICE:-/home/alice/.config/runit/service/deltachat-bridge}"
REMOTE_BRIDGE_LOG="${PICOCLAW_REMOTE_BRIDGE_LOG:-/home/alice/.picoclaw/log/deltachat-bridge/current}"
GO_VERSION="${PICOCLAW_GO_VERSION:-1.24}"
LOG_LINES="${PICOCLAW_LOG_LINES:-30}"
BRIDGE_SRC="${PICOCLAW_BRIDGE_SRC:-scripts/deltachat_bridge.py}"
REMOTE_BRIDGE="${PICOCLAW_REMOTE_BRIDGE:-/home/alice/.picoclaw/bin/deltachat_bridge.py}"

SKIP_BUILD=0
SKIP_RESTART=0
SHOW_LOGS=1
COPY_BRIDGE=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-remote.sh [options]

Builds picoclaw for linux/amd64, deploys it to a remote host, restarts the
gateway runit service, and optionally tails recent logs.

Options:
  -r, --remote <user@host>       Remote SSH target
      --local-bin <path>         Local output binary path
      --remote-bin <path>        Remote binary path
      --service <path>           Remote runit service directory path
      --bridge-service <path>    Remote DeltaChat bridge runit service path
      --remote-log <path>        Remote gateway log file path
      --remote-bridge-log <path> Remote DeltaChat bridge log file path
      --go-version <version>     Go version for mise fallback (default: 1.24)
      --log-lines <n>            Log lines to show after deploy (default: 30)
      --copy-bridge              Also deploy scripts/deltachat_bridge.py
      --bridge-src <path>        Local bridge script path
      --remote-bridge <path>     Remote bridge script path
      --skip-build               Reuse existing local binary
      --skip-restart             Deploy without restarting service
      --no-logs                  Skip log tail after deploy
  -h, --help                     Show this help

Environment overrides:
  PICOCLAW_REMOTE, PICOCLAW_LOCAL_BIN, PICOCLAW_REMOTE_BIN,
  PICOCLAW_REMOTE_SERVICE, PICOCLAW_REMOTE_LOG, PICOCLAW_REMOTE_BRIDGE_SERVICE,
  PICOCLAW_REMOTE_BRIDGE_LOG, PICOCLAW_GO_VERSION, PICOCLAW_LOG_LINES,
  PICOCLAW_BRIDGE_SRC, PICOCLAW_REMOTE_BRIDGE
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -r|--remote)
      REMOTE="$2"
      shift 2
      ;;
    --local-bin)
      LOCAL_BIN="$2"
      shift 2
      ;;
    --remote-bin)
      REMOTE_BIN="$2"
      shift 2
      ;;
    --service)
      REMOTE_SERVICE="$2"
      shift 2
      ;;
    --bridge-service)
      REMOTE_BRIDGE_SERVICE="$2"
      shift 2
      ;;
    --remote-log)
      REMOTE_LOG="$2"
      shift 2
      ;;
    --remote-bridge-log)
      REMOTE_BRIDGE_LOG="$2"
      shift 2
      ;;
    --go-version)
      GO_VERSION="$2"
      shift 2
      ;;
    --log-lines)
      LOG_LINES="$2"
      shift 2
      ;;
    --copy-bridge)
      COPY_BRIDGE=1
      shift
      ;;
    --bridge-src)
      BRIDGE_SRC="$2"
      shift 2
      ;;
    --remote-bridge)
      REMOTE_BRIDGE="$2"
      shift 2
      ;;
    --skip-build)
      SKIP_BUILD=1
      shift
      ;;
    --skip-restart)
      SKIP_RESTART=1
      shift
      ;;
    --no-logs)
      SHOW_LOGS=0
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "Unknown argument: $1" >&2
      usage
      exit 1
      ;;
  esac
done

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

need_cmd ssh
need_cmd rsync

if ! [[ "$LOG_LINES" =~ ^[0-9]+$ ]]; then
  echo "--log-lines must be a non-negative integer" >&2
  exit 1
fi

build_binary() {
  mkdir -p "$(dirname "$LOCAL_BIN")"

  if command -v go >/dev/null 2>&1; then
    echo "[deploy] Building with local go"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$LOCAL_BIN" ./cmd/picoclaw
    return
  fi

  if command -v mise >/dev/null 2>&1; then
    echo "[deploy] Building with mise go@${GO_VERSION}"
    GOOS=linux GOARCH=amd64 CGO_ENABLED=0 mise x "go@${GO_VERSION}" -- go build -o "$LOCAL_BIN" ./cmd/picoclaw
    return
  fi

  echo "Neither go nor mise is available to build picoclaw" >&2
  exit 1
}

if [[ "$SKIP_BUILD" -eq 0 ]]; then
  build_binary
else
  echo "[deploy] Skipping build"
fi

if [[ ! -f "$LOCAL_BIN" ]]; then
  echo "Local binary not found: $LOCAL_BIN" >&2
  exit 1
fi

tmp_remote_bin="${REMOTE_BIN}.new.$RANDOM"

echo "[deploy] Uploading binary to $REMOTE:$REMOTE_BIN"
ssh "$REMOTE" "mkdir -p \"$(dirname "$REMOTE_BIN")\""
rsync -az "$LOCAL_BIN" "$REMOTE:$tmp_remote_bin"
ssh "$REMOTE" "install -m 0755 \"$tmp_remote_bin\" \"$REMOTE_BIN\" && rm -f \"$tmp_remote_bin\""

if [[ "$COPY_BRIDGE" -eq 1 ]]; then
  if [[ ! -f "$BRIDGE_SRC" ]]; then
    echo "Bridge source file not found: $BRIDGE_SRC" >&2
    exit 1
  fi
  echo "[deploy] Uploading bridge script to $REMOTE:$REMOTE_BRIDGE"
  ssh "$REMOTE" "mkdir -p \"$(dirname "$REMOTE_BRIDGE")\""
  rsync -az "$BRIDGE_SRC" "$REMOTE:$REMOTE_BRIDGE"
  ssh "$REMOTE" "chmod 0755 \"$REMOTE_BRIDGE\""
fi

if [[ "$SKIP_RESTART" -eq 0 ]]; then
  if [[ "$COPY_BRIDGE" -eq 1 ]]; then
    echo "[deploy] Restarting bridge service $REMOTE_BRIDGE_SERVICE"
    ssh "$REMOTE" "sv restart \"$REMOTE_BRIDGE_SERVICE\" && sleep 1 && sv status \"$REMOTE_BRIDGE_SERVICE\""
  fi

  echo "[deploy] Restarting service $REMOTE_SERVICE"
  ssh "$REMOTE" "sv restart \"$REMOTE_SERVICE\" && sleep 1 && sv status \"$REMOTE_SERVICE\""
else
  echo "[deploy] Skipping service restart"
fi

if [[ "$SHOW_LOGS" -eq 1 && "$LOG_LINES" -gt 0 ]]; then
  if [[ "$COPY_BRIDGE" -eq 1 ]]; then
    echo "[deploy] Last $LOG_LINES lines from $REMOTE_BRIDGE_LOG"
    ssh "$REMOTE" "if [ -f \"$REMOTE_BRIDGE_LOG\" ]; then tail -n $LOG_LINES \"$REMOTE_BRIDGE_LOG\"; else echo \"Log file not found: $REMOTE_BRIDGE_LOG\"; fi"
  fi

  echo "[deploy] Last $LOG_LINES lines from $REMOTE_LOG"
  ssh "$REMOTE" "if [ -f \"$REMOTE_LOG\" ]; then tail -n $LOG_LINES \"$REMOTE_LOG\"; else echo \"Log file not found: $REMOTE_LOG\"; fi"
fi

echo "[deploy] Done"
