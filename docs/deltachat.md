# DeltaChat Bridge

This repo includes an optional `deltachat-bridge` Docker service that connects Delta Chat to PicoClaw's `deltachat` channel over WebSocket.

## Quick Start (nine.testrun.org)

1) Enable the channel in `picoclaw-home/config.json`:

```json
{
  "channels": {
    "deltachat": {
      "enabled": true,
      "bridge_url": "ws://deltachat-bridge:3100",
      "allow_from": []
    }
  }
}
```

2) Add these values in `.env` (or uncomment in `.env.example` and copy):

```env
PICOCLAW_CHANNELS_DELTACHAT_ENABLED=true
PICOCLAW_CHANNELS_DELTACHAT_BRIDGE_URL=ws://deltachat-bridge:3100

DELTACHAT_SETUP_QR=DCACCOUNT:https://nine.testrun.org/new
DELTACHAT_DISPLAY_NAME=Alice
```

3) Start the optional bridge profile:

```bash
docker compose --profile deltachat up -d --build
```

4) Watch startup logs:

```bash
docker compose logs -f deltachat-bridge
```

On first run, the bridge will auto-provision a new chatmail account from `nine.testrun.org` and log:

- `Using Delta Chat identity: <random>@nine.testrun.org`

That address is your bridge identity for contacts/groups.

5) After bridge is ready, restart PicoClaw once so the channel reconnects cleanly:

```bash
docker compose restart picoclaw
```

## Notes

- Account state is persisted in Docker volume `picoclaw-deltachat-accounts`.
- The bridge listens inside Docker at `ws://deltachat-bridge:3100`.
- If `allow_from` is empty, all senders are accepted. Restrict it once you know the sender addresses you trust.
- File attachments are supported both ways. Inbound DeltaChat files are delivered as media paths under `/accounts/...`.
- For outbound files, prefer paths under `/root/.picoclaw/workspace/...` so both containers can access them.

## Typing + Reactions

- While PicoClaw is processing an incoming DeltaChat message, the channel sends a best-effort typing signal via draft updates.
- Delta Chat core does not currently expose remote typing events to bots, so user -> PicoClaw typing indicators are not available.
- Incoming DeltaChat reactions are forwarded to PicoClaw as synthetic inbound messages with metadata `event=reaction`.
- Outbound reactions are supported with a command-style message content:

```text
/react <message_id> <emoji>
```

Example:

```text
/react 12345 👍
```

## Bring Your Own Credentials

Instead of onboarding QR setup, you can provide explicit credentials:

```env
DELTACHAT_SETUP_QR=
DELTACHAT_EMAIL=alice@your-domain.example
DELTACHAT_PASSWORD=your-mail-password
```
