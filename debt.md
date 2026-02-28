# Tech Debt

Captured from a high-level audit (Feb 2026). This is a backlog of risks + refactors to consider.

## Security / Safety

- [ ] Sanitize session snapshot filenames (path traversal risk) and write atomically (`pkg/session/manager.go`)
- [ ] Workspace-scope filesystem tools + add size caps (`read_file`, `write_file`, `list_dir`) (`pkg/tools/filesystem.go`)
- [ ] Add SSRF protections + hard byte limits to `web_fetch` (currently `io.ReadAll` unbounded) (`pkg/tools/web.go`)
- [ ] Enforce attachment root allowlist for outbound media (prevent local-file exfil) (`pkg/tools/message.go`, `pkg/channels/telegram.go`, `pkg/channels/deltachat.go`, `scripts/deltachat_bridge.py`)
- [ ] Reduce sensitive leakage in logs/transcripts (tool args at INFO, weak/no redaction) (`pkg/tools/registry.go`, `pkg/tools/executor.go`, `pkg/session/transcript.go`, `pkg/agent/loop.go`)

## Reliability / Correctness

- [ ] Budget multimodal payloads (`Message.Parts`) in message budgeting to avoid large requests/costs (`pkg/providers/budget.go`)
- [ ] OpenAI-compatible: only append synthetic user message for tool-result images if at least 1 image encoded (`pkg/providers/http_provider.go`)
- [ ] `read_file` should not read arbitrarily large files into memory by default (`pkg/tools/filesystem.go`)
- [ ] DeltaChat attachment readiness: existence != fully written; consider stable-size or header-decode checks (`pkg/channels/deltachat.go`, `scripts/deltachat_bridge.py`)
- [ ] Log when we retry sans images on policy/safety refusal (currently silent recovery) (`pkg/llmloop/run.go`)
- [ ] Standardize: session keys are data, but filenames should always use a sanitized form (snapshots + transcript + `session_history`) (`pkg/session/transcript.go`, `pkg/session/manager.go`, `pkg/tools/session_history.go`)
- [ ] Bus drops messages when buffers fill; add structured logs/counters or backpressure strategy (`pkg/bus/bus.go`)
- [ ] DeltaChat bridge security posture: ensure it binds to localhost by default and/or requires a shared secret if exposed (`scripts/deltachat_bridge.py`)
- [ ] Use atomic writes for frequently-updated JSON stores (sessions + cron) (`pkg/session/manager.go`, `pkg/cron/service.go`)

## DRY / Architecture

- [ ] Centralize safe path resolution + allowlists for all tools/channels dealing with local paths
- [ ] Centralize SSRF-safe URL validation + bounded download helper (share across `web_fetch` + `image_inspect` + downloads)
- [ ] Centralize redaction rules (common secret keys/headers, tokens in URLs) and apply consistently
- [ ] Standardize tool failure conventions (error return vs "Error: ..." content) to simplify recovery logic
- [ ] Make "gateway mode" vs "local CLI mode" explicit: default-deny risky tools in gateway mode, allow in CLI mode
- [ ] Reduce duplicated DeltaChat attachment fallback logic (bridge vs Go channel) and pass structured metadata (`wait_ms`, `resolved_via`)
- [ ] Add a simple retention policy for multimodal parts (keep last N images / summarize) to avoid payload growth

## Open Question

- [ ] Clarify threat model: is the gateway reachable by untrusted senders? This determines whether tool sandboxing/allowlists must be on by default.
