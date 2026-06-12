# minirouter

Lightweight, local-first AI model router.

## What is implemented

- Go server scaffold with hexagonal boundaries in `internal/`
- Duplicated freerouter-style rules engine for model classification
- JSON config loading with environment overrides
- Mock provider and provider registry for local testing and deterministic responses
- Failure-aware fallback routing with configurable retry/failover policies
- Concrete provider adapters for OpenAI, Gemini, LM Studio, and Ollama
- OpenAI-compatible `v1/models` and `v1/chat/completions`
- Streaming support using SSE
- Live HTMX dashboard at `/` with SSE refresh
- Request logging with in-memory adapter (hexagonal port, DB-ready)
- Optional inbound API key enforcement for OpenAI-compatible endpoints
- Dockerfile, Compose file, and sample config
- Unit tests for routing, config, app, and HTTP layers

## Run locally

```bash
go test ./...
go run ./cmd/minirouter
```

Set `MINIROUTER_CONFIG` to point at a JSON file if you want to load custom providers.

The root URL (`/`) serves a small dashboard by default. It shows recent routed requests and aggregate routing stats and auto-refreshes via SSE.

If no providers are configured, the binary boots with the built-in mock provider so the service is usable locally without upstream credentials.

## Docker

```bash
docker compose up --build
```

## Configuration

Start from `config.example.json` and add provider entries for:

- Google Gemini
- OpenAI
- LM Studio
- Ollama

Concrete provider adapters live in `internal/adapters/providers`.

- `openai.go`
- `gemini.go`
- `lmstudio.go`
- `ollama.go`

To add a new provider, add a new `.go` file in `internal/adapters/providers` and implement the `app.Provider` interface, then register it in `factory.go`.

The router uses a synthetic `auto` model that is resolved by the rules engine before dispatch.

Each provider can include an optional `thrashLimit` (for example local runtimes like LM Studio/Ollama). When set to a value greater than zero, routing applies a recency preference to the last N distinct models used on that provider, reducing model load/unload thrash when candidates are close.

On server boot, enabled providers are queried for model metadata (context limits and any pricing metadata they expose). Failures are logged as warnings and startup continues.

Each provider model entry can include optional overrides:

- `contextLimit`
- `tokenInputCost` (cost per 1M input tokens)
- `tokenOutputCost` (cost per 1M output tokens)

Override precedence is config-first: if an override is present in config, it replaces upstream metadata for that field.

If `tokenInputCost`/`tokenOutputCost` are missing in both config and upstream metadata, the model is treated as free.

Routing now also applies:

- Context-limit eligibility: models with known context limits below request size are skipped.
- Cost preference: lower-cost models are weighted higher when candidates are otherwise close.

Routing tiers now accept ordered model arrays (`routing.tiers.<TIER>.models`), and selection falls through in that order.

Failure routing lives under `routing.failures` and can retry or fall back to explicit model IDs depending on the error class.

Server-level options:

- `server.frontendEnabled` (default `true`): serves the HTMX dashboard at `/` and UI fragment/SSE endpoints.
- `server.incomingAPIKey` (default empty): when set, requests to `v1/models` and `v1/chat/completions` must include `Authorization: Bearer <key>`.

Environment overrides:

- `MINIROUTER_FRONTEND_ENABLED`
- `MINIROUTER_INCOMING_API_KEY`

Request logging uses a hexagonal `RequestLogStore` port (`internal/app/request_log.go`) with an in-memory implementation in `internal/adapters/logs/memory.go`. This keeps the app layer storage-agnostic and ready for a future PostgreSQL adapter.

## Next steps

- Implement OpenAI passthrough routes beyond chat completions
- Expand failure-type policies and request-level routing controls