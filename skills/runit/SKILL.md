---
name: runit
description: Manage long-running workspace services (runit/runsvdir) on this host.
metadata: {"nanobot":{"emoji":"🧰","os":["linux"],"requires":{"bins":["runsvdir","runsv","sv","svlogd"]}}}
---

# runit (workspace services)

This setup provides a dedicated runit supervisor service (`workspace-services`)
for agent-managed daemons.

## Where services live

Default service root:

`/home/picoclaw/.picoclaw/workspace/services/`

Equivalent generic form:

`$PICOCLAW_HOME/workspace/services`

Each service is a directory:

```text
/home/picoclaw/.picoclaw/workspace/services/<name>/run
/home/picoclaw/.picoclaw/workspace/services/<name>/log/run   # optional (svlogd)
/home/picoclaw/.picoclaw/workspace/services/<name>/down      # optional (disable autostart)
```

Important: `run` (and `log/run`) must be executable.

## Workspace supervisor service

Check that workspace service supervision is running:

```bash
sv status /etc/service/workspace-services
```

Supervisor log:

`/home/picoclaw/.picoclaw/log/workspace-services/current`

```bash
tail -n 80 /home/picoclaw/.picoclaw/log/workspace-services/current
```

## Minimal service example

Example: a tiny HTTP server on port 8081.

Create:

- `/home/picoclaw/.picoclaw/workspace/services/hello-http/run`

```sh
#!/bin/sh
set -eu

exec python3 -m http.server 8081
```

Then mark executable:

```bash
chmod +x /home/picoclaw/.picoclaw/workspace/services/hello-http/run
```

## Logging (recommended)

Add a `log/run` so stdout/stderr are captured on disk:

- `/home/picoclaw/.picoclaw/workspace/services/hello-http/log/run`

```sh
#!/bin/sh
set -eu

LOG_DIR="/home/picoclaw/.picoclaw/workspace/services/hello-http/log"
mkdir -p "$LOG_DIR"
exec svlogd -tt "$LOG_DIR"
```

Then:

```bash
chmod +x /home/picoclaw/.picoclaw/workspace/services/hello-http/log/run
```

Log file:

`/home/picoclaw/.picoclaw/workspace/services/hello-http/log/current`

## Control services

`sv` is the primary CLI.

Use explicit paths (works even if `SVDIR` is not exported in your shell):

```bash
SVC_ROOT=/home/picoclaw/.picoclaw/workspace/services
sv status "$SVC_ROOT/hello-http"
sv up "$SVC_ROOT/hello-http"
sv down "$SVC_ROOT/hello-http"
sv restart "$SVC_ROOT/hello-http"
```

If you prefer service names, export `SVDIR` first:

```bash
export SVDIR=/home/picoclaw/.picoclaw/workspace/services
sv status hello-http
```

## Disabling autostart

Create a `down` file:

```bash
SVC_ROOT=/home/picoclaw/.picoclaw/workspace/services
touch "$SVC_ROOT/hello-http/down"
sv down "$SVC_ROOT/hello-http"
```

Remove it to re-enable autostart:

```bash
SVC_ROOT=/home/picoclaw/.picoclaw/workspace/services
rm "$SVC_ROOT/hello-http/down"
sv up "$SVC_ROOT/hello-http"
```

## Guardrails

Use this skill for workspace services under `.../workspace/services`.
Do not modify system services under `/etc/sv/picoclaw-gateway` or `/etc/sv/deltachat-bridge` unless explicitly requested.
