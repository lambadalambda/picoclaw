# Agent Rules for picoclaw

Rules and conventions for AI agents (and humans) working on this codebase.

## Commits

- **Small, topical commits.** Each commit should do one thing. Separate tests from implementation, separate feature areas from each other.
- **Commit early and often.** Don't accumulate a massive diff. If something works, commit it.
- **Conventional commit messages.** Use the format `type(scope): description` — e.g. `feat(telegram): implement media sending`, `test(providers): add retry tests`, `fix(agent): handle nil callback`.
- **Never commit secrets.** No API keys, `.env` files, or credentials. These are gitignored.

## Testing

- **TDD (Test-Driven Development).** Write tests first (RED), then implement (GREEN). This applies to all new features and bug fixes.
- **Verify RED before GREEN.** Run the tests and confirm they fail for the right reason before writing the implementation.
- **Run the full suite before finishing.** `go test ./...` must pass. `go build ./...` must succeed.
- **Use `httptest` for HTTP-level tests.** Don't hit real APIs in unit tests.

## Code style

- **Go conventions.** Follow standard Go style — `gofmt`, exported names are PascalCase, unexported are camelCase.
- **Keep it minimal.** picoclaw is meant to be small and understandable. Don't over-abstract.
- **Error handling over panics.** Return errors, log warnings. Don't panic in library code.
- **Log at the right level.** DEBUG for raw payloads, INFO for flow milestones, WARN for recoverable problems, ERROR for failures.

## Architecture

- **Channels** (`pkg/channels/`) handle platform-specific I/O (Telegram, Discord, Slack, etc.). They implement the `Channel` interface.
- **Bus** (`pkg/bus/`) is the message broker between channels and the agent loop. Inbound messages go in, outbound messages come out.
- **Agent loop** (`pkg/agent/`) orchestrates LLM calls and tool execution.
- **Tools** (`pkg/tools/`) are capabilities the agent can invoke (message, exec, web search, file ops, etc.).
- **Providers** (`pkg/providers/`) abstract LLM API calls (OpenAI-compatible HTTP, Claude SDK, Codex SDK).
- **The gateway is not HTTP.** There's no REST API. Users interact via messaging channels or the CLI REPL (`picoclaw agent`).

## Docker

- The Docker setup uses a bind mount at `./picoclaw-home/` for the picoclaw home directory, so config can be edited from the host.
- The entrypoint script patches `config.json` with `jq` from environment variables because the env tag templates in `ProviderConfig` don't actually work with `caarlos0/env`.
- Build with `docker compose build`, run with `docker compose up`.
