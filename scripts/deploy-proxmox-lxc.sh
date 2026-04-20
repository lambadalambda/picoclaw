#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

PROXMOX_REMOTE="${PICOCLAW_PROXMOX_REMOTE:-root@nas}"
CTID="${PICOCLAW_LXC_ID:-111}"
LXC_HOSTNAME="${PICOCLAW_LXC_HOSTNAME:-picoclaw}"

TEMPLATE_STORAGE="${PICOCLAW_TEMPLATE_STORAGE:-local}"
TEMPLATE_NAME="${PICOCLAW_TEMPLATE_NAME:-ubuntu-24.04-standard_24.04-2_amd64.tar.zst}"
LXC_STORAGE="${PICOCLAW_LXC_STORAGE:-local-lvm}"
LXC_DISK_SIZE="${PICOCLAW_LXC_DISK_SIZE:-16}"
LXC_CORES="${PICOCLAW_LXC_CORES:-2}"
LXC_MEMORY="${PICOCLAW_LXC_MEMORY:-4096}"
LXC_SWAP="${PICOCLAW_LXC_SWAP:-1024}"
LXC_NET0="${PICOCLAW_LXC_NET0:-name=eth0,bridge=vmbr0,ip=dhcp,type=veth}"
LXC_NAMESERVER="${PICOCLAW_LXC_NAMESERVER:-1.1.1.1}"

LOCAL_BIN="${PICOCLAW_LOCAL_BIN:-build/picoclaw-linux-amd64}"
REMOTE_BIN="${PICOCLAW_REMOTE_BIN:-/usr/local/bin/picoclaw}"
GO_VERSION="${PICOCLAW_GO_VERSION:-1.24}"
LOG_LINES="${PICOCLAW_LOG_LINES:-30}"

LOCAL_HOME="${PICOCLAW_LOCAL_HOME:-picoclaw-home}"
SYNC_HOME=0
NO_SYNC_HOME=0

BRIDGE_SRC="${PICOCLAW_BRIDGE_SRC:-scripts/deltachat_bridge.py}"
BRIDGE_DEST="${PICOCLAW_BRIDGE_DEST:-/home/picoclaw/.picoclaw/bin/deltachat_bridge.py}"
BRIDGE_PORT="${PICOCLAW_BRIDGE_PORT:-3001}"

GATEWAY_SERVICE="${PICOCLAW_GATEWAY_SERVICE:-picoclaw-gateway}"
BRIDGE_SERVICE="${PICOCLAW_BRIDGE_SERVICE:-deltachat-bridge}"
WORKSPACE_SERVICES_SERVICE="${PICOCLAW_WORKSPACE_SERVICES_SERVICE:-workspace-services}"
WORKSPACE_SERVICES_DIR="${PICOCLAW_WORKSPACE_SERVICES_DIR:-/home/picoclaw/.picoclaw/workspace/services}"
GATEWAY_LOG="${PICOCLAW_GATEWAY_LOG:-/home/picoclaw/.picoclaw/log/picoclaw-gateway/current}"
BRIDGE_LOG="${PICOCLAW_BRIDGE_LOG:-/home/picoclaw/.picoclaw/log/deltachat-bridge/current}"
WORKSPACE_SERVICES_LOG="${PICOCLAW_WORKSPACE_SERVICES_LOG:-/home/picoclaw/.picoclaw/log/workspace-services/current}"

RUNIT_SKILL_SRC="${PICOCLAW_RUNIT_SKILL_SRC:-skills/runit/SKILL.md}"
RUNIT_SKILL_DEST="${PICOCLAW_RUNIT_SKILL_DEST:-/home/picoclaw/.picoclaw/workspace/skills/runit/SKILL.md}"

BOOTSTRAP=0
SKIP_BUILD=0
SKIP_RESTART=0
SHOW_LOGS=1
COPY_BRIDGE=0
NO_COPY_BRIDGE=0
ENABLE_BRIDGE=0

usage() {
  cat <<'EOF'
Usage: scripts/deploy-proxmox-lxc.sh [options]

Deploys picoclaw to a Proxmox LXC via pct push.
It can also create and bootstrap a new Ubuntu 24.04 container.

Options:
  -r, --remote <user@host>       Proxmox SSH target (default: root@nas)
      --ctid <id>                LXC numeric ID (default: 111)
      --hostname <name>          LXC hostname for bootstrap (default: picoclaw)
      --bootstrap                Create/provision the LXC if needed
      --template <name>          LXC template name for bootstrap
      --template-storage <name>  Storage for LXC templates (default: local)
      --lxc-storage <name>       Storage for LXC rootfs (default: local-lvm)
      --disk-size <gb>           Root disk size in GB-ish units (default: 16)
      --cores <n>                LXC CPU cores (default: 2)
      --memory <mb>              LXC memory in MiB (default: 4096)
      --swap <mb>                LXC swap in MiB (default: 1024)
      --net0 <config>            Proxmox net0 string (default: DHCP on vmbr0)
      --nameserver <ip>          LXC nameserver (default: 1.1.1.1)
      --local-bin <path>         Local output binary path
      --remote-bin <path>        Binary path inside LXC
      --go-version <version>     Go version for mise fallback (default: 1.24)
      --sync-home                Upload and merge local picoclaw-home into /home/picoclaw/.picoclaw
      --no-sync-home             Disable home sync (bootstrap defaults to sync)
      --local-home <path>        Local picoclaw-home path
      --copy-bridge              Upload scripts/deltachat_bridge.py into LXC
      --no-copy-bridge           Disable bridge upload (bootstrap defaults to upload)
      --enable-bridge            Enable and restart bridge runit service
      --bridge-src <path>        Local bridge script path
      --bridge-dest <path>       Bridge script destination inside LXC
      --bridge-port <port>       Bridge websocket port env (default: 3001)
      --skip-build               Reuse existing local binary
      --skip-restart             Deploy without service restart
      --no-logs                  Skip log tail after deploy
      --log-lines <n>            Log lines to show (default: 30)
  -h, --help                     Show this help

Environment overrides:
  PICOCLAW_PROXMOX_REMOTE, PICOCLAW_LXC_ID, PICOCLAW_LXC_HOSTNAME,
  PICOCLAW_TEMPLATE_STORAGE, PICOCLAW_TEMPLATE_NAME, PICOCLAW_LXC_STORAGE,
  PICOCLAW_LXC_DISK_SIZE, PICOCLAW_LXC_CORES, PICOCLAW_LXC_MEMORY,
  PICOCLAW_LXC_SWAP, PICOCLAW_LXC_NET0, PICOCLAW_LXC_NAMESERVER,
  PICOCLAW_LOCAL_BIN, PICOCLAW_REMOTE_BIN, PICOCLAW_GO_VERSION,
  PICOCLAW_LOCAL_HOME, PICOCLAW_BRIDGE_SRC, PICOCLAW_BRIDGE_DEST,
  PICOCLAW_BRIDGE_PORT, PICOCLAW_LOG_LINES, PICOCLAW_GATEWAY_SERVICE,
  PICOCLAW_BRIDGE_SERVICE, PICOCLAW_WORKSPACE_SERVICES_SERVICE,
  PICOCLAW_WORKSPACE_SERVICES_DIR, PICOCLAW_GATEWAY_LOG,
  PICOCLAW_BRIDGE_LOG, PICOCLAW_WORKSPACE_SERVICES_LOG,
  PICOCLAW_RUNIT_SKILL_SRC, PICOCLAW_RUNIT_SKILL_DEST
EOF
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    -r|--remote)
      PROXMOX_REMOTE="$2"
      shift 2
      ;;
    --ctid)
      CTID="$2"
      shift 2
      ;;
    --hostname)
      LXC_HOSTNAME="$2"
      shift 2
      ;;
    --bootstrap)
      BOOTSTRAP=1
      shift
      ;;
    --template)
      TEMPLATE_NAME="$2"
      shift 2
      ;;
    --template-storage)
      TEMPLATE_STORAGE="$2"
      shift 2
      ;;
    --lxc-storage)
      LXC_STORAGE="$2"
      shift 2
      ;;
    --disk-size)
      LXC_DISK_SIZE="$2"
      shift 2
      ;;
    --cores)
      LXC_CORES="$2"
      shift 2
      ;;
    --memory)
      LXC_MEMORY="$2"
      shift 2
      ;;
    --swap)
      LXC_SWAP="$2"
      shift 2
      ;;
    --net0)
      LXC_NET0="$2"
      shift 2
      ;;
    --nameserver)
      LXC_NAMESERVER="$2"
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
    --go-version)
      GO_VERSION="$2"
      shift 2
      ;;
    --sync-home)
      SYNC_HOME=1
      shift
      ;;
    --no-sync-home)
      SYNC_HOME=0
      NO_SYNC_HOME=1
      shift
      ;;
    --local-home)
      LOCAL_HOME="$2"
      shift 2
      ;;
    --copy-bridge)
      COPY_BRIDGE=1
      shift
      ;;
    --no-copy-bridge)
      COPY_BRIDGE=0
      NO_COPY_BRIDGE=1
      shift
      ;;
    --enable-bridge)
      ENABLE_BRIDGE=1
      COPY_BRIDGE=1
      shift
      ;;
    --bridge-src)
      BRIDGE_SRC="$2"
      shift 2
      ;;
    --bridge-dest)
      BRIDGE_DEST="$2"
      shift 2
      ;;
    --bridge-port)
      BRIDGE_PORT="$2"
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
    --log-lines)
      LOG_LINES="$2"
      shift 2
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

if [[ "$BOOTSTRAP" -eq 1 && "$NO_SYNC_HOME" -eq 0 ]]; then
  SYNC_HOME=1
fi

if [[ "$BOOTSTRAP" -eq 1 && "$NO_COPY_BRIDGE" -eq 0 ]]; then
  COPY_BRIDGE=1
fi

need_cmd() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "Missing required command: $1" >&2
    exit 1
  fi
}

require_int() {
  local value="$1"
  local flag_name="$2"
  if ! [[ "$value" =~ ^[0-9]+$ ]]; then
    echo "$flag_name must be a non-negative integer" >&2
    exit 1
  fi
}

remote_sh() {
  ssh "$PROXMOX_REMOTE" "$1"
}

container_exists() {
  remote_sh "pct config $CTID >/dev/null 2>&1"
}

container_running() {
  remote_sh "pct status $CTID 2>/dev/null | grep -q 'status: running'"
}

ensure_container_running() {
  if ! container_exists; then
    echo "Container $CTID does not exist on $PROXMOX_REMOTE" >&2
    echo "Run with --bootstrap to create it." >&2
    exit 1
  fi

  if ! container_running; then
    echo "[deploy] Starting LXC $CTID"
    remote_sh "pct start $CTID"
    sleep 2
  fi
}

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

upload_file_to_lxc() {
  local src="$1"
  local dst="$2"
  local owner="$3"
  local group="$4"
  local perms="$5"

  if [[ ! -f "$src" ]]; then
    echo "Local file not found: $src" >&2
    exit 1
  fi

  local remote_tmp
  remote_tmp="/tmp/picoclaw.$RANDOM.$(basename "$src")"

  local lxc_stage
  lxc_stage="/tmp/picoclaw-stage.$RANDOM.$(basename "$dst")"

  rsync -az "$src" "$PROXMOX_REMOTE:$remote_tmp"
  remote_sh "pct exec $CTID -- mkdir -p \"$(dirname "$dst")\""
  remote_sh "pct push $CTID \"$remote_tmp\" \"$lxc_stage\" --user root --group root --perms 0600 && rm -f \"$remote_tmp\""
  remote_sh "pct exec $CTID -- bash -lc 'install -m \"$perms\" -o \"$owner\" -g \"$group\" \"$lxc_stage\" \"$dst\" && rm -f \"$lxc_stage\"'"
}

ensure_template_exists() {
  if remote_sh "pveam list \"$TEMPLATE_STORAGE\" | grep -Fq \"$TEMPLATE_NAME\""; then
    return
  fi

  echo "[bootstrap] Downloading template $TEMPLATE_NAME to $TEMPLATE_STORAGE"
  remote_sh "pveam download \"$TEMPLATE_STORAGE\" \"$TEMPLATE_NAME\""
}

create_container() {
  echo "[bootstrap] Creating LXC $CTID ($LXC_HOSTNAME)"
  remote_sh "pct create $CTID \"${TEMPLATE_STORAGE}:vztmpl/${TEMPLATE_NAME}\" --hostname \"$LXC_HOSTNAME\" --cores \"$LXC_CORES\" --memory \"$LXC_MEMORY\" --swap \"$LXC_SWAP\" --rootfs \"${LXC_STORAGE}:${LXC_DISK_SIZE}\" --net0 \"$LXC_NET0\" --onboot 1 --unprivileged 1 --features keyctl=1,nesting=1 --nameserver \"$LXC_NAMESERVER\""
}

ensure_runtime_dirs() {
  remote_sh "pct exec $CTID -- bash -lc 'if ! id -u picoclaw >/dev/null 2>&1; then useradd --create-home --shell /bin/bash picoclaw; fi; install -d -o picoclaw -g picoclaw /home/picoclaw/.local /home/picoclaw/.local/bin /home/picoclaw/.picoclaw /home/picoclaw/.picoclaw/bin /home/picoclaw/.picoclaw/log /home/picoclaw/.picoclaw/log/picoclaw-gateway /home/picoclaw/.picoclaw/log/deltachat-bridge /home/picoclaw/.picoclaw/log/workspace-services /home/picoclaw/.picoclaw/deltachat-accounts /home/picoclaw/.picoclaw/workspace /home/picoclaw/.picoclaw/workspace/memory /home/picoclaw/.picoclaw/workspace/services /home/picoclaw/.picoclaw/workspace/skills /home/picoclaw/.picoclaw/workspace/skills/runit /home/picoclaw/.config /home/picoclaw/.config/runit /home/picoclaw/.config/runit/service'"
}

install_runit_services() {
  local temp_dir
  temp_dir="$(mktemp -d)"
  trap 'rm -rf "$temp_dir"' RETURN

  cat >"$temp_dir/gateway-run" <<'EOF'
#!/bin/sh
set -eu

exec chpst -u picoclaw:picoclaw \
  env HOME=/home/picoclaw USER=picoclaw PATH=/home/picoclaw/.local/bin:/usr/local/bin:/usr/bin:/bin \
  /usr/local/bin/picoclaw gateway 2>&1
EOF

  cat >"$temp_dir/gateway-log-run" <<'EOF'
#!/bin/sh
set -eu

exec chpst -u picoclaw:picoclaw svlogd -tt /home/picoclaw/.picoclaw/log/picoclaw-gateway
EOF

  cat >"$temp_dir/bridge-run" <<EOF
#!/bin/sh
set -eu

if [ ! -x /home/picoclaw/.local/deltachat-bridge-venv/bin/python ]; then
  chpst -u picoclaw:picoclaw \
    env HOME=/home/picoclaw USER=picoclaw PATH=/home/picoclaw/.local/bin:/usr/local/bin:/usr/bin:/bin \
    sh -lc 'python3 -m venv /home/picoclaw/.local/deltachat-bridge-venv && /home/picoclaw/.local/deltachat-bridge-venv/bin/pip install --no-cache-dir deltachat-rpc-server==2.43.0 deltachat-rpc-client==2.43.0 websockets==16.0'
fi

exec chpst -u picoclaw:picoclaw \
  env HOME=/home/picoclaw USER=picoclaw PATH=/home/picoclaw/.local/bin:/usr/local/bin:/usr/bin:/bin \
  DELTACHAT_BRIDGE_HOST=127.0.0.1 \
  DELTACHAT_BRIDGE_PORT=${BRIDGE_PORT} \
  DELTACHAT_ACCOUNTS_DIR=/home/picoclaw/.picoclaw/deltachat-accounts \
  DELTACHAT_RPC_SERVER_PATH=/home/picoclaw/.local/deltachat-bridge-venv/bin/deltachat-rpc-server \
  DELTACHAT_BRIDGE_READY_FILE=/tmp/deltachat-bridge.ready \
  /home/picoclaw/.local/deltachat-bridge-venv/bin/python /home/picoclaw/.picoclaw/bin/deltachat_bridge.py 2>&1
EOF

  cat >"$temp_dir/bridge-log-run" <<'EOF'
#!/bin/sh
set -eu

exec chpst -u picoclaw:picoclaw svlogd -tt /home/picoclaw/.picoclaw/log/deltachat-bridge
EOF

  remote_sh "pct exec $CTID -- bash -lc 'install -d /etc/sv/${GATEWAY_SERVICE}/log /etc/sv/${BRIDGE_SERVICE}/log /etc/service'"

  upload_file_to_lxc "$temp_dir/gateway-run" "/etc/sv/${GATEWAY_SERVICE}/run" root root 0755
  upload_file_to_lxc "$temp_dir/gateway-log-run" "/etc/sv/${GATEWAY_SERVICE}/log/run" root root 0755
  upload_file_to_lxc "$temp_dir/bridge-run" "/etc/sv/${BRIDGE_SERVICE}/run" root root 0755
  upload_file_to_lxc "$temp_dir/bridge-log-run" "/etc/sv/${BRIDGE_SERVICE}/log/run" root root 0755

  remote_sh "pct exec $CTID -- bash -lc 'ln -sfn /etc/sv/${GATEWAY_SERVICE} /etc/service/${GATEWAY_SERVICE}; ln -sfn /etc/sv/${BRIDGE_SERVICE} /etc/service/${BRIDGE_SERVICE}; ln -sfn /etc/sv/${GATEWAY_SERVICE} /home/picoclaw/.config/runit/service/${GATEWAY_SERVICE}; ln -sfn /etc/sv/${BRIDGE_SERVICE} /home/picoclaw/.config/runit/service/${BRIDGE_SERVICE}'"

  remote_sh "pct exec $CTID -- systemctl enable --now runit"

  if [[ "$ENABLE_BRIDGE" -eq 1 ]]; then
    remote_sh "pct exec $CTID -- rm -f /etc/sv/${BRIDGE_SERVICE}/down"
  else
    remote_sh "pct exec $CTID -- touch /etc/sv/${BRIDGE_SERVICE}/down"
  fi

  trap - RETURN
  rm -rf "$temp_dir"
}

install_workspace_services_runit() {
  local temp_dir
  temp_dir="$(mktemp -d)"
  trap 'rm -rf "$temp_dir"' RETURN

  cat >"$temp_dir/workspace-services-run" <<EOF
#!/bin/sh
set -eu

SVC_DIR="${WORKSPACE_SERVICES_DIR}"
mkdir -p "\$SVC_DIR" /home/picoclaw/.picoclaw/workspace/memory

exec chpst -u picoclaw:picoclaw \
  env HOME=/home/picoclaw USER=picoclaw PATH=/home/picoclaw/.local/bin:/usr/local/bin:/usr/bin:/bin SVDIR="\$SVC_DIR" \
  runsvdir -P "\$SVC_DIR" 2>&1
EOF

  cat >"$temp_dir/workspace-services-log-run" <<'EOF'
#!/bin/sh
set -eu

exec chpst -u picoclaw:picoclaw svlogd -tt /home/picoclaw/.picoclaw/log/workspace-services
EOF

  remote_sh "pct exec $CTID -- bash -lc 'install -d /etc/sv/${WORKSPACE_SERVICES_SERVICE}/log /etc/service'"
  upload_file_to_lxc "$temp_dir/workspace-services-run" "/etc/sv/${WORKSPACE_SERVICES_SERVICE}/run" root root 0755
  upload_file_to_lxc "$temp_dir/workspace-services-log-run" "/etc/sv/${WORKSPACE_SERVICES_SERVICE}/log/run" root root 0755
  remote_sh "pct exec $CTID -- bash -lc 'ln -sfn /etc/sv/${WORKSPACE_SERVICES_SERVICE} /etc/service/${WORKSPACE_SERVICES_SERVICE}; ln -sfn /etc/sv/${WORKSPACE_SERVICES_SERVICE} /home/picoclaw/.config/runit/service/${WORKSPACE_SERVICES_SERVICE}'"
  remote_sh "pct exec $CTID -- systemctl enable --now runit"
  remote_sh "pct exec $CTID -- bash -lc 'sv restart /etc/service/${WORKSPACE_SERVICES_SERVICE} >/dev/null 2>&1 || true; sv up /etc/service/${WORKSPACE_SERVICES_SERVICE} >/dev/null 2>&1 || true; for _ in 1 2 3 4 5; do status=\$(sv status /etc/service/${WORKSPACE_SERVICES_SERVICE} 2>/dev/null || true); case \"\$status\" in run:*) echo \"\$status\"; exit 0 ;; esac; sleep 1; done; sv status /etc/service/${WORKSPACE_SERVICES_SERVICE}'"

  trap - RETURN
  rm -rf "$temp_dir"
}

sync_workspace_runit_skill() {
  if [[ ! -f "$RUNIT_SKILL_SRC" ]]; then
    echo "Runit skill source file not found: $RUNIT_SKILL_SRC" >&2
    exit 1
  fi

  echo "[deploy] Syncing runit skill into workspace"
  upload_file_to_lxc "$RUNIT_SKILL_SRC" "$RUNIT_SKILL_DEST" picoclaw picoclaw 0644
}

bootstrap_container() {
  if container_exists; then
    echo "[bootstrap] LXC $CTID already exists, skipping create"
  else
    ensure_template_exists
    create_container
  fi

  remote_sh "pct set $CTID --nameserver \"$LXC_NAMESERVER\""
  ensure_container_running

  echo "[bootstrap] Installing runtime packages"
  remote_sh "pct exec $CTID -- bash -lc 'apt-get update && DEBIAN_FRONTEND=noninteractive apt-get install -y runit rsync ca-certificates curl python3 python3-venv python3-pip jq'"

  ensure_runtime_dirs
  install_runit_services
}

sync_home_dir() {
  if [[ ! -d "$LOCAL_HOME" ]]; then
    echo "Local picoclaw-home directory not found: $LOCAL_HOME" >&2
    exit 1
  fi

  local tmp_tar
  tmp_tar="$(mktemp "/tmp/picoclaw-home.XXXXXX.tar.gz")"

  echo "[deploy] Packing $LOCAL_HOME"
  COPYFILE_DISABLE=1 tar -C "$LOCAL_HOME" -czf "$tmp_tar" .

  upload_file_to_lxc "$tmp_tar" "/tmp/picoclaw-home.tar.gz" root root 0644
  rm -f "$tmp_tar"

  echo "[deploy] Syncing picoclaw home into container"
  remote_sh "pct exec $CTID -- bash -lc 'install -d -o picoclaw -g picoclaw /home/picoclaw/.picoclaw && tar -xzf /tmp/picoclaw-home.tar.gz -C /home/picoclaw/.picoclaw && rm -f /tmp/picoclaw-home.tar.gz && chown -R picoclaw:picoclaw /home/picoclaw/.picoclaw'"

  ensure_runtime_dirs
}

restart_services() {
  if [[ "$SKIP_RESTART" -eq 1 ]]; then
    echo "[deploy] Skipping service restart"
    return
  fi

  echo "[deploy] Restarting gateway service"
  remote_sh "pct exec $CTID -- bash -lc 'sv restart /etc/service/${GATEWAY_SERVICE} >/dev/null 2>&1 || sv up /etc/service/${GATEWAY_SERVICE}; sleep 1; sv status /etc/service/${GATEWAY_SERVICE}'"

  if [[ "$ENABLE_BRIDGE" -eq 1 ]]; then
    echo "[deploy] Enabling and restarting bridge service"
    remote_sh "pct exec $CTID -- bash -lc 'rm -f /etc/sv/${BRIDGE_SERVICE}/down; sv restart /etc/service/${BRIDGE_SERVICE} >/dev/null 2>&1 || sv up /etc/service/${BRIDGE_SERVICE}; sleep 1; sv status /etc/service/${BRIDGE_SERVICE}'"
    return
  fi

  if [[ "$COPY_BRIDGE" -eq 1 ]]; then
    echo "[deploy] Bridge script updated"
    remote_sh "pct exec $CTID -- bash -lc 'if [ -f /etc/sv/${BRIDGE_SERVICE}/down ]; then echo \"[deploy] Bridge service is disabled (down file present)\"; else sv restart /etc/service/${BRIDGE_SERVICE} >/dev/null 2>&1 || sv up /etc/service/${BRIDGE_SERVICE}; sleep 1; sv status /etc/service/${BRIDGE_SERVICE}; fi'"
  fi
}

tail_logs() {
  if [[ "$SHOW_LOGS" -eq 0 || "$LOG_LINES" -eq 0 ]]; then
    return
  fi

  echo "[deploy] Last $LOG_LINES lines from gateway log"
  remote_sh "pct exec $CTID -- bash -lc 'if [ -f \"$GATEWAY_LOG\" ]; then tail -n $LOG_LINES \"$GATEWAY_LOG\"; else echo \"Log file not found: $GATEWAY_LOG\"; fi'"

  if [[ "$ENABLE_BRIDGE" -eq 1 || "$COPY_BRIDGE" -eq 1 ]]; then
    echo "[deploy] Last $LOG_LINES lines from bridge log"
    remote_sh "pct exec $CTID -- bash -lc 'if [ -f \"$BRIDGE_LOG\" ]; then tail -n $LOG_LINES \"$BRIDGE_LOG\"; else echo \"Log file not found: $BRIDGE_LOG\"; fi'"
  fi
}

need_cmd ssh
need_cmd rsync
need_cmd tar

require_int "$CTID" "--ctid"
require_int "$LXC_DISK_SIZE" "--disk-size"
require_int "$LXC_CORES" "--cores"
require_int "$LXC_MEMORY" "--memory"
require_int "$LXC_SWAP" "--swap"
require_int "$LOG_LINES" "--log-lines"
require_int "$BRIDGE_PORT" "--bridge-port"

if [[ "$BOOTSTRAP" -eq 1 ]]; then
  bootstrap_container
fi

ensure_container_running

ensure_runtime_dirs

echo "[deploy] Ensuring workspace runit supervisor"
install_workspace_services_runit

if [[ "$SYNC_HOME" -eq 1 ]]; then
  sync_home_dir
fi

sync_workspace_runit_skill

if [[ "$SKIP_BUILD" -eq 0 ]]; then
  build_binary
else
  echo "[deploy] Skipping build"
fi

if [[ ! -f "$LOCAL_BIN" ]]; then
  echo "Local binary not found: $LOCAL_BIN" >&2
  exit 1
fi

echo "[deploy] Uploading binary to LXC $CTID:$REMOTE_BIN"
upload_file_to_lxc "$LOCAL_BIN" "$REMOTE_BIN" root root 0755

if [[ "$COPY_BRIDGE" -eq 1 ]]; then
  echo "[deploy] Uploading bridge script to LXC $CTID:$BRIDGE_DEST"
  upload_file_to_lxc "$BRIDGE_SRC" "$BRIDGE_DEST" picoclaw picoclaw 0755
fi

restart_services
tail_logs

if [[ "$SHOW_LOGS" -ne 0 && "$LOG_LINES" -ne 0 ]]; then
  echo "[deploy] Last $LOG_LINES lines from workspace-services log"
  remote_sh "pct exec $CTID -- bash -lc 'if [ -f \"$WORKSPACE_SERVICES_LOG\" ]; then tail -n $LOG_LINES \"$WORKSPACE_SERVICES_LOG\"; else echo \"Log file not found: $WORKSPACE_SERVICES_LOG\"; fi'"
fi

echo "[deploy] Container network state"
remote_sh "pct exec $CTID -- ip -4 -brief addr show dev eth0"

echo "[deploy] Done"
