// 在隔離設定與隨機埠執行兩個真正的 Tanpopo Server；不操作既有管理服務。
import fs from "node:fs";
import path from "node:path";
import os from "node:os";
import net from "node:net";
import dgram from "node:dgram";
import cp from "node:child_process";
import crypto from "node:crypto";
import assert from "node:assert/strict";
import { fileURLToPath } from "node:url";
import { setTimeout as sleep } from "node:timers/promises";

const project = path.resolve(path.dirname(fileURLToPath(import.meta.url)), "..");
if (!process.argv[2]) {
  console.error("用法：node scripts/smoke-mlx-model-matrix.mjs <案例 JSON> [結果目錄]");
  process.exit(2);
}
const options = JSON.parse(fs.readFileSync(path.resolve(process.argv[2]), "utf8"));
assert.equal(process.platform, "darwin", "需在 Apple Silicon Mac 執行");
assert.equal(process.arch, "arm64", "需在 Apple Silicon Mac 執行");
assert(options.cases?.length > 0, "請提供 cases 陣列");
const modelRoot = fs.realpathSync(options.model_root);
const native = fs.realpathSync(options.runtime ?? path.join(project, "bin/mlx-runtime/prebuilt/darwin-arm64/bin/mlx-server"));
const directory = process.argv[3] ? path.resolve(process.argv[3]) : fs.mkdtempSync(path.join(os.tmpdir(), "tanpopo-model-matrix-"));
fs.mkdirSync(directory, { recursive: true });
assert(!fs.existsSync(path.join(directory, "result.json")), "結果目錄已有測試紀錄，請另選目錄");
const nodes = [];
let interrupted = false;
for (const signal of ["SIGINT", "SIGTERM"]) process.on(signal, () => {
  interrupted = true;
  // 只通知本次建立的 Server；關閉 stdin 後，由 Server／Runtime 回收子程序。
  for (const node of nodes) if (node.process?.exitCode === null && node.process.signalCode === null) node.process.kill("SIGTERM");
});
const report = {
  schema: 1, started_at: new Date().toISOString(), scope: "同機兩個獨立 Server，非實體雙機效能驗收",
  source_commit: cp.execFileSync("git", ["rev-parse", "HEAD"], { cwd: project, encoding: "utf8" }).trim(),
  platform: `${os.platform()}/${os.arch()}`, physical_memory_bytes: os.totalmem(),
  native_sha256: crypto.createHash("sha256").update(fs.readFileSync(native)).digest("hex"),
  script_sha256: crypto.createHash("sha256").update(fs.readFileSync(fileURLToPath(import.meta.url))).digest("hex"),
  capability: JSON.parse(cp.execFileSync(native, ["--distributed-capabilities"], { encoding: "utf8" })),
  settings: { temperature: 0, context_size: 4096, prefill_step_size: 128, kv_quantization: false }, cases: [],
};
const save = () => fs.writeFileSync(path.join(directory, "result.json"), JSON.stringify(report, null, 2) + "\n");
const writeJSON = (file, value) => fs.writeFileSync(file, JSON.stringify(value, null, 2), { mode: 0o600 });
const elapsed = start => Math.round(performance.now() - start);
function say(message) { console.log(`${new Date().toISOString()} ${message}`); }
async function freePort() {
  return new Promise((resolve, reject) => {
    const server = net.createServer(); server.on("error", reject);
    server.listen(0, "127.0.0.1", () => { const port = server.address().port; server.close(() => resolve(port)); });
  });
}
async function api(node, route, body, method, timeout = 180000) {
  const response = await fetch(node.url + route, { method: method ?? (body === undefined ? "GET" : "POST"),
    headers: { "Content-Type": "application/json" }, body: body === undefined ? undefined : JSON.stringify(body),
    signal: AbortSignal.timeout(timeout) });
  const text = await response.text();
  if (!response.ok) throw Error(`${route}: HTTP ${response.status} ${text.slice(0, 2000)}`);
  return JSON.parse(text);
}
async function until(label, check, timeout = 30000) {
  const deadline = Date.now() + timeout;
  while (Date.now() < deadline) { const result = await check(); if (result) return result; await sleep(250); }
  throw Error(`${label} 超過 ${timeout / 1000} 秒`);
}
async function ready(node, distributed) {
  return until("模型載入", async () => {
    const status = await api(node, "/api/runtime/status", undefined, undefined, 5000);
    if (status.running && status.ready) return status;
    const cluster = distributed ? await api(node, "/api/cluster/status", undefined, undefined, 5000) : {};
    if (!status.running && !cluster.session) throw Error(`模型已停止：${JSON.stringify({ status, cluster })}`);
    return false;
  }, 300000);
}
function same(actual, expected) {
  assert.equal(actual.content.trim(), expected.content.trim(), "單機／叢集回答不一致");
  assert.equal((actual.reasoning ?? "").trim(), (expected.reasoning ?? "").trim(), "推理內容不一致");
  for (const key of ["prompt_tokens", "completion_tokens", "total_tokens"])
    assert.equal(actual.usage[key], expected.usage[key], `${key} 不一致`);
  assert.equal(actual.finish_reason, expected.finish_reason, "結束原因不一致");
}
async function chat(node, body) {
  const start = performance.now();
  const result = await api(node, "/api/chat/completions", body);
  assert(result.content || result.reasoning, "模型未產生內容");
  assert(result.usage.completion_tokens > 0, "輸出 Token 必須大於零");
  return { ...result, elapsed_ms: elapsed(start) };
}
async function stream(node, body) {
  const start = performance.now();
  const response = await fetch(node.url + "/api/chat/completions", { method: "POST", headers: { "Content-Type": "application/json" },
    body: JSON.stringify({ ...body, stream: true }), signal: AbortSignal.timeout(180000) });
  assert.equal(response.status, 200);
  const text = await response.text(), result = { content: "", reasoning: "", elapsed_ms: elapsed(start) };
  let done = false;
  for (const line of text.split(/\r?\n/)) {
    if (!line.startsWith("data: ")) continue;
    const data = line.slice(6); if (data === "[DONE]") { done = true; continue; }
    const chunk = JSON.parse(data); assert(!chunk.error, JSON.stringify(chunk.error));
    for (const choice of chunk.choices ?? []) {
      result.content += choice.delta?.content ?? "";
      result.reasoning += choice.delta?.reasoning_content ?? choice.delta?.reasoning ?? "";
      if (choice.finish_reason) result.finish_reason = choice.finish_reason;
    }
    if (chunk.usage) result.usage = chunk.usage;
  }
  assert(done, "SSE 缺少 DONE"); assert(result.usage, "SSE 缺少 Token 統計");
  return result;
}
async function stopModels() {
  for (const node of nodes) await api(node, "/api/cluster/stop", {}, undefined, 30000);
  for (const node of nodes) await api(node, "/api/runtime/stop", {}, undefined, 30000);
  for (const node of nodes) {
    assert.equal((await api(node, "/api/runtime/status")).running, false, "Runtime 未停止");
    assert(!(await api(node, "/api/cluster/status")).session, "叢集未回收");
  }
}
function prompts(entry) {
  const text = message => ({ role: "user", content: message });
  const result = [
    { name: "英文短句", messages: [text("Reply with exactly these two words: Ring ready")] },
    { name: "中文多輪", messages: [text("請記住密語是藍色松鼠。"), { role: "assistant", content: "我記住了。" }, text("剛才的密語是什麼？只輸出密語。")] },
    { name: "跨區塊 Prefill", messages: [text(Array.from({ length: 96 }, (_, i) => `Item ${i + 1} is blue.`).join("\n") + "\nWhat color is item 80? Answer with one word.")] },
  ];
  if (entry.image) result.push({ name: "圖片", messages: [{ role: "user", content: [
    { type: "text", text: "請用一句話說明這張圖片的主要顏色。" },
    { type: "image_url", image_url: { url: "data:image/png;base64," + fs.readFileSync(options.image ? path.resolve(options.image) : path.join(project, "tests/distributed/testdata/red.png")).toString("base64") } },
  ] }] });
  return result.map(item => ({ ...item, body: { messages: item.messages, max_tokens: 16, stream: false } }));
}
async function runCase(entry) {
  const resolved = fs.realpathSync(path.join(modelRoot, entry.model)), relative = path.relative(modelRoot, resolved);
  assert(relative && !relative.startsWith("..") && !path.isAbsolute(relative), "案例模型須位於 model_root 內");
  const configData = fs.readFileSync(path.join(resolved, "config.json")), config = JSON.parse(configData);
  const weights = fs.readdirSync(resolved).filter(file => file.endsWith(".safetensors"));
  const row = { name: entry.name ?? entry.model, model: entry.model, architecture: config.model_type,
    config_sha256: crypto.createHash("sha256").update(configData).digest("hex"),
    weight_bytes: weights.reduce((sum, file) => sum + fs.statSync(path.join(resolved, file)).size, 0),
    quantization: config.quantization ?? config.quantization_config, image: Boolean(entry.image), checks: [] };
  report.cases.push(row); save();
  const start = performance.now(), coordinator = nodes[0];
  say(`開始 ${row.name}`);
  try {
    let since = performance.now();
    await api(coordinator, "/api/runtime/start", { model: entry.model, startup_command_id: coordinator.profile.id, kv_cache_quantization_enabled: false, mmap_enabled: false });
    row.single_status = await ready(coordinator, false); row.single_start_ms = elapsed(since);
    const cases = prompts(entry);
    for (const prompt of cases) {
      const check = { name: prompt.name, single: await chat(coordinator, prompt.body) };
      row.checks.push(check); save(); say(`${row.name} 單機 ${prompt.name}：${JSON.stringify(check.single.content)}`);
    }
    await api(coordinator, "/api/runtime/stop", {});
    const peer = await until("UDP 探索", async () => (await api(coordinator, "/api/cluster/status")).peers.find(item => item.id === nodes[1].id && !item.busy));
    since = performance.now();
    await api(coordinator, "/api/cluster/start", { model: entry.model, peer_ids: [peer.id], startup_command_id: coordinator.profile.id, kv_cache_quantization_enabled: false });
    row.cluster_status = await ready(coordinator, true); row.cluster_start_ms = elapsed(since);
    row.health = await (await fetch(row.cluster_status.url + "/health", { signal: AbortSignal.timeout(5000) })).json();
    assert.equal(row.health.distributed.world_size, 2);
    assert(row.health.distributed.sharded_layers > 0);
    assert(row.health.distributed.local_linear_bytes < row.health.distributed.original_linear_bytes);
    save();
    for (let index = 0; index < cases.length; index++) {
      const check = row.checks[index]; check.cluster = await chat(coordinator, cases[index].body); save();
      try { same(check.cluster, check.single); check.matched = true; }
      catch (error) { check.matched = false; check.error = error.message; }
      say(`${row.name} 叢集 ${check.name}：${check.matched ? "一致" : "不一致"}`); save();
    }
    // 使用同一張圖片或短文字，同時驗證串流 transport 不改變結果。
    const last = entry.image ? cases.length - 1 : 0;
    row.sse = await stream(coordinator, cases[last].body); same(row.sse, row.checks[last].single);
    assert(row.checks.every(check => check.matched), "至少一項單機／叢集結果不一致");
    row.passed = true;
  } catch (error) {
    row.passed = false; row.error = error.stack;
    row.logs = [];
    for (const node of nodes) { try { row.logs.push(await api(node, "/api/runtime/logs", undefined, undefined, 5000)); } catch {} }
    say(`${row.name} 失敗：${error.message}`);
  } finally {
    try { await stopModels(); row.stopped = true; }
    catch (error) { row.passed = false; row.cleanup_error = error.message; throw error; }
    finally { row.elapsed_ms = elapsed(start); save(); }
    say(`${row.name} ${row.passed ? "通過" : "未通過"}，${row.elapsed_ms} ms`);
  }
}
async function main() {
  const binary = path.join(directory, "tanpopo-server");
  cp.execFileSync("go", ["build", "-buildvcs=false", "-ldflags", "-X LlamaLoader/src/appversion.Repository=", "-o", binary, "./src/cmd/llamaloader"], { cwd: project, stdio: "inherit" });
  const runtime = path.join(directory, "mlx-runtime/prebuilt/darwin-arm64");
  fs.mkdirSync(runtime, { recursive: true }); fs.symlinkSync(path.dirname(native), path.join(runtime, "bin"));
  const socket = dgram.createSocket("udp4"); await new Promise(resolve => socket.bind(0, "127.0.0.1", resolve));
  const discovery = socket.address().port; socket.close();
  for (let index = 0; index < 2; index++) {
    const dir = path.join(directory, `node-${index}`); fs.mkdirSync(dir);
    const node = { url: "http://127.0.0.1:" + await freePort(), directory: dir }; nodes.push(node);
    writeJSON(path.join(dir, "settings.json"), { model_directory: modelRoot, mlx_model_directory: modelRoot,
      auto_performance_calibration_enabled: false, memory_pressure_protection_enabled: false });
    writeJSON(path.join(dir, "agent.properties"), { service_name: `Tanpopo 模型矩陣 ${index}`, http_host: "127.0.0.1", http_port: Number(new URL(node.url).port),
      web_path: path.join(project, "website"), settings_path: path.join(dir, "settings.json"), startup_commands_path: path.join(dir, "startup.json"),
      access_control_path: path.join(dir, "access.json"), runtime_state_path: path.join(dir, "runtime.json"), disable_authentication: true });
    const log = fs.openSync(path.join(dir, "server.log"), "a");
    node.process = cp.spawn(binary, ["-config", path.join(dir, "agent.properties")], { cwd: dir, env: { ...process.env, TANPOPO_UI: "shell" },
      detached: true, stdio: ["ignore", log, log] }); fs.closeSync(log);
    node.process.on("error", error => { node.spawnError = error; });
    await until("Server 啟動", async () => {
      if (node.spawnError) throw node.spawnError;
      if (node.process.exitCode !== null || node.process.signalCode !== null) throw Error("Server 提前結束");
      try { return await api(node, "/api/health", undefined, undefined, 1000); } catch { return false; }
    });
    node.profile = await api(node, "/api/startup-commands", { name: "通用模型矩陣 Smoke", runtime: "mlx-server", server_host: "127.0.0.1", server_port: await freePort(),
      context_size: 4096, gpu_layers: -1, threads: 0, mmap_reserve_gb: 0, extra_args: ["--no-thinking", "--temperature", "0", "--prefill-step-size", "128"] });
    await api(node, "/api/cluster/config", { enabled: true, discovery_port: discovery, interface: "lo0" }, "PUT");
    node.id = (await api(node, "/api/cluster/status")).local.id;
  }
  for (const entry of options.cases) {
    if (interrupted) throw Error("測試已中斷");
    await runCase(entry);
  }
  report.passed = report.cases.every(row => row.passed && row.stopped);
  report.completed_at = new Date().toISOString(); save();
  say(`完成：${report.cases.filter(row => row.passed).length}/${report.cases.length} 通過；${path.join(directory, "result.json")}`);
  if (!report.passed) process.exitCode = 1;
}
try { await main(); }
catch (error) { report.error = error.stack; save(); console.error(error); process.exitCode = 1; }
finally {
  for (const node of nodes) {
    try { await api(node, "/api/cluster/stop", {}, undefined, 15000); await api(node, "/api/runtime/stop", {}, undefined, 15000); } catch {}
    if (node.process?.pid && node.process.exitCode === null && node.process.signalCode === null) {
      node.process.kill("SIGTERM");
      try { await until("Server 結束", () => node.process.exitCode !== null || node.process.signalCode !== null, 20000); }
      catch { try { process.kill(-node.process.pid, "SIGKILL"); } catch {} }
    }
  }
}
