# DRY-Up Plan (Routing + Delivery)

Goal: reduce duplication and drift in routing/session conventions and message delivery wiring across the main agent loop, subagents, cron, and heartbeat — without introducing heavy abstractions.

Guiding rules:

- DRY *mechanics + conventions* (string formats, parsing, defaults), keep *policy decisions* at the edges (cron vs heartbeat vs subagent).
- Avoid import cycles: shared helpers live in a leaf package (e.g. `pkg/routing`).
- TDD: add tests that lock in current behavior before moving code.

## Milestones

- [x] 1) Add `pkg/routing` helpers + tests
- [x] 2) Migrate agent session-key checks to `pkg/routing`
- [x] 3) Migrate system-route encode/decode to `pkg/routing` (subagent reports + agent system message routing)
- [x] 4) Migrate heartbeat spawn routing to `pkg/routing`
- [x] 5) DRY message tool registration/config (main agent + subagent)
- [x] 6) DRY last-active-target resolution (cron + heartbeat)
- [x] 7) Optional: add baseline outbound validation at channel dispatch

## 1) `pkg/routing` helpers + tests

- [x] Add `pkg/routing/session_keys.go`
- [x] Add `pkg/routing/session_keys_test.go` (covers heartbeat + cron detection)
- [x] Add `pkg/routing/system_route.go` (encode/decode `channel:chat_id` splitting on first colon)
- [x] Add `pkg/routing/system_route_test.go` (covers chat ids containing `:`)

## 2) Agent session-key checks

- [x] Add tests for current `shouldEchoToolCallsForSession` behavior
- [x] Update `pkg/agent/toolexec.go` to use `pkg/routing` (no behavior change)

## 3) System-route encode/decode

- [x] Add/extend tests for `processSystemMessage` routing behavior
- [x] Update `pkg/agent/loop.go` to use `routing.DecodeSystemRoute`
- [x] Update `pkg/tools/subagent_report.go` and `pkg/tools/subagent.go` to use `routing.EncodeSystemRoute`

## 4) Heartbeat spawn routing

- [x] Add tests for `pkg/tools/spawn.go` heartbeat session rewriting
- [x] Replace string parsing in `pkg/tools/spawn.go` with `pkg/routing` helpers
- [x] Update any heartbeat session-key creation sites (e.g. `cmd/picoclaw/main.go`) to use `pkg/routing`

## 5) DRY message tool registration

- [x] Add a small helper in `pkg/tools` to configure/register `message` consistently
- [x] Migrate `pkg/agent/loop.go` message tool wiring to the helper
- [x] Migrate `pkg/tools/subagent.go` message tool wiring to the helper

## 6) DRY last-active-target resolution

- [x] Extract a helper that reads `cron/last_target.json` and returns `(channel, chat_id, ok, err)`
- [x] Use it from cron and heartbeat (each keeps its own policy for missing targets)

## 7) Optional outbound guard

- [x] Add minimal validation in `pkg/channels/manager.go` outbound dispatch (empty target, unknown channel, etc.)
- [ ] Consider whether media-path validation belongs here as a final backstop
