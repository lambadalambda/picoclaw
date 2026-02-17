# Troubleshooting

## `no API key configured for provider`

Set provider credentials in either:

- `.env` (recommended with Docker)
- `picoclaw-home/config.json`

Then restart:

```bash
docker compose restart picoclaw
```

## Telegram `Conflict: terminated by other getUpdates`

You have multiple bot instances using the same token.

Fix:

- run only one `picoclaw gateway` instance per token
- stop duplicate container/processes

## Config changes not applying

If you changed `.env` or `config.json`, restart the service:

```bash
docker compose restart picoclaw
```

## Subagent task seems stuck

Check status from the agent (spawn tool `action=status` or `action=list`), and cancel if needed (`action=cancel`).

If still unclear, inspect logs:

```bash
docker compose logs -f picoclaw
```

Look for `trace_id` and `task_id` fields to correlate events.
