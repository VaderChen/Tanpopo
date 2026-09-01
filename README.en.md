# Tanpopo

[繁體中文](README.md) · [English](README.en.md) · [日本語](README.ja.md) · [한국어](README.ko.md)

Tanpopo is a local model service manager written in Go. Its name is the Japanese word for dandelion (たんぽぽ): generated tokens spread outward like dandelion seeds. It manages a cross-platform `llama-server` for GGUF models and a native Swift/MLX `mlx-server` on Apple Silicon.

## Highlights

- Start, stop, restore, and inspect local model runtimes from one web interface.
- Scan nested GGUF files and complete MLX model directories. GGUF, MLX, and mmproj selectors are alphabetized and display `model name (directory name)` while preserving the full safe path internally. Non-macOS systems hide the Apple Silicon-only `mlx-server` option.
- **Fast GGUF mode** is enabled by default when `mlx-server` loads GGUF. Its model-name-independent tensor capability policy selects INT4, INT8, BF16, and group sizing, then persists converted weights in `.fgguf`. System settings provide Mode 1 (balanced default), Mode 2 (higher accuracy), and Mode 3 (fastest). This general mechanism benefits most GGUF models that mlx-server can parse, but it does not guarantee that every architecture, quantization, or custom checkpoint will load, run faster, or preserve identical accuracy.
- Group mlx-server targets by GGUF and MLX. Language models not yet declared compatible by the runtime remain selectable in an **Untested (N)** group and are validated by an actual load attempt.
- Download public, gated, or private Hugging Face GGUF files and MLX repositories.
- Pick popular GGUF or MLX models from a separate JSON catalog. Each format is grouped into 8B-class, 30B-class, and 70B-and-above tiers, with model names sorted alphabetically inside each tier. The quick picker fills in the Runtime, repository, revision, and GGUF filename without hard-coding model entries in JavaScript.
- When the server supports byte ranges, download large files in 64 MiB segments with at most four workers; otherwise fall back to one stream. Completed queue entries are removed automatically. The desktop app can open the destination folder, while browser mode keeps that action disabled.
- Save reusable launch profiles for context size, GPU layers, threads, KV cache, MTP, and DFlash. Apple Silicon includes native MLX DFlash and MLX MTP profiles.
- Keep the independent DFlash and MMap switches in a compact Advanced settings popover so new options do not continuously lengthen the page.
- Detect DFlash support and require a compatible Draft model before enabling it. The separate MMap switch supports both llama-server and Apple Silicon mlx-server, using file-backed paging to reduce model-loading memory pressure.
- KV Cache, MMap, DFlash, and MTP are integrated end to end across feature defaults, launch profiles, runtime switches, compatibility preflight, runtime arguments, persisted state, and error reporting. Speculative decoding modes are mutually exclusive with one another and with KV Cache quantization.
- Automatically discover and download a matching DFlash Draft from the same or a separate Hugging Face repository, verified through metadata and model configuration instead of a hard-coded model list.
- Use an ephemeral local chat with Markdown, math rendering, and separated reasoning from API fields, `<think>` blocks, or `<|channel|>` thought channels. The reasoning panel opens by default while generation is active, shows animated dots, and collapses automatically when generation finishes; token counts and output tokens per second remain visible.
- Starting a model immediately opens a loading notice explaining that large models may require conversion. Runtime testing supports one run, three repeated runs with average and median throughput, or a long-output run requiring at least 500 output tokens.
- Use a real OpenAI-compatible per-token SSE stream from MLX; disconnecting or cancelling the client immediately cancels the matching generation task.
- Protect the model API with access keys, an IP allowlist, both, or neither.
- Restore the selected Runtime and running state before admin sign-in after restarting Tanpopo. A signed **Remember me** cookie also remains valid across service restarts and is invalidated by credential changes.
- Choose `AUTO`, Traditional Chinese, English, Japanese, or Korean for the management interface.
- Choose among Dandelion, Sky Blue, Sakura Pink, and the dark Midnight Purple theme.
- View CPU, GPU, MEMORY, and network status in a bottom bar refreshed every three seconds. Utilization uses muted green, yellow, and red bands at 50% and 80%.
- Inspect read-only OS, kernel, architecture, hostname, CPU, GPU, memory, network interface, and reachable management URL details under **System settings → System information**. Loopback URLs are omitted from shared URL lists.
- Check the latest stable GitHub Release at startup and every hour, including the build number for multiple releases on the same day. On Linux, an authenticated administrator can upload an official release ZIP under **System settings → About** to validate, install, and restart automatically.
- Linux packages include a Vulkan-enabled llama.cpp build path, dependency and GPU-permission checks, a reusable `build-llama-server.sh`, and DRM GPU-utilization fallback when ROCm tools are unavailable.
- On macOS, run as a native AppKit/WKWebView app and optionally remain in the system menu bar.

## Public test reports

- [Model compatibility report](https://vaderchen.github.io/Tanpopo/reports/model-compatibility.html): documents the supported scope and compatibility boundaries for native MLX, MLX loading GGUF, llama.cpp GGUF, multimodal projection, KV Cache quantization, and speculative decoding.
- [MLX and GGUF conversion loading speed and computational accuracy](https://vaderchen.github.io/Tanpopo/reports/performance-comparison.html): compares paired 4B, 9B, and 27B models across native MLX, llama.cpp + GGUF, and MLX + Fast GGUF Modes 1–3, including fixed 100-question results, throughput, conversion cache, and process RAM.

Both HTML reports support `AUTO`, Traditional Chinese, and English. Their results are reproducible snapshots for the stated date, hardware, runtime versions, and samples—not a promise that every model or device will behave identically, nor a compatibility, speed, or accuracy guarantee for Fast GGUF.

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

The application version uses `1.YY.MMDD build HHmm`, and GitHub tags use `v1.YY.MMDD-build-HHmm`. `run.command`, `run.sh`, `build.command`, and `pack.command` derive both values in the `Asia/Taipei` time zone. Set `TANPOPO_VERSION` or `TANPOPO_BUILD` only when intentionally producing a fixed release. Update checks compare both the date version and build number, so a later release from the same day is detected; drafts and prereleases are not treated as latest.

Official Linux x64 packages check and install missing compiler, CMake, Vulkan header, shader compiler, and GPU permission prerequisites. They use a packaged Vulkan runtime when present, otherwise build the pinned llama.cpp source locally through `build-llama-server.sh`.

## Model runtimes

### llama-server

`llama-server` provides the broadest GGUF and platform compatibility. The default GGUF directory is `~/services/models`. Tanpopo combines a selected model with a saved launch profile only when starting the runtime. Multimodal models may select a matching `mmproj` file.

DFlash is off by default. Tanpopo inspects model architecture and pairing metadata before enabling the switch. If the required Draft GGUF is missing, the switch is reset and the UI asks the user to download it.

MMap is a separate switch in the runtime page's Advanced settings popover, is off by default, and supports both `llama-server` and Apple Silicon `mlx-server`. llama-server uses `--load-mode mmap`; mlx-server maps supported safetensors and directly usable GGUF weights as file-backed pages. Launch profiles can use Auto or a 4, 8, 16, 24, 32, 48, 64, 96, or 128 GB memory reserve target. llama-server uses `--fit-target` to configure GPU Layers, while mlx-server uses physical memory minus the reserve as its MLX allocation target. This value is not a hard runtime memory limit. First-token latency and generation speed still depend on storage performance and page pressure.

Launch profiles select Q8 or Q4 KV Cache. A separate Advanced settings switch decides whether the selected format is used for the current launch; turning it off keeps the cache unquantized. Q4 saves more memory, while Q8 preserves more precision. KV Cache quantization and DFlash are mutually exclusive in both the UI and backend.

### mlx-server

`mlx-server` is a native Apple Silicon runtime built with Swift, SwiftNIO, and MLX Swift. It does not invoke Python, pip, or `mlx_lm.server`. It accepts complete MLX directories from `~/services/mlx-models` and supported GGUF files from the regular GGUF directory. Native MLX model types are reported dynamically by the bundled `mlx-swift-lm 3.31.4` registries and include multimodal Gemma 4. The runtime currently reports direct GGUF support for Gemma, Llama, Mimo, MiniCPM, Mistral, Qwen 2, Qwen 3, Qwen 3.5, and SmolLM3; other detected language models remain selectable in an Untested group and are validated by a real load attempt. When exactly one compatible `mmproj` is present beside a GGUF, mlx-server attaches it automatically. With no `mmproj`, the model loads as a text-only LLM; multiple candidates are never guessed.

**Fast GGUF mode** is the general GGUF optimization entry point for mlx-server and is enabled by default when a GGUF target is selected. A separate **Fast GGUF strategy** card in System settings stores both the default toggle and one of three strategies:

- **Mode 1 (balanced default)**: reuse representable K-Quant source 4-bit blocks at their required group 32; use group 64 for the remaining low-bit tensors.
- **Mode 2 (higher accuracy)**: requantize low-bit sources to INT8 with group 64.
- **Mode 3 (fastest)**: requantize low-bit sources to INT4 with strategy-controlled group 32, regardless of a manual group-size argument.

Disabling Fast GGUF uses the general `auto + group auto + recurrent off` conversion path. Every strategy follows tensor dtype, shape, source blocks, and architecture metadata rather than model filenames. The 32-element group in reused Q4_K data is defined by the source sub-block format and does not imply global group 32. The `quality` profile uses FP32 reference weights for diagnostics and is not a normal performance mode.

The strategy applies without per-model special cases and helps most GGUF models that mlx-server can parse, but it is not a compatibility, speed, or accuracy guarantee. DFlash continues to require an MLX safetensors target and a compatible Draft model. MTP supports compatible native MLX Target/Draft pairs and GGUF files whose architecture metadata and tensor contract expose embedded prediction layers; pairing is validated from metadata and shapes rather than filenames.

For mlx-server, enabling KV Cache quantization applies the profile's Q8 or Q4 mode with a group size of 64 and delayed quantization after 2,048 tokens. The profile Context Size becomes the quantized KV Cache limit.

KV Cache, MMap, DFlash, and MTP are first-class Tanpopo features rather than undocumented command-line flags. Settings persist model-feature defaults, launch profiles define detailed parameters, and the runtime page can override them for one load. The backend validates incompatible combinations, builds the runtime arguments, persists state, and reports failures to the UI.

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
