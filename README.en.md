# Tanpopo

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

Tanpopo is a local model service manager written in Go. Its name is the Japanese word for dandelion (たんぽぽ): generated tokens spread outward like dandelion seeds. It manages a cross-platform `llama-server` for GGUF models and a native Swift/MLX `mlx-server` on Apple Silicon.

## Highlights

- Start, stop, restore, and inspect local model runtimes from one web interface.
- Scan nested GGUF files and complete MLX model directories without exposing full directory paths in model selectors. On Apple Silicon, `mlx-server` can load supported GGUF models directly without a safetensors conversion step.
- Group mlx-server targets by GGUF and MLX. Language models not yet declared compatible by the runtime remain selectable in an **Untested (N)** group and are validated by an actual load attempt.
- Download public, gated, or private Hugging Face GGUF files and MLX repositories.
- Pick popular GGUF or MLX models from a separate JSON catalog. Each format is grouped into 8B-class, 30B-class, and 70B-and-above tiers, with model names sorted alphabetically inside each tier. The quick picker fills in the Runtime, repository, revision, and GGUF filename without hard-coding model entries in JavaScript.
- When the server supports byte ranges, download large files in 64 MiB segments with at most four workers; otherwise fall back to one stream. Completed queue entries are removed automatically. The desktop app can open the destination folder, while browser mode keeps that action disabled.
- Save reusable launch profiles for context size, GPU layers, threads, KV cache, MTP, and DFlash.
- Keep the independent DFlash and MMap switches in a compact Advanced settings popover so new options do not continuously lengthen the page.
- Detect DFlash support and require a compatible Draft model before enabling it. The separate MMap switch supports both llama-server and Apple Silicon mlx-server, using file-backed paging to reduce model-loading memory pressure.
- Automatically discover and download a matching DFlash Draft from the same or a separate Hugging Face repository, verified through metadata and model configuration instead of a hard-coded model list.
- Use an ephemeral local chat with Markdown, math rendering, separated reasoning, animated waiting state, token counts, and output tokens per second.
- Starting a model immediately opens a loading notice explaining that large models may require conversion. Test a running model through an animated dialog, then inspect input/output tokens, generation speed, elapsed time, or an explicit loading/connection error. Main-page refreshes use a separate compact progress dialog.
- Use a real OpenAI-compatible per-token SSE stream from MLX; disconnecting or cancelling the client immediately cancels the matching generation task.
- Protect the model API with access keys, an IP allowlist, both, or neither.
- Restore the selected Runtime and running state before admin sign-in after restarting Tanpopo.
- Choose `AUTO`, Traditional Chinese, English, Japanese, or Korean for the management interface.
- Choose among Dandelion, Sky Blue, Sakura Pink, and the dark Midnight Purple theme.
- View CPU, GPU, MEMORY, and network status in a bottom bar refreshed every three seconds. Utilization uses muted green, yellow, and red bands at 50% and 80%.
- Inspect read-only OS, kernel, architecture, hostname, CPU, GPU, memory, network interface, and reachable management URL details under **System settings → System information**. Loopback URLs are omitted from shared URL lists.
- Check the latest stable GitHub Release at startup and every hour, notify when an update is available, and expose the current version, manual update check, and clickable repository URL under **System settings → About**.
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

Initial local credentials come from `agent.sample.properties` and are intended only for the first launch. Change them before allowing LAN access or enabling reverse proxy. You can also disable admin sign-in from Settings after confirming the warning; credentials remain local so authentication can be re-enabled later.

On macOS, resident mode places Tanpopo in the system menu bar. Closing the window then hides only the UI; use **Quit Tanpopo** from the menu to stop the service completely. Resident mode is off by default.

```bash
TANPOPO_UI=shell ./run.command  # Force foreground shell mode
TANPOPO_UI=gui ./run.command    # Force the native UI where supported
```

The application version uses `1.YY.MMDD build HHmm`, for example `1.26.0829 build 1430`. The root `VERSION` file stores only the `1.YY.MMDD` release version used for GitHub comparisons; build and launch scripts inject `build HHmm` from the build time. Unless `TANPOPO_VERSION` is explicitly set, `pack.command` validates `VERSION` against the current `Asia/Taipei` date so a package cannot silently reuse the previous day's version; set `TANPOPO_VERSION` only when intentionally rebuilding a historical release. Tanpopo checks the latest stable GitHub Release once at startup and then hourly; the same UI session reports each new version only once. **System settings → About** shows the current and latest versions, last check time, a manual check button, and a clickable [GitHub repository URL](https://github.com/VaderChen/Tanpopo). Update checks compare only `1.YY.MMDD`; drafts and prereleases are not treated as the latest version.

## Model runtimes

### llama-server

`llama-server` provides the broadest GGUF and platform compatibility. The default GGUF directory is `~/services/models`. Tanpopo combines a selected model with a saved launch profile only when starting the runtime. Multimodal models may select a matching `mmproj` file.

DFlash is off by default. Tanpopo inspects model architecture and pairing metadata before enabling the switch. If the required Draft GGUF is missing, the switch is reset and the UI asks the user to download it.

MMap is a separate switch in the runtime page's Advanced settings popover, is off by default, and supports both `llama-server` and Apple Silicon `mlx-server`. llama-server uses `--load-mode mmap`; mlx-server maps supported safetensors and directly usable GGUF weights as file-backed pages. Launch profiles can use Auto or a 4, 8, 16, 24, 32, 48, 64, 96, or 128 GB memory reserve target. llama-server uses `--fit-target` to configure GPU Layers, while mlx-server uses physical memory minus the reserve as its MLX allocation target. This value is not a hard runtime memory limit. First-token latency and generation speed still depend on storage performance and page pressure.

Launch profiles select Q8 or Q4 KV Cache. A separate Advanced settings switch decides whether the selected format is used for the current launch; turning it off keeps the cache unquantized. Q4 saves more memory, while Q8 preserves more precision. KV Cache quantization and DFlash are mutually exclusive in both the UI and backend.

### mlx-server

`mlx-server` is a native Apple Silicon runtime built with Swift, SwiftNIO, and MLX Swift. It does not invoke Python, pip, or `mlx_lm.server`. It accepts complete MLX directories from `~/services/mlx-models` and supported GGUF files from the regular GGUF directory. Native MLX model types are reported dynamically by the bundled `mlx-swift-lm 3.31.4` registries and include multimodal Gemma 4. The runtime currently reports direct GGUF support for Gemma, Llama, Mimo, MiniCPM, Mistral, Qwen 2, Qwen 3, Qwen 3.5, and SmolLM3; unsupported detected language models stay visible but disabled. A matching `mmproj` may be selected for multimodal Qwen 3.5 GGUF models.

GGUF loading defaults to a group size of 64 and the quality profile; these can be changed with `--gguf-group-size 32|64` and `--gguf-profile quality|speed`. GGUF targets use standard MLX generation. Existing DFlash profiles continue to require an MLX safetensors target and a compatible Draft model.

For mlx-server, enabling KV Cache quantization applies the profile's Q8 or Q4 mode with a group size of 64 and delayed quantization after 2,048 tokens. The profile Context Size becomes the quantized KV Cache limit.

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

## NetPass reverse proxy

The optional NetPass page is available only when administrator sign-in and model API Access Key validation are both enabled. The page checks those prerequisites when the user requests activation, displays a multilingual policy and responsibility notice, and requires an explicit acknowledgment before the reverse proxy can start. Once connected, it reveals the assigned public NetPass URL; enabling the service makes both the local management interface and API calls reachable from the public network.

NetPass is a technical collaboration between Tanpopo and Mars Semi Corp. for technical exchange and experimental use. It is currently provided without charge; Mars Semi Corp. may revise its usage policy at any time and will announce material changes separately. Users are responsible for evaluating the need, applying appropriate security controls, and accepting network-security risks. Mars Semi Corp. and Tanpopo accept no liability for security incidents, data exposure, or other losses arising from use of the service.

NetPassClient is a separate closed-source component. Its source, binaries, credentials, and official packaging process are not part of this repository and are not synchronized to GitHub. Official signed installers may include a controlled platform-specific binary. Its server API key is stored only in restricted local configuration and is never returned as plaintext by the management API.

## Local data

Runtime selections and desired running state are saved in `data/runtime_state.json`. General settings are written atomically to `data/settings.json`; admin authentication remains in `agent.properties`; API access policy is stored separately with restricted file permissions. Chat messages are not persisted.

## Building

```bash
go build -buildvcs=false -trimpath -o bin/Tanpopo ./src/cmd/llamaloader
./scripts/build-mlx-server-runtime.sh  # Apple Silicon only
```

`run.command` calls `build.command --runtime` first. A runtime is reused only when its pinned version matches and no source file is newer than the prebuilt binary; otherwise it is rebuilt automatically. Set `TANPOPO_REBUILD_RUNTIMES=1` to force a runtime rebuild.

## License and notices

See [LICENSE](LICENSE), [COMMERCIAL-LICENSE.md](COMMERCIAL-LICENSE.md), and [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md). Bundled runtime components retain their respective upstream licenses. Security reporting and local secret-handling guidance are documented in [SECURITY.md](SECURITY.md).
