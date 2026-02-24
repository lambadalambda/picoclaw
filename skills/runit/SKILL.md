---
name: runit
description: Manage long-running workspace services (runit/runsvdir) inside the Docker container.
metadata: {"nanobot":{"emoji":"🧰","os":["linux"],"requires":{"bins":["runsvdir","runsv","sv","svlogd"]}}}
---

# runit (workspace services)

This repo's Docker image can start a lightweight service supervisor (runit)
so the agent can run long-lived daemons that boot with PicoClaw.

## Where services live

Default service root (bind-mounted, persistent):

`/root/.picoclaw/workspace/services/`

Each service is a directory:

```text
/root/.picoclaw/workspace/services/<name>/run
/root/.picoclaw/workspace/services/<name>/log/run   # optional (svlogd)
/root/.picoclaw/workspace/services/<name>/down      # optional (disable autostart)
```

Important: `run` (and `log/run`) must be executable.

## Minimal service example

Example: a tiny HTTP server on port 8081.

Create:

- `/root/.picoclaw/workspace/services/hello-http/run`

```sh
#!/bin/sh
set -eu

exec python3 -m http.server 8081
```

Then mark executable:

```bash
chmod +x /root/.picoclaw/workspace/services/hello-http/run
```

## Logging (recommended)

Add a `log/run` so stdout/stderr are captured on disk:

- `/root/.picoclaw/workspace/services/hello-http/log/run`

```sh
#!/bin/sh
set -eu

LOG_DIR="/root/.picoclaw/workspace/services/hello-http/log"
mkdir -p "$LOG_DIR"
exec svlogd -tt "$LOG_DIR"
```

Then:

```bash
chmod +x /root/.picoclaw/workspace/services/hello-http/log/run
```

Log file:

`/root/.picoclaw/workspace/services/hello-http/log/current`

## Control services

`sv` is the primary CLI.

If `$SVDIR` is set to `/root/.picoclaw/workspace/services`, you can use just
the service name:

```bash
sv status hello-http
sv up hello-http
sv down hello-http
sv restart hello-http
```

Otherwise, point `sv` at the directory explicitly:

```bash
sv status /root/.picoclaw/workspace/services/hello-http
```

## Disabling autostart

Create a `down` file:

```bash
touch /root/.picoclaw/workspace/services/hello-http/down
sv down hello-http
```

Remove it to re-enable autostart:

```bash
rm /root/.picoclaw/workspace/services/hello-http/down
sv up hello-http
```
