// 僅讀取 Tanpopo 的公開靜態圖檔；不啟停模型、不變更網路或 App 設定。
import http from "node:http";
import https from "node:https";
import fs from "node:fs";
import path from "node:path";
import { fileURLToPath } from "node:url";
import { setTimeout as sleep } from "node:timers/promises";

const asset = "/assets/tanpopo-icon.png";
const maximumBytes = 16 * 1024 * 1024;

function integer(value, name, low, high) {
  if (!Number.isInteger(value) || value < low || value > high) {
    throw new Error(`${name} 必須是 ${low}–${high} 的整數。`);
  }
  return value;
}

export async function diagnose(options, progress = () => {}) {
  const base = new URL(options.url);
  if (!["http:", "https:"].includes(base.protocol) || base.username || base.password ||
      base.pathname !== "/" || base.search || base.hash) {
    throw new Error("請提供不含帳密、路徑、查詢或片段的 http(s) Server 網址。");
  }
  const requests = integer(options.requests ?? 128, "requests", 1, 512);
  const interval = integer(options.intervalMs ?? 150, "interval-ms", 0, 10000);
  const timeout = integer(options.timeoutMs ?? 15000, "timeout-ms", 100, 60000);
  const controlInterval = integer(options.controlIntervalMs ?? 4000, "control-interval-ms", 100, 60000);
  const transport = base.protocol === "https:" ? https : http;
  const agent = new transport.Agent({ keepAlive: true, maxSockets: 1 });
  const start = performance.now();
  const report = { schema: 1, started_at: new Date().toISOString(), target: new URL(asset, base).href,
    planned_requests: requests, timeout_ms: timeout, interval_ms: interval,
    control_interval_ms: controlInterval, persistent: [], fresh: [] };
  const elapsed = () => Math.round(performance.now() - start);

  function transfer(index, persistent) {
    return new Promise(resolve => {
      const started = performance.now();
      const result = { index, start_ms: elapsed(), bytes: 0, reused: false, maximum_gap_ms: 0, slow_chunks: [] };
      let last = started, finished = false, deadline;
      const finish = error => {
        if (finished) return;
        finished = true;
        clearTimeout(deadline);
        result.duration_ms = Math.round(performance.now() - started);
        result.maximum_gap_ms = Math.max(result.maximum_gap_ms, Math.round(performance.now() - last));
        if (error) result.error = error.message;
        resolve(result);
      };
      const request = transport.get(report.target, { agent: persistent ? agent : false }, response => {
        result.headers_ms = Math.round(performance.now() - started);
        result.status = response.statusCode;
        result.reused = request.reusedSocket;
        response.on("error", finish);
        if (response.statusCode !== 200) {
          request.destroy(new Error(`圖檔回應 HTTP ${response.statusCode}；不跟隨重新導向。`));
          response.resume();
          return;
        }
        response.on("data", chunk => {
          const now = performance.now();
          const gap = Math.round(now - last);
          last = now;
          result.bytes += chunk.length;
          result.maximum_gap_ms = Math.max(result.maximum_gap_ms, gap);
          if (gap >= 100 && result.slow_chunks.length < 32) {
            result.slow_chunks.push({ elapsed_ms: Math.round(now - started), gap_ms: gap, bytes: chunk.length });
          }
          if (result.bytes > maximumBytes) request.destroy(new Error("圖檔超過 16 MiB，停止測量。"));
        });
        response.on("end", () => finish(result.bytes === 0 ? new Error("圖檔回應為空。") : undefined));
      });
      request.on("socket", socket => socket.setNoDelay(true));
      request.on("error", finish);
      // 採整筆期限，不能讓持續到達的少量資料無限延長傳輸。
      deadline = setTimeout(() => request.destroy(new Error(`整筆傳輸超過 ${timeout} ms。`)), timeout);
    });
  }

  let pendingControl;
  const controls = setInterval(() => {
    if (pendingControl) return;
    pendingControl = transfer(report.fresh.length, false).then(result => {
      report.fresh.push(result);
      progress("fresh", result);
    }).finally(() => { pendingControl = undefined; });
  }, controlInterval);
  try {
    for (let index = 0; index < requests; index++) {
      const result = await transfer(index, true);
      report.persistent.push(result);
      progress("persistent", result);
      if (result.error) break;
      if (index + 1 < requests) await sleep(interval);
    }
  } finally {
    clearInterval(controls);
    // 保留已開始的對照請求，避免將程式清理誤記為遠端連線失敗。
    await pendingControl;
    agent.destroy();
  }
  report.duration_ms = elapsed();
  report.received_bytes = [...report.persistent, ...report.fresh].reduce((sum, item) => sum + item.bytes, 0);
  report.completed = report.persistent.length === requests &&
    [...report.persistent, ...report.fresh].every(item => !item.error);
  return report;
}

const usage = `用法：node scripts/diagnose-lan-tcp.mjs --url http://<Server IPv4>:10082 [--output 新檔案.json]
選項：--requests 128 --interval-ms 150 --timeout-ms 15000 --control-interval-ms 4000
會重複下載約 1 MB 圖檔並以新連線對照；預設流量約 128 MB 加對照請求，不是線路峰值頻寬基準。`;

async function main(args) {
  if (args.length === 1 && args[0] === "--help") { console.log(usage); return; }
  const values = {};
  const names = new Set(["url", "output", "requests", "interval-ms", "timeout-ms", "control-interval-ms"]);
  for (let i = 0; i < args.length; i += 2) {
    const name = args[i].slice(2);
    if (!args[i].startsWith("--") || !names.has(name) || args[i + 1] === undefined || name in values) {
      throw new Error(usage);
    }
    values[name] = args[i + 1];
  }
  if (!values.url) throw new Error(usage);
  let output;
  if (values.output) output = fs.openSync(values.output, "wx");
  try {
    const report = await diagnose({ url: values.url,
      requests: values.requests === undefined ? undefined : Number(values.requests),
      intervalMs: values["interval-ms"] === undefined ? undefined : Number(values["interval-ms"]),
      timeoutMs: values["timeout-ms"] === undefined ? undefined : Number(values["timeout-ms"]),
      controlIntervalMs: values["control-interval-ms"] === undefined ? undefined : Number(values["control-interval-ms"]),
    }, (kind, item) => {
      if (kind === "fresh" || item.index % 16 === 0 || item.error) {
        console.error(`${kind === "fresh" ? "新連線" : "持續連線"} #${item.index + 1}：${item.bytes} bytes，${item.duration_ms} ms${item.error ? "，" + item.error : ""}`);
      }
    });
    const json = JSON.stringify(report, null, 2) + "\n";
    if (output !== undefined) fs.writeFileSync(output, json);
    else process.stdout.write(json);
    console.error(`測量${report.completed ? "完成" : "未通過"}，共接收 ${report.received_bytes} bytes；結果不能單獨判定故障設備或 MLX 相容性。`);
    if (!report.completed) process.exitCode = 1;
  } finally { if (output !== undefined) fs.closeSync(output); }
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  main(process.argv.slice(2)).catch(error => { console.error(error.message); process.exitCode = 1; });
}
