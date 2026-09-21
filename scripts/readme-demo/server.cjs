// 僅供 README 錄製：提供真實 website 靜態檔與記憶體內的示範 API。
// 不啟動模型、不連線外部服務、不讀取或保存使用者設定。
const http = require("node:http");
const fs = require("node:fs/promises");
const path = require("node:path");
const { setTimeout: delay } = require("node:timers/promises");
const fixtures = require("./fixtures.json");

async function startDemoServer() {
  const website = path.resolve(__dirname, "../../website");
  const state = {
    runtime: { running: false, ready: false, runtime: "mlx-server", startup_command_id: "demo-mlx", url: "" },
    startedAt: 0, downloadedAt: 0, job: null,
    settings: structuredClone(fixtures.settings),
    models: structuredClone(fixtures.models),
    requests: [], errors: []
  };
  function runtimeStatus() {
    if (state.startedAt) {
      state.runtime.ready = Date.now() - state.startedAt >= 1500;
      state.runtime.model_preparation = state.runtime.ready ? "ready" : "loading_cache";
    }
    return state.runtime;
  }
  function downloads() {
    if (!state.job) return [];
    const fraction = Math.min(1, (Date.now() - state.downloadedAt) / 6500);
    state.job.bytes_done = Math.round(state.job.bytes_total * fraction);
    if (fraction >= 1) {
      state.models.push({
        path: `gguf:Qwen3.5-2B/${state.job.filename}`, format: "gguf", size: state.job.bytes_total,
        modified_at: "2026-09-21T01:00:00Z", architecture: "qwen35"
      });
      state.job = null;
      return [];
    }
    return [state.job];
  }
  const json = (res, value, status = 200) => {
    res.writeHead(status, { "Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store" });
    res.end(JSON.stringify(value));
  };
  async function body(req) {
    let text = "";
    for await (const part of req) {
      text += part;
      if (text.length > 65536) throw new Error("示範請求過大");
    }
    return JSON.parse(text || "{}");
  }
  async function chat(res) {
    res.writeHead(200, { "Content-Type": "text/event-stream", "Cache-Control": "no-cache" });
    const send = (value) => res.write(`data: ${JSON.stringify(value)}\n\n`);
    for (const [field, content, interval] of [
      ["reasoning_content", fixtures.chat.reasoning, 95],
      ["content", fixtures.chat.answer, 56]
    ]) {
      for (const part of content.match(/.{1,4}|\n/g)) {
        if (res.destroyed) return;
        send({ choices: [{ index: 0, delta: { [field]: part }, finish_reason: null }] });
        await delay(interval);
      }
    }
    send({ choices: [{ index: 0, delta: {}, finish_reason: "stop" }] });
    // 不加入捏造的 tokens/sec 或效能數字，避免把演示節奏當作測速。
    res.end("data: [DONE]\n\n");
  }
  const server = http.createServer(async (req, res) => {
    try {
      const url = new URL(req.url, "http://127.0.0.1");
      const route = url.pathname;
      if (route.startsWith("/api/")) {
        state.requests.push(`${req.method} ${route}`);
        if (route === "/api/session") return json(res, { authenticated: true, authentication_enabled: false });
        if (route === "/api/settings" && req.method === "GET") return json(res, state.settings);
        if (route === "/api/app-version") return json(res, { update_available: false, current_version: "DEMO" });
        if (route === "/api/system/info") return json(res, { os_name: "macOS", architecture: "arm64" });
        if (route === "/api/system/metrics") {
          const active = runtimeStatus().ready;
          return json(res, {
            cpu: { available: true, percent: active ? 18.2 : 4.6, device: "DEMO" },
            gpu: { available: true, percent: active ? 42.8 : 0, device: "DEMO" },
            memory: { available: true, percent: active ? 36.4 : 21.7, device: "DEMO" }
          });
        }
        if (route === "/api/models") {
          downloads();
          let models = url.searchParams.get("role") === "draft" ? [] : state.models;
          if (url.searchParams.get("runtime") === "llama-server") {
            models = models.filter((m) => m.format === "gguf").map((m) => ({ ...m, path: m.path.replace(/^gguf:/, "") }));
          }
          return json(res, { models });
        }
        if (route === "/api/startup-commands") return json(res, { commands: fixtures.commands, capabilities: [] });
        if (route === "/api/runtime/status") return json(res, runtimeStatus());
        if (route === "/api/runtime/logs") return json(res, { logs: state.startedAt ? "[DEMO] Fast GGUF 快取已讀取\n[DEMO] 模型已就緒，提供 OpenAI 相容 API" : "" });
        if (route === "/api/runtime/conversion-preflight" && req.method === "POST") return json(res, { applicable: true, requires_conversion: false, cache_hit: true });
        if (route === "/api/runtime/start" && req.method === "POST") {
          const request = await body(req);
          const command = fixtures.commands.find((c) => c.id === request.startup_command_id);
          if (!command || !state.models.some((m) => m.path === request.model)) return json(res, { error: "不支援的示範模型或參數" }, 400);
          state.startedAt = Date.now();
          state.runtime = {
            running: true, ready: false, desired_running: true, runtime: command.runtime,
            pid: 24021, model: request.model, startup_command_id: command.id, startup_command_name: command.name,
            url: "http://127.0.0.1:8080", started_at: "2026-09-21T01:00:00Z",
            fast_gguf: Boolean(request.fast_gguf_enabled), mmap_enabled: Boolean(request.mmap_enabled),
            kv_cache_quantization: request.kv_cache_quantization_enabled ? command.kv_cache_quantization : "",
            effective_context_size: command.context_size
          };
          return json(res, runtimeStatus(), 202);
        }
        if (route === "/api/chat/completions" && req.method === "POST") {
          await body(req);
          if (!runtimeStatus().ready) return json(res, { error: "請先啟動示範模型" }, 409);
          return await chat(res);
        }
        if (route === "/api/downloads/repository-files") return json(res, { files: [fixtures.download.filename] });
        if (route === "/api/downloads" && req.method === "GET") return json(res, { downloads: downloads() });
        if (route === "/api/downloads" && req.method === "POST") {
          const request = await body(req);
          state.downloadedAt = Date.now();
          state.job = {
            id: "demo-download", runtime: request.runtime, repository: request.repository, filename: request.filename,
            revision: "main", destination: `/demo/models/${request.filename}`, state: "downloading",
            bytes_total: fixtures.download.bytes_total, bytes_done: 0
          };
          return json(res, { job: state.job, jobs: [state.job] }, 202);
        }
        state.errors.push(`${req.method} ${route}`);
        return json(res, { error: "此 API 不在示範範圍內" }, 404);
      }
      let file;
      if (route === "/" || route === "/demo") file = path.join(__dirname, "stage.html");
      else {
        file = path.resolve(website, "." + decodeURIComponent(route));
        if (!file.startsWith(website + path.sep)) return json(res, { error: "無效路徑" }, 403);
      }
      const data = await fs.readFile(file);
      const mime = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".json": "application/json", ".png": "image/png", ".woff2": "font/woff2" }[path.extname(file)] || "application/octet-stream";
      res.writeHead(200, { "Content-Type": mime, "Cache-Control": "no-store" });
      res.end(data);
    } catch (error) {
      state.errors.push(error.message);
      if (!res.headersSent) json(res, { error: "示範服務發生錯誤" }, 500);
      else res.end();
    }
  });
  await new Promise((resolve) => server.listen(0, "127.0.0.1", resolve));
  return { server, state, url: `http://127.0.0.1:${server.address().port}`, close: () => new Promise((resolve) => { server.close(resolve); server.closeAllConnections(); }) };
}

module.exports = { startDemoServer };
if (require.main === module) {
  startDemoServer().then(({ url, close }) => {
    console.log(`示範介面：${url}/demo`);
    for (const signal of ["SIGINT", "SIGTERM"]) process.once(signal, async () => { await close(); process.exit(0); });
  }).catch((error) => { console.error(error); process.exitCode = 1; });
}
