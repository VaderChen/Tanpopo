# Tanpopo

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

Tanpopo is a local model service manager written in Go. Its name is the Japanese word for dandelion (たんぽぽ): generated tokens spread outward like dandelion seeds. It manages a cross-platform `llama-server` for GGUF models and a native Swift/MLX `mlx-server` on Apple Silicon.

## Highlights

- Start, stop, restore, and inspect local model runtimes from one web interface.
- Scan nested GGUF files and complete MLX model directories without exposing full directory paths in model selectors.
- Download public, gated, or private Hugging Face GGUF files and MLX repositories.
- Save reusable launch profiles for context size, GPU layers, threads, KV cache, MTP, and DFlash.
- Detect DFlash support and require a compatible Draft model before enabling it.
- Use an ephemeral local chat with Markdown, math rendering, separated reasoning, animated waiting state, token counts, and output tokens per second.
- Use a real OpenAI-compatible per-token SSE stream from MLX; disconnecting or cancelling the client immediately cancels the matching generation task.
- Protect the model API with access keys, an IP allowlist, both, or neither.
- Restore the selected Runtime and running state before admin sign-in after restarting Tanpopo.
- Choose `AUTO`, Traditional Chinese, English, Japanese, or Korean for the management interface.
- On macOS, run as a native AppKit/WKWebView app and optionally remain in the system menu bar.

## Quick start

Development requires Go 1.25 or later and a C/C++ toolchain with CMake. Building `mlx-server` additionally requires Swift 6 and Xcode on Apple Silicon. Packaged deployments carry pinned runtime sources and any available prebuilt binaries.

```bash
cd /path/to/Tanpopo
./run.command
```

The first launch creates `agent.properties` from `agent.sample.properties`. The management service listens on `0.0.0.0:10082` by default and is available locally at:

```text
http://127.0.0.1:10082
```

Default administrator credentials:

```text
account:  root
password: root
```

You can change the credentials or disable admin sign-in from Settings. Disabling authentication requires confirmation. Credentials remain stored locally so authentication can be re-enabled later.

On macOS, resident mode places Tanpopo in the system menu bar. Closing the window then hides only the UI; use **Quit Tanpopo** from the menu to stop the service completely. Resident mode is off by default.

```bash
TANPOPO_UI=shell ./run.command  # Force foreground shell mode
TANPOPO_UI=gui ./run.command    # Force the native UI where supported
```

## Model runtimes

### llama-server

`llama-server` provides the broadest GGUF and platform compatibility. The default GGUF directory is `~/services/models`. Tanpopo combines a selected model with a saved launch profile only when starting the runtime. Multimodal models may select a matching `mmproj` file; the mmproj control is hidden when MLX is selected.

DFlash is off by default. Tanpopo inspects model architecture and pairing metadata before enabling the switch. If the required Draft GGUF is missing, the switch is reset and the UI asks the user to download it.

### mlx-server

`mlx-server` is a native Apple Silicon runtime built with Swift, SwiftNIO, and MLX Swift. It does not invoke Python, pip, or `mlx_lm.server`. The default MLX model directory is `~/services/mlx-models`; a valid model directory must contain `config.json` and safetensors weights.

Supported compatibility endpoints include:

```text
GET  /health
GET  /v1/health
GET  /props
GET  /models
GET  /v1/models
POST /chat/completions
POST /v1/chat/completions
POST /v1/completions
POST /completion
```

Use the Base URL shown in Runtime status, normally `http://127.0.0.1:8080/v1`. `/models` and `/v1/models` return the currently loaded model ID for clients that support model discovery.

For `/v1/chat/completions`, `stream: true` emits OpenAI-compatible SSE events while tokens are generated. When the HTTP channel closes, the server cancels the producer task and the underlying MLX generation stream instead of waiting for completion.

## API security

Settings provides two independent controls:

| Access key | IP allowlist | Result |
| --- | --- | --- |
| Off | Off | No additional restriction |
| On | Off | A valid Bearer or `X-OpenLoader-Key` value is required |
| Off | On | The direct client IP must match the allowlist |
| On | On | Both checks must pass |

Issued key plaintext is shown once. Only a SHA-256 hash is persisted. IPv4, IPv6, CIDR ranges, wildcard suffixes, and `*` are supported in the allowlist. Runtime policy snapshots are fail-closed if enabled but missing or invalid.

## Local data

Runtime selections and desired running state are saved in `data/runtime_state.json`. General settings are written atomically to `data/settings.json`; admin authentication remains in `agent.properties`; API access policy is stored separately with restricted file permissions. Chat messages are not persisted.

## Building

```bash
go build -buildvcs=false -trimpath -o bin/Tanpopo ./src/cmd/llamaloader
./scripts/build-mlx-server-runtime.sh  # Apple Silicon only
```

`run.command` reuses a runtime when its pinned version matches and rebuilds only when required. Set `TANPOPO_REBUILD_RUNTIMES=1` to force a runtime rebuild.

## License and notices

See [LICENSE](LICENSE), [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md), and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Bundled runtime components retain their respective upstream licenses.
