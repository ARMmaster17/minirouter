# Minirouter

Minirouter is a lightweight, local-first AI model router designed to simplify how you interact with multiple LLM providers. It acts as a smart gateway, routing your requests to the most appropriate model based on cost, context limits, and custom routing rules, while providing a unified OpenAI-compatible API.

Whether you are using cloud giants like OpenAI and Gemini or running local models via Ollama and LM Studio, Minirouter gives you a single endpoint to manage them all.

## Getting Started

### Raw Binary
If you have Go installed, you can run Minirouter directly from the source:

```bash
# Run immediately
go run ./cmd/minirouter

# Or build the binary
go build -o minirouter ./cmd/minirouter
./minirouter
```

**Custom Configuration:** By default, Minirouter boots with a built-in mock provider for easy testing. To use your own providers, create a `config.json` file and point the server to it using an environment variable:

```bash
export MINIROUTER_CONFIG=/path/to/your/config.json
./minirouter
```

### Docker Compose
For a quick, containerized deployment, use Docker Compose:

```bash
docker compose up --build
```

## Configuration & Tuning

Minirouter is configured via a JSON file (see `config.example.json` for a template). 

### Key Tuning Options

- **Provider Overrides**: You can manually specify `contextLimit`, `tokenInputCost`, and `tokenOutputCost` for any model. If provided in the config, these override the metadata returned by the upstream provider.
- **Thrash Limit**: For local runtimes (like Ollama or LM Studio), set a `thrashLimit` greater than zero. This applies a recency preference to the last N models used, reducing the performance hit of frequently loading and unloading models from VRAM.
- **Routing Tiers**: Organise your models into ordered tiers. Minirouter will attempt to resolve requests through these tiers sequentially, ensuring high-quality models are tried first, with cheaper or smaller models as fallbacks.
- **Failure Policies**: Configure how the router handles errors. You can define specific retry policies or explicit fallback model IDs based on the type of failure encountered.
- **API Security**: Enable `server.incomingAPIKey` in your config to protect your endpoints. When set, requests must include an `Authorization: Bearer <key>` header.

### Monitoring
Minirouter includes a built-in **HTMX Dashboard** accessible at the root URL (`/`). The dashboard provides real-time visibility into routed requests and aggregate statistics via SSE.

## Support & Contributing

We welcome community feedback! If you encounter a bug or have an idea for a new feature, please open an issue on GitHub:

👉 [GitHub Issues](https://github.com/ARMmaster17/minirouter/issues)

## License

Minirouter is licensed under the **GNU Affero General Public License v3 (AGPLv3)**.
